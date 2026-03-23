package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/commands"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/adminnotify"
	"github.com/u16-io/FindSenryu4Discord/pkg/backup"
	"github.com/u16-io/FindSenryu4Discord/pkg/health"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/pkg/permissions"
	"github.com/u16-io/FindSenryu4Discord/service"

	"github.com/ikawaha/kagome-dict/uni"
	"github.com/0x307e/go-haiku"
	"github.com/bwmarrin/discordgo"
)

var (
	startTime       time.Time
	adminNotifier   *adminnotify.Manager
	botReady        atomic.Bool
	guildCacheTimer atomic.Pointer[time.Timer]
	allSessions     []*discordgo.Session
	expectedShards  atomic.Int32
	connectedShards atomic.Int32

	// adminPermission is used for DefaultMemberPermissions on admin-only commands.
	adminPermission int64 = discordgo.PermissionAdministrator

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
			Name:                     "channel",
			Description:              "チャンネルタイプ別の川柳検出設定を変更します",
			DefaultMemberPermissions: &adminPermission,
		},
		{
			Name:        "doctor",
			Description: "このチャンネルでBotが正常に動作するか診断します",
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
		"channel": commands.HandleChannelCommand,
		"delete":  commands.HandleDeleteCommand,
		"doctor":  commands.HandleDoctorCommand,
		"detect":  commands.HandleDetectCommand,
		"admin":   commands.HandleAdminCommand,
		"contact": commands.HandleContactCommand,
	}
)

