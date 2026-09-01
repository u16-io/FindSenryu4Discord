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
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/commands"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/adminnotify"
	"github.com/u16-io/FindSenryu4Discord/pkg/backup"
	"github.com/u16-io/FindSenryu4Discord/pkg/crypto"
	"github.com/u16-io/FindSenryu4Discord/pkg/health"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/pkg/permissions"
	"github.com/u16-io/FindSenryu4Discord/service"

	"github.com/0x307e/go-haiku"
	"github.com/bwmarrin/discordgo"
	"github.com/ikawaha/kagome-dict/uni"
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

	// manageChannelPermission is used for DefaultMemberPermissions on channel management commands.
	manageChannelPermission int64 = discordgo.PermissionManageChannels

	userCommands = []*discordgo.ApplicationCommand{
		{
			Name:                     "mute",
			Description:              "このチャンネルでの川柳検出をミュートします",
			DefaultMemberPermissions: &manageChannelPermission,
		},
		{
			Name:                     "unmute",
			Description:              "このチャンネルでの川柳検出のミュートを解除します",
			DefaultMemberPermissions: &manageChannelPermission,
		},
		{
			Name:        "rank",
			Description: "ギルド内で詠んだ回数が多い人のランキングを表示します",
		},
		{
			Name:        "delete",
			Description: "指定ユーザーの川柳を削除します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "削除対象のユーザー",
					Required:    true,
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
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "ban",
					Description: "指定ユーザーの川柳検出を無効化します（管理者専用）",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "user",
							Description: "対象ユーザー",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "unban",
					Description: "指定ユーザーの川柳検出無効化を解除します（管理者専用）",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "user",
							Description: "対象ユーザー",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "川柳検出無効化ユーザー一覧を表示します（管理者専用）",
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"mute":    commands.HandleMuteCommand,
		"unmute":  commands.HandleUnmuteCommand,
		"rank":    handleRankCommand,
		"channel": commands.HandleChannelCommand,
		"delete":  commands.HandleDeleteCommand,
		"doctor":  commands.HandleDoctorCommand,
		"detect":  commands.HandleDetectCommand,
		"admin":   commands.HandleAdminCommand,
		"contact": commands.HandleContactCommand,
	}
)

type guildEventTracker struct {
	unavailableGuilds map[string]struct{}
}

func newGuildEventTracker() *guildEventTracker {
	return &guildEventTracker{
		unavailableGuilds: make(map[string]struct{}),
	}
}

