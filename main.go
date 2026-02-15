package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/u16-io/FindSenryu4Discord/commands"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/backup"
	"github.com/u16-io/FindSenryu4Discord/pkg/health"
	"github.com/u16-io/FindSenryu4Discord/pkg/adminnotify"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/pkg/permissions"
	"github.com/u16-io/FindSenryu4Discord/service"

	"github.com/0x307e/go-haiku"
	"github.com/bwmarrin/discordgo"
)

var (
	startTime           time.Time
	adminNotifier *adminnotify.Manager
	botReady            atomic.Bool

	userCommands = []*discordgo.ApplicationCommand{
		{
			Name:        "mute",
			Description: "このチャンネルでの川柳検出をミュートします",
		},
		{
			Name:        "unmute",
			Description: "このチャンネルでの川柳検出のミュートを解除します",
		},
		{
			Name:        "rank",
			Description: "ギルド内で詠んだ回数が多い人のランキングを表示します",
		},
		{
			Name:        "delete",
			Description: "自分の川柳を削除します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "削除対象のユーザー（管理者のみ）",
					Required:    false,
				},
			},
		},
		{
			Name:        "detect",
			Description: "自分の川柳検出のオン/オフを切り替えます",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "on",
					Description: "川柳検出を有効にします",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "off",
					Description: "川柳検出を無効にします",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "status",
					Description: "現在の川柳検出設定を表示します",
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"mute":    handleMuteCommand,
		"unmute":  handleUnmuteCommand,
		"rank":    handleRankCommand,
		"delete":  commands.HandleDeleteCommand,
		"detect":  commands.HandleDetectCommand,
		"admin":   commands.HandleAdminCommand,
		"contact": commands.HandleContactCommand,
	}
)