func main() {
	startTime = time.Now()

	// Initialize haiku dictionary
	haiku.UseDict(uni.Dict())

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

	// Get recommended shard count from Discord
	tmpSession, err := discordgo.New("Bot " + conf.Discord.Token)
	if err != nil {
		logger.Error("Failed to create Discord session", "error", err)
		os.Exit(1)
	}
	gatewayBot, err := tmpSession.GatewayBot()
	if err != nil {
		logger.Error("Failed to get gateway bot info", "error", err)
		os.Exit(1)
	}
	shardCount := gatewayBot.Shards
	if shardCount < 1 {
		shardCount = 1
	}
	logger.Info("Discord gateway info", "recommended_shards", gatewayBot.Shards, "using_shards", shardCount)

	// Gateway Intents
	intents := discordgo.IntentGuilds |
		discordgo.IntentGuildMessages |
		discordgo.IntentMessageContent

	// Create and open sessions for each shard
	expectedShards.Store(int32(shardCount))
	allSessions = make([]*discordgo.Session, shardCount)
	for i := 0; i < shardCount; i++ {
		s, err := discordgo.New("Bot " + conf.Discord.Token)
		if err != nil {
			logger.Error("Failed to create Discord session", "error", err, "shard", i)
			os.Exit(1)
		}
		s.ShardID = i
		s.ShardCount = shardCount
		s.Identify.Intents = intents

		s.AddHandler(messageCreate)
		s.AddHandler(interactionCreate)
		s.AddHandler(guildCreate)
		s.AddHandler(guildDelete)
		s.AddHandler(onConnect)

		if err := s.Open(); err != nil {
			logger.Error("Failed to open Discord connection", "error", err, "shard", i)
			os.Exit(1)
		}
		logger.Info("Shard connected", "shard_id", i, "shard_count", shardCount)
		allSessions[i] = s

		// Rate limit: wait between shard connections (Discord recommends ~5s)
		if i < shardCount-1 {
			time.Sleep(5 * time.Second)
		}
	}

	// Share all sessions with commands package for cross-shard guild counting
	commands.SetAllSessions(allSessions)

	// Use shard 0 as the primary session for command registration
	dg := allSessions[0]

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

	// Update database stats in metrics
	dbStats := db.GetStats()
	metrics.SetDatabaseStats(dbStats.SenryuCount, dbStats.MutedChannelCount)

	// Initialize admin notification manager
	if conf.Admin.LogChannelID != "" || conf.Admin.ReportChannelID != "" {
		adminNotifier = adminnotify.NewManager(dg, conf.Admin.LogChannelID, conf.Admin.ReportChannelID)
		adminNotifier.SetAllSessions(allSessions)
		adminNotifier.Start()
		adminNotifier.NotifyStarted()
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
		adminNotifier.NotifyStopping()
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

	// Close all Discord shard connections
	for _, s := range allSessions {
		if err := s.Close(); err != nil {
			logger.Error("Failed to close Discord connection", "error", err, "shard", s.ShardID)
		}
	}

	// Close database
	if err := db.Close(); err != nil {
		logger.Error("Failed to close database", "error", err)
	}

	logger.Info("Shutdown complete")
}

func onConnect(s *discordgo.Session, _ *discordgo.Connect) {
	n := connectedShards.Add(1)
	logger.Info("Gateway connected, caching guilds...", "shard", s.ShardID, "connected_shards", n, "expected_shards", expectedShards.Load())
	botReady.Store(false)
	// Reset debounce timer on new shard connection to prevent premature ready
	if t := guildCacheTimer.Load(); t != nil {
		t.Stop()
	}
}

func countAllGuilds() int {
	total := 0
	for _, s := range allSessions {
		if s != nil {
			total += len(s.State.Guilds)
		}
	}
	return total
}

func guildCreate(s *discordgo.Session, g *discordgo.GuildCreate) {
	metrics.SetConnectedGuilds(countAllGuilds())
	if !botReady.Load() {
		logger.Debug("Guild cache", "name", g.Name, "id", g.ID)
		// Debounce: reset timer on each GUILD_CREATE during cache burst.
		// When no more events arrive within 5s, mark as ready.
		if t := guildCacheTimer.Load(); t != nil {
			t.Stop()
		}
		t := time.AfterFunc(5*time.Second, func() {
			if connectedShards.Load() < expectedShards.Load() {
				// Not all shards connected yet; wait for remaining shards
				logger.Info("Guild cache paused, waiting for remaining shards",
					"guilds", countAllGuilds(),
					"connected_shards", connectedShards.Load(),
					"expected_shards", expectedShards.Load(),
				)
				return
			}
			total := countAllGuilds()
			logger.Info("Guild cache complete, bot is ready", "guilds", total, "shards", expectedShards.Load())
			metrics.SetConnectedGuilds(total)
			botReady.Store(true)
		})
		guildCacheTimer.Store(t)
		return
	}
	logger.Info("Joined guild", "name", g.Name, "id", g.ID)
	if adminNotifier != nil {
		adminNotifier.NotifyGuildJoin(g.Guild)
	}
}

func guildDelete(s *discordgo.Session, g *discordgo.GuildDelete) {
	logger.Info("Left guild", "id", g.ID)
	metrics.SetConnectedGuilds(countAllGuilds())

	// Clean up guild data
	senryuCount, err := service.DeleteSenryuByServer(g.ID)
	if err != nil {
		logger.Error("Failed to clean up guild data", "error", err, "guild_id", g.ID, "type", "senryus")
	}
	optOutCount, err := service.DeleteOptOutByServer(g.ID)
	if err != nil {
		logger.Error("Failed to clean up guild data", "error", err, "guild_id", g.ID, "type", "opt-outs")
	}
	channelConfigCount, err := service.DeleteChannelConfigByGuild(g.ID)
	if err != nil {
		logger.Error("Failed to clean up guild data", "error", err, "guild_id", g.ID, "type", "channel-config")
	}

	logger.Info("Guild data cleaned up",
		"guild_id", g.ID,
		"senryus", senryuCount,
		"opt_outs", optOutCount,
		"channel_configs", channelConfigCount,
	)

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
	case strings.HasPrefix(customID, commands.ChannelTogglePrefix):
		commands.HandleChannelToggle(s, i)
	}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	metrics.RecordMessageProcessed()

	ch, err := s.State.Channel(m.ChannelID)
	if err != nil {
		ch, err = s.Channel(m.ChannelID)
		if err != nil {
			logger.Warn("Failed to get channel", "error", err, "channel_id", m.ChannelID)
			metrics.RecordError("discord_api")
			return
		}
	}

	// DM channels are not supported
	switch ch.Type {
	case discordgo.ChannelTypeDM, discordgo.ChannelTypeGroupDM:
		s.ChannelMessageSend(m.ChannelID, "個チャはダメです")
		return
	}

	// Check if this channel type is enabled for the guild
	if !service.IsChannelTypeEnabled(m.GuildID, ch.Type) {
		return
	}

	// Skip senryu features in admin guild
	if m.GuildID == permissions.GetAdminGuildID() {
		return
	}

	if handleYomeYomuna(m, s) {
		return
	}

	if !service.IsMute(m.ChannelID) && !isParentChannelMuted(ch) {
		if m.Author.ID != s.State.User.ID {
			if service.IsDetectionOptedOut(m.GuildID, m.Author.ID) {
				return
			}
			if containsDiscordTokens(m.Content) {
				return
			}
			content := m.Content
			spoiler := containsSpoiler(content)
			if spoiler {
				content = stripSpoilerMarkers(content)
			}
			h := findHaikuSafe(content, []int{5, 7, 5})
			if len(h) != 0 {
				senryu := strings.Split(h[0], " ")
				created, err := service.CreateSenryu(
					model.Senryu{
						ServerID:  m.GuildID,
						AuthorID:  m.Author.ID,
						Kamigo:    senryu[0],
						Nakasichi: senryu[1],
						Simogo:    senryu[2],
						Spoiler:   &spoiler,
					},
				)
				if err != nil {
					logger.Error("Failed to create senryu", "error", err)
					metrics.RecordError("database")
					return
				}
				replyText := fmt.Sprintf("川柳を検出しました！\n「%s」", h[0])
				if spoiler {
					replyText = fmt.Sprintf("川柳を検出しました！\n||「%s」||", h[0])
				}
				if _, err := s.ChannelMessageSendReply(
					m.ChannelID,
					replyText,
					m.Reference(),
				); err != nil {
					logger.Warn("Failed to send senryu reply", "error", err, "channel_id", m.ChannelID)
					// 返信に失敗した場合、保存した川柳を削除して整合性を保つ
					if delErr := service.DeleteSenryu(int(created.ID), m.GuildID); delErr != nil {
						logger.Error("Failed to rollback senryu after reply failure", "error", delErr, "senryu_id", created.ID)
					} else {
						logger.Info("Rolled back senryu after reply failure", "senryu_id", created.ID, "channel_id", m.ChannelID)
					}
				}
			}
		}
	}
}