func registerGatewayHandlers(s *discordgo.Session) {
	// DiscordGo otherwise runs typed handlers in independent goroutines, which can
	// reorder guild availability transitions. Keep dispatch ordered and opt only
	// the handlers that do substantial work back into asynchronous execution.
	s.SyncEvents = true
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		go messageCreate(s, m)
	})
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		go interactionCreate(s, i)
	})
	tracker := newGuildEventTracker()
	s.AddHandler(tracker.guildCreate)
	s.AddHandler(tracker.guildDelete)
	s.AddHandler(onConnect)
}

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

	// Initialize encryption
	if err := crypto.Init(conf.Encryption.Key); err != nil {
		logger.Error("Failed to initialize encryption", "error", err)
		os.Exit(1)
	}
	conf.Encryption.Key = "" // zero out key from config struct
	if crypto.IsEnabled() {
		logger.Info("Senryu encryption enabled")
	}

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

		registerGatewayHandlers(s)

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
			Name:                     "contact",
			Description:              "Bot管理者にお問い合わせを送信します",
			DefaultMemberPermissions: &adminPermission,
		})
	}

	// Register user commands (global)
	logger.Info("Registering user slash commands...")
	for _, cmd := range userCommands {
		if _, err := dg.ApplicationCommandCreate(dg.State.User.ID, "", cmd); err != nil {
			logger.Error("Failed to register command", "command", cmd.Name, "error", err)
		} else {
			logger.Info("Registered command", "command", cmd.Name)
		}
	}

	// Register admin commands (guild-specific)
	adminGuildID := permissions.GetAdminGuildID()
	if adminGuildID != "" {
		logger.Info("Registering admin slash commands...", "guild_id", adminGuildID)
		for _, cmd := range commands.AdminCommands() {
			if _, err := dg.ApplicationCommandCreate(dg.State.User.ID, adminGuildID, cmd); err != nil {
				logger.Error("Failed to register admin command", "command", cmd.Name, "error", err)
			} else {
				logger.Info("Registered admin command", "command", cmd.Name, "guild_id", adminGuildID)
			}
		}
	}

	// Update game status
	dg.UpdateGameStatus(1, conf.Discord.Playing)

	// Update database stats in metrics
	dbStats := db.GetStats()
	metrics.SetDatabaseStats(dbStats.SenryuCount, dbStats.MutedChannelCount, dbStats.OptOutCount)

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

	// Slash commands are intentionally NOT removed on shutdown.
	// ApplicationCommandCreate (called on startup) is an upsert, so commands
	// persist across restarts without the up-to-1-hour global propagation delay.

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
	if t := guildCacheTimer.Swap(nil); t != nil {
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

func resetGuildCacheTimer() {
	// Debounce: reset timer on each GUILD_CREATE during cache burst.
	// When no more events arrive within 5s, mark as ready.
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
	if previous := guildCacheTimer.Swap(t); previous != nil {
		previous.Stop()
	}
}

func cacheGuildCreate(g *discordgo.GuildCreate) {
	logger.Debug("Guild cache", "name", g.Name, "id", g.ID)
	// Register existing guilds so reconnect doesn't trigger welcome messages
	commands.MarkGuildWelcomeSent(g.ID)
	resetGuildCacheTimer()
}

func notifyGuildJoin(s *discordgo.Session, g *discordgo.GuildCreate) {
	logger.Info("Joined guild", "name", g.Name, "id", g.ID)
	notifier := adminNotifier
	go func() {
		if notifier != nil {
			notifier.NotifyGuildJoin(g.Guild)
		}
		commands.SendWelcomeMessage(s, g)
	}()
}

func (t *guildEventTracker) consumeRecovery(guildID string) bool {
	if _, recovered := t.unavailableGuilds[guildID]; !recovered {
		return false
	}
	delete(t.unavailableGuilds, guildID)
	return true
}

func (t *guildEventTracker) guildCreate(s *discordgo.Session, g *discordgo.GuildCreate) {
	metrics.SetConnectedGuilds(countAllGuilds())
	if g.Unavailable {
		logger.Warn("Guild unavailable", "id", g.ID)
		if !botReady.Load() {
			cacheGuildCreate(g)
		}
		return
	}

	if t.consumeRecovery(g.ID) {
		logger.Info("Guild available again", "name", g.Name, "id", g.ID)
		if !botReady.Load() {
			cacheGuildCreate(g)
		}
		return
	}

	if !botReady.Load() {
		cacheGuildCreate(g)
		return
	}

	notifyGuildJoin(s, g)
}

func (t *guildEventTracker) prepareGuildDelete(g *discordgo.GuildDelete) bool {
	if g.Unavailable {
		t.unavailableGuilds[g.ID] = struct{}{}
		logger.Warn("Guild temporarily unavailable", "id", g.ID)
		return false
	}

	// A definitive delete supersedes any earlier temporary-unavailable event.
	delete(t.unavailableGuilds, g.ID)
	logger.Info("Left guild", "id", g.ID)
	metrics.SetConnectedGuilds(countAllGuilds())

	// Clear welcome-sent flag so re-invitation triggers a new welcome message
	commands.ClearGuildWelcomeSent(g.ID)
	return true
}

func cleanupGuildData(g *discordgo.GuildDelete, notifier *adminnotify.Manager, notify bool) {
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

	if notify {
		notifier.NotifyGuildLeave(g, senryuCount, optOutCount)
	}
}

func (t *guildEventTracker) guildDelete(_ *discordgo.Session, g *discordgo.GuildDelete) {
	if !t.prepareGuildDelete(g) {
		return
	}

	notifier := adminNotifier
	go cleanupGuildData(g, notifier, botReady.Load() && notifier != nil)
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
	case strings.HasPrefix(customID, commands.DeletePagePrefix):
		commands.HandleDeletePage(s, i)
	case customID == commands.ContactCategoryCustomID:
		commands.HandleContactCategorySelect(s, i)
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
			content = stripCodeBlocks(content)
			if !isJapaneseRich(content) {
				return
			}
			h := findHaikuSafe(content, []int{5, 7, 5})
			if len(h) != 0 && !haikuSpansNewline(content, h[0]) {
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
				if _, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
					Content:   replyText,
					Reference: m.Reference(),
					AllowedMentions: &discordgo.MessageAllowedMentions{
						Parse: []discordgo.AllowedMentionType{},
					},
					Flags: discordgo.MessageFlagsSuppressEmbeds,
				}); err != nil {
					logger.Warn("Failed to send senryu reply", "error", err, "channel_id", m.ChannelID)
					// 返信に失敗した場合、保存した川柳を削除して整合性を保つ
					if delErr := service.DeleteSenryu(int(created.ID), m.GuildID); delErr != nil {
						logger.Error("Failed to rollback senryu after reply failure", "error", delErr, "senryu_id", created.ID)
					} else {
						logger.Info("Rolled back senryu after reply failure", "senryu_id", created.ID, "channel_id", m.ChannelID)
					}
					// Bot権限不足エラーの場合、該当チャンネルを自動ミュート
					if isBotPermissionError(err) {
						if muteErr := service.ToMute(m.ChannelID, m.GuildID); muteErr != nil {
							logger.Error("Failed to auto-mute channel after permission error", "error", muteErr, "channel_id", m.ChannelID)
						} else {
							metrics.RecordAutoMute()
							logger.Warn("Auto-muted channel due to missing Bot permissions", "channel_id", m.ChannelID, "server_id", m.GuildID)
						}
					}
				}
			}
		}
	}
}