func main() {
	startTime = time.Now()

	// Load configuration
	conf, err := config.Load("config.toml")
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(logger.Config{
		Level:  conf.Log.Level,
		Format: conf.Log.Format,
	})

	logger.Info("Starting FindSenryu4Discord",
		"log_level", conf.Log.Level,
		"db_driver", conf.Database.Driver,
	)

	// Initialize database
	if err := db.Init(); err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// Start health check server
	healthServer, err := health.StartServer()
	if err != nil {
		logger.Error("Failed to start health server", "error", err)
	}

	// Initialize backup manager
	var backupManager *backup.Manager
	if conf.Database.Driver == "sqlite3" && conf.Backup.Enabled {
		backupManager = backup.NewManager(conf.Backup, conf.Database.Path)
		backupManager.Start()
		commands.SetBackupManager(backupManager)
	}
	commands.SetStartTime(startTime)

	// Create Discord session
	dg, err := discordgo.New("Bot " + conf.Discord.Token)
	if err != nil {
		logger.Error("Failed to create Discord session", "error", err)
		os.Exit(1)
	}

	// Add handlers
	dg.AddHandler(messageCreate)
	dg.AddHandler(interactionCreate)
	dg.AddHandler(guildCreate)
	dg.AddHandler(guildDelete)

	// Open connection
	if err := dg.Open(); err != nil {
		logger.Error("Failed to open Discord connection", "error", err)
		os.Exit(1)
	}

	// Conditionally add /contact command
	if conf.Admin.ContactChannelID != "" {
		userCommands = append(userCommands, &discordgo.ApplicationCommand{
			Name:        "contact",
			Description: "Bot管理者にお問い合わせを送信します",
		})
	}

	// Register user commands (global)
	logger.Info("Registering user slash commands...")
	registeredUserCommands := make([]*discordgo.ApplicationCommand, len(userCommands))
	for i, cmd := range userCommands {
		rcmd, err := dg.ApplicationCommandCreate(dg.State.User.ID, "", cmd)
		if err != nil {
			logger.Error("Failed to register command", "command", cmd.Name, "error", err)
		} else {
			registeredUserCommands[i] = rcmd
			logger.Info("Registered command", "command", cmd.Name)
		}
	}

	// Register admin commands (guild-specific)
	adminGuildID := permissions.GetAdminGuildID()
	var registeredAdminCommands []*discordgo.ApplicationCommand
	if adminGuildID != "" {
		logger.Info("Registering admin slash commands...", "guild_id", adminGuildID)
		for _, cmd := range commands.AdminCommands() {
			rcmd, err := dg.ApplicationCommandCreate(dg.State.User.ID, adminGuildID, cmd)
			if err != nil {
				logger.Error("Failed to register admin command", "command", cmd.Name, "error", err)
			} else {
				registeredAdminCommands = append(registeredAdminCommands, rcmd)
				logger.Info("Registered admin command", "command", cmd.Name, "guild_id", adminGuildID)
			}
		}
	}

	// Update game status
	dg.UpdateGameStatus(1, conf.Discord.Playing)

	// Log connected guilds
	logger.Info("Connected guilds", "count", len(dg.State.Guilds))
	metrics.SetConnectedGuilds(len(dg.State.Guilds))
	for _, guild := range dg.State.Guilds {
		logger.Debug("Connected to guild", "name", guild.Name, "id", guild.ID)
	}

	// Update database stats in metrics
	dbStats := db.GetStats()
	metrics.SetDatabaseStats(dbStats.SenryuCount, dbStats.MutedChannelCount)

	// Initialize admin notification manager
	if conf.Admin.LogChannelID != "" {
		adminNotifier = adminnotify.NewManager(dg, conf.Admin.LogChannelID)
		adminNotifier.Start()
	}
	botReady.Store(true)

	// Mark as ready
	if healthServer != nil {
		healthServer.SetReady(true)
	}

	logger.Info("Bot is now running. Press CTRL-C to exit.")

	// Wait for termination signal
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Graceful shutdown
	logger.Info("Shutting down...")

	// Mark as not ready
	if healthServer != nil {
		healthServer.SetReady(false)
	}

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop admin notification manager
	if adminNotifier != nil {
		adminNotifier.Stop(ctx)
	}

	// Stop backup manager
	if backupManager != nil {
		backupManager.Stop(ctx)
	}

	// Stop health server
	if healthServer != nil {
		if err := healthServer.Stop(ctx); err != nil {
			logger.Error("Failed to stop health server", "error", err)
		}
	}

	// Remove slash commands
	logger.Info("Removing user slash commands...")
	for _, cmd := range registeredUserCommands {
		if cmd != nil {
			if err := dg.ApplicationCommandDelete(dg.State.User.ID, "", cmd.ID); err != nil {
				logger.Error("Failed to delete command", "command", cmd.Name, "error", err)
			}
		}
	}

	// Remove admin commands
	if adminGuildID != "" {
		logger.Info("Removing admin slash commands...")
		for _, cmd := range registeredAdminCommands {
			if cmd != nil {
				if err := dg.ApplicationCommandDelete(dg.State.User.ID, adminGuildID, cmd.ID); err != nil {
					logger.Error("Failed to delete admin command", "command", cmd.Name, "error", err)
				}
			}
		}
	}

	// Close Discord connection
	if err := dg.Close(); err != nil {
		logger.Error("Failed to close Discord connection", "error", err)
	}

	// Close database
	if err := db.Close(); err != nil {
		logger.Error("Failed to close database", "error", err)
	}

	logger.Info("Shutdown complete")
}

func guildCreate(s *discordgo.Session, g *discordgo.GuildCreate) {
	logger.Info("Joined guild", "name", g.Name, "id", g.ID)
	metrics.SetConnectedGuilds(len(s.State.Guilds))
	if botReady.Load() && adminNotifier != nil {
		adminNotifier.NotifyGuildJoin(g.Guild)
	}
}