var medals = []string{"🥇", "🥈", "🥉", "🎖️", "🎖️"}

func handleMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("mute")

	if err := service.ToMute(i.ChannelID, i.GuildID); err != nil {
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

	guild, err := s.State.Guild(i.GuildID)
	if err != nil {
		guild, err = s.Guild(i.GuildID)
		if err != nil {
			logger.Warn("Failed to get guild for rank embed", "error", err, "guild_id", i.GuildID)
		}
	}

	embed := discordgo.MessageEmbed{
		Type:      discordgo.EmbedTypeRich,
		Title:     "サーバー内ランキング",
		Timestamp: time.Now().Format(time.RFC3339),
		Fields: []*discordgo.MessageEmbedField{},
	}
	if guild != nil {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text:    guild.Name,
			IconURL: guild.IconURL(""),
		}
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
			if errors.Is(err, service.ErrSenryuNotFound) {
				s.ChannelMessageSendReply(m.ChannelID, "まだ誰も詠んでいません。", m.Reference())
			} else {
				logger.Error("Failed to get last senryu", "error", err)
				s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
			}
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

// isParentChannelMuted checks if the parent channel of a thread is muted.
func isParentChannelMuted(ch *discordgo.Channel) bool {
	if ch.ParentID == "" {
		return false
	}
	return service.IsMute(ch.ParentID)
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

// containsDiscordTokens reports whether s contains Discord-specific tokens
// (mentions, channels, roles, custom emoji, URLs) that should exclude
// the message from haiku detection.
var reDiscordTokens = regexp.MustCompile(
	`<@!?\d+>` + // user mentions
		`|<#\d+>` + // channel mentions
		`|<@&\d+>` + // role mentions
		`|<a?:\w+:\d+>` + // custom emoji
		`|https?://\S+`, // URLs
)

func containsDiscordTokens(s string) bool {
	return reDiscordTokens.MatchString(s)
}

// findHaikuSafe wraps haiku.Find with recover to prevent panics from crashing the bot.
func findHaikuSafe(content string, rule []int) (result []string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warn("Recovered from panic in haiku.Find", "error", r, "content_len", len(content))
			result = nil
		}
	}()
	return haiku.Find(content, rule)
}

var reSpoiler = regexp.MustCompile(`\|\|.+?\|\|`)

func containsSpoiler(s string) bool {
	return reSpoiler.MatchString(s)
}

func stripSpoilerMarkers(s string) string {
	return strings.ReplaceAll(s, "||", "")
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