var medals = []string{"🥇", "🥈", "🥉", "🎖️", "🎖️"}

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

	stats, statsErr := service.GetServerStats(i.GuildID)
	if statsErr != nil {
		logger.Warn("Failed to get server stats", "error", statsErr, "guild_id", i.GuildID)
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
		Fields:    []*discordgo.MessageEmbedField{},
	}
	if statsErr == nil {
		if stats.TotalSenryus == 0 {
			embed.Description = "まだ誰も詠んでいません"
		} else {
			embed.Description = fmt.Sprintf("累計 **%d** 句 / **%d** 人の詠み手", stats.TotalSenryus, stats.UniqueAuthors)
		}
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
		displayName := resolveDisplayName(member)
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
			if _, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Content: fmt.Sprintf("ここで一句\n「%s」\n詠み手: %s",
					strings.Join([]string{
						senryus[0].Kamigo,
						senryus[1].Nakasichi,
						senryus[2].Simogo,
					}, " "), strings.Join(getWriters(senryus, m.GuildID, s), ", ")),
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse: []discordgo.AllowedMentionType{},
				},
				Flags: discordgo.MessageFlagsSuppressEmbeds,
			}); err != nil {
				logger.Warn("Failed to send senryu message", "error", err, "channel_id", m.ChannelID)
			}
		}
		return true
	case "詠むな":
		senryu, err := service.GetLastSenryu(m.GuildID)
		if err != nil {
			if errors.Is(err, service.ErrSenryuNotFound) {
				s.ChannelMessageSendReply(m.ChannelID, "まだ誰も詠んでいません。", m.Reference())
			} else {
				logger.Error("Failed to get last senryu", "error", err)
				s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
			}
		} else {
			var authorName string
			if senryu.AuthorID == m.Author.ID {
				authorName = "お前"
			} else {
				member, err := s.GuildMember(m.GuildID, senryu.AuthorID)
				if err != nil {
					authorName = "<@" + senryu.AuthorID + ">"
				} else {
					authorName = resolveDisplayName(member)
				}
			}
			var reply string
			if senryu.Spoiler != nil && *senryu.Spoiler {
				reply = authorName + "が||「" + senryu.Kamigo + " " + senryu.Nakasichi + " " + senryu.Simogo + "」||って詠んだのが最後やぞ"
			} else {
				reply = authorName + "が「" + senryu.Kamigo + " " + senryu.Nakasichi + " " + senryu.Simogo + "」って詠んだのが最後やぞ"
			}
			if _, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Content:   reply,
				Reference: m.Reference(),
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse: []discordgo.AllowedMentionType{},
				},
				Flags: discordgo.MessageFlagsSuppressEmbeds,
			}); err != nil {
				logger.Warn("Failed to send reply", "error", err, "channel_id", m.ChannelID)
			}
		}
		return true
	}
	return false
}

// resolveDisplayName returns the best display name for a guild member,
// preferring Nick > GlobalName > Username.
func resolveDisplayName(member *discordgo.Member) string {
	if member.Nick != "" {
		return member.Nick
	}
	if member.User.GlobalName != "" {
		return member.User.GlobalName
	}
	return member.User.Username
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

var (
	reFencedCodeBlock = regexp.MustCompile("(?s)```.*?```")
	reInlineCode      = regexp.MustCompile("`[^`]+`")
)

func stripCodeBlocks(s string) string {
	s = reFencedCodeBlock.ReplaceAllString(s, "")
	s = reInlineCode.ReplaceAllString(s, "")
	return s
}

var reSpoiler = regexp.MustCompile(`\|\|.+?\|\|`)

func containsSpoiler(s string) bool {
	return reSpoiler.MatchString(s)
}

func stripSpoilerMarkers(s string) string {
	return strings.ReplaceAll(s, "||", "")
}

func haikuSpansNewline(content, haikuResult string) bool {
	if !strings.Contains(content, "\n") {
		return false
	}
	matched := strings.ReplaceAll(haikuResult, " ", "")
	return !strings.Contains(content, matched)
}

// japaneseCharRatioThreshold is the minimum ratio of Japanese characters
// (Hiragana, Katakana, Han) to total non-space characters required for a
// message to be considered "Japanese-rich" and eligible for senryu detection.
const japaneseCharRatioThreshold = 0.5

func isJapaneseRich(s string) bool {
	var total, jp int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) ||
			r == 'ー' || // Katakana long vowel mark (U+30FC)
			r == '・' { // Katakana middle dot (U+30FB)
			jp++
		}
	}
	if total == 0 {
		return false
	}
	return float64(jp)/float64(total) >= japaneseCharRatioThreshold
}

// isBotPermissionError returns true if the error is a Discord API error
// caused by missing Bot permissions on the channel.
func isBotPermissionError(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Message != nil {
		switch restErr.Message.Code {
		case 50001, // Missing Access
			50013,  // Missing Permissions
			160002: // Cannot reply without permission to read message history
			return true
		}
	}
	return false
}

func getWriters(senryus []model.Senryu, guildID string, session *discordgo.Session) []string {
	var writers []string
	for _, senryu := range senryus {
		member, err := session.GuildMember(guildID, senryu.AuthorID)
		if err != nil {
			continue
		}
		writers = append(writers, resolveDisplayName(member))
	}
	return sliceUnique(writers)
}