func guildDelete(s *discordgo.Session, g *discordgo.GuildDelete) {
	logger.Info("Left guild", "id", g.ID)
	metrics.SetConnectedGuilds(len(s.State.Guilds))

	// Clean up guild data
	senryuCount, err := service.DeleteSenryuByServer(g.ID)
	if err != nil {
		logger.Error("Failed to clean up senryus on guild leave", "error", err, "guild_id", g.ID)
	}
	optOutCount, err := service.DeleteOptOutByServer(g.ID)
	if err != nil {
		logger.Error("Failed to clean up opt-outs on guild leave", "error", err, "guild_id", g.ID)
	}

	if botReady.Load() && adminNotifier != nil {
		adminNotifier.NotifyGuildLeave(g, senryuCount, optOutCount)
	}
}

func interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	case discordgo.InteractionMessageComponent:
		handleComponentInteraction(s, i)
	case discordgo.InteractionModalSubmit:
		handleModalSubmitInteraction(s, i)
	}
}

func handleModalSubmitInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.ModalSubmitData().CustomID
	switch {
	case customID == commands.ContactModalCustomID:
		commands.HandleContactModalSubmit(s, i)
	case strings.HasPrefix(customID, commands.ReplyModalPrefix):
		commands.HandleContactReplyModalSubmit(s, i)
	}
}

func handleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	switch {
	case customID == commands.DeleteSelectCustomID:
		commands.HandleDeleteSelectMenu(s, i)
	case strings.HasPrefix(customID, commands.DeleteConfirmPrefix):
		commands.HandleDeleteConfirm(s, i)
	case customID == commands.DeleteCancelCustomID:
		commands.HandleDeleteCancel(s, i)
	case strings.HasPrefix(customID, commands.ContactReplyPrefix):
		commands.HandleContactReplyButton(s, i)
	}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	metrics.RecordMessageProcessed()

	ch, err := s.Channel(m.ChannelID)
	if err != nil {
		logger.Warn("Failed to get channel", "error", err, "channel_id", m.ChannelID)
		metrics.RecordError("discord_api")
		return
	}

	// Channel type behavior
	switch ch.Type {
	case discordgo.ChannelTypeDM, discordgo.ChannelTypeGroupDM:
		s.ChannelMessageSend(m.ChannelID, "個チャはダメです")
		return
	case discordgo.ChannelTypeGuildVoice, discordgo.ChannelTypeGuildStageVoice:
		return
	}

	if ch.Type != discordgo.ChannelTypeGuildText {
		return
	}

	// Skip senryu features in admin guild
	if m.GuildID == permissions.GetAdminGuildID() {
		return
	}

	if handleYomeYomuna(m, s) {
		return
	}

	if !service.IsMute(m.ChannelID) {
		if m.Author.ID != s.State.User.ID {
			if service.IsDetectionOptedOut(m.GuildID, m.Author.ID) {
				return
			}
			h := haiku.Find(m.Content, []int{5, 7, 5})
			if len(h) != 0 {
				senryu := strings.Split(h[0], " ")
				_, err := service.CreateSenryu(
					model.Senryu{
						ServerID:  m.GuildID,
						AuthorID:  m.Author.ID,
						Kamigo:    senryu[0],
						Nakasichi: senryu[1],
						Simogo:    senryu[2],
					},
				)
				if err != nil {
					logger.Error("Failed to create senryu", "error", err)
					metrics.RecordError("database")
				}
				if _, err := s.ChannelMessageSendReply(
					m.ChannelID,
					fmt.Sprintf("川柳を検出しました！\n「%s」", h[0]),
					m.Reference(),
				); err != nil {
					logger.Warn("Failed to send senryu reply", "error", err, "channel_id", m.ChannelID)
				}
			}
		}
	}
}

var medals = []string{"🥇", "🥈", "🥉", "🎖️", "🎖️"}

func handleMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("mute")

	if err := service.ToMute(i.ChannelID); err != nil {
		logger.Error("Failed to mute channel", "error", err, "channel_id", i.ChannelID)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "ミュートに失敗しました ❌",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	} else {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "このチャンネルでの川柳検出をミュートしました ✅",
			},
		})
	}
}

func handleUnmuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("unmute")

	if err := service.ToUnMute(i.ChannelID); err != nil {
		logger.Error("Failed to unmute channel", "error", err, "channel_id", i.ChannelID)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "ミュート解除に失敗しました ❌",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	} else {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "このチャンネルでの川柳検出のミュートを解除しました ✅",
			},
		})
	}
}

func handleRankCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("rank")

	ranks, err := service.GetRanking(i.GuildID)
	if err != nil {
		logger.Error("Failed to get ranking", "error", err, "guild_id", i.GuildID)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "ランキングの取得に失敗しました",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	guild, err := s.Guild(i.GuildID)
	if err != nil {
		logger.Warn("Failed to get guild for rank embed", "error", err, "guild_id", i.GuildID)
	}

	embed := discordgo.MessageEmbed{
		Type:      discordgo.EmbedTypeRich,
		Title:     "サーバー内ランキング",
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text:    "This bot was made by 0x307e.",
			IconURL: "https://github.com/0x307e.png",
		},
		Fields: []*discordgo.MessageEmbedField{},
	}
	if guild != nil {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: guild.IconURL(""),
		}
	}

	for _, rank := range ranks {
		member, err := s.GuildMember(i.GuildID, rank.AuthorId)
		if err != nil {
			continue
		}
		displayName := member.Nick
		if displayName == "" {
			displayName = member.User.GlobalName
		}
		if displayName == "" {
			displayName = member.User.Username
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s 第%d位: %d回", medals[rank.Rank-1], rank.Rank, rank.Count),
			Value:  displayName,
			Inline: true,
		})
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{&embed},
		},
	})
}

func handleYomeYomuna(m *discordgo.MessageCreate, s *discordgo.Session) bool {
	switch m.Content {
	case "詠め":
		senryus, err := service.GetThreeRandomSenryus(m.GuildID)
		if err != nil {
			logger.Error("Failed to get random senryus", "error", err)
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
			return true
		}
		if len(senryus) == 0 {
			if _, err := s.ChannelMessageSend(m.ChannelID, "まだ誰も詠んでいません。あなたが先に詠んでください。"); err != nil {
				logger.Warn("Failed to send message", "error", err, "channel_id", m.ChannelID)
			}
		} else {
			if _, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("ここで一句\n「%s」\n詠み手: %s",
				strings.Join([]string{
					senryus[0].Kamigo,
					senryus[1].Nakasichi,
					senryus[2].Simogo,
				}, " "), strings.Join(getWriters(senryus, m.GuildID, s), ", "))); err != nil {
				logger.Warn("Failed to send senryu message", "error", err, "channel_id", m.ChannelID)
			}
		}
		return true
	case "詠むな":
		senryu, err := service.GetLastSenryu(m.GuildID, m.Author.ID)
		if err != nil {
			logger.Error("Failed to get last senryu", "error", err)
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		} else {
			if _, err := s.ChannelMessageSendReply(
				m.ChannelID,
				senryu,
				m.Reference(),
			); err != nil {
				logger.Warn("Failed to send reply", "error", err, "channel_id", m.ChannelID)
			}
		}
		return true
	}
	return false
}

func sliceUnique(target []string) (unique []string) {
	m := map[string]bool{}
	for _, v := range target {
		if !m[v] {
			m[v] = true
			unique = append(unique, v)
		}
	}
	return unique
}

func getWriters(senryus []model.Senryu, guildID string, session *discordgo.Session) []string {
	var writers []string
	for _, senryu := range senryus {
		member, err := session.GuildMember(guildID, senryu.AuthorID)
		if err != nil {
			continue
		}
		if member.Nick != "" {
			writers = append(writers, member.Nick)
		} else {
			writers = append(writers, member.User.Username)
		}
	}
	return sliceUnique(writers)
}
