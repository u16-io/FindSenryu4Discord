package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/pkg/backup"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/pkg/permissions"
)

var (
	backupManager *backup.Manager
	startTime     time.Time
)

// SetBackupManager sets the backup manager for admin commands
func SetBackupManager(m *backup.Manager) {
	backupManager = m
}

// SetStartTime sets the start time for uptime calculation
func SetStartTime(t time.Time) {
	startTime = t
}

// AdminCommands returns the admin slash commands
func AdminCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "admin",
			Description: "Bot管理者向けコマンド",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "stats",
					Description: "Bot統計情報を表示します",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "guilds",
					Description: "参加サーバー一覧を表示します",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "backup",
					Description: "手動バックアップを作成します",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
	}
}

// HandleAdminCommand handles admin slash commands
func HandleAdminCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if user is an owner
	userID := getUserID(i)

	if !permissions.CheckOwnerPermission(userID, "admin_command") {
		respondError(s, i, "このコマンドはBot管理者のみ使用できます")
		return
	}

	metrics.RecordCommandExecuted("admin")

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		respondError(s, i, "サブコマンドを指定してください")
		return
	}

	switch options[0].Name {
	case "stats":
		handleStatsCommand(s, i)
	case "guilds":
		handleGuildsCommand(s, i)
	case "backup":
		handleBackupCommand(s, i)
	}
}

func handleStatsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	dbStats := db.GetStats()
	conf := config.GetConf()

	uptime := time.Since(startTime).Round(time.Second)

	embed := &discordgo.MessageEmbed{
		Title:     "Bot Statistics",
		Color:     0x00ff00,
		Timestamp: time.Now().Format(time.RFC3339),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Uptime",
				Value:  uptime.String(),
				Inline: true,
			},
			{
				Name:   "Connected Guilds",
				Value:  fmt.Sprintf("%d", len(s.State.Guilds)),
				Inline: true,
			},
			{
				Name:   "Database Driver",
				Value:  conf.Database.Driver,
				Inline: true,
			},
			{
				Name:   "Total Senryus",
				Value:  fmt.Sprintf("%d", dbStats.SenryuCount),
				Inline: true,
			},
			{
				Name:   "Muted Channels",
				Value:  fmt.Sprintf("%d", dbStats.MutedChannelCount),
				Inline: true,
			},
			{
				Name:   "Database Connected",
				Value:  fmt.Sprintf("%v", dbStats.IsConnected),
				Inline: true,
			},
		},
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleGuildsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guilds := s.State.Guilds
	if len(guilds) == 0 {
		respondError(s, i, "参加しているサーバーがありません")
		return
	}

	// Limit to first 25 guilds for embed field limit
	displayCount := len(guilds)
	if displayCount > 25 {
		displayCount = 25
	}

	fields := make([]*discordgo.MessageEmbedField, 0, displayCount)
	for idx, guild := range guilds[:displayCount] {
		memberCount := guild.MemberCount
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%d. %s", idx+1, guild.Name),
			Value:  fmt.Sprintf("ID: %s\nMembers: %d", guild.ID, memberCount),
			Inline: true,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Connected Guilds (%d total)", len(guilds)),
		Color:       0x0099ff,
		Fields:      fields,
		Timestamp:   time.Now().Format(time.RFC3339),
		Description: "",
	}

	if len(guilds) > 25 {
		embed.Description = fmt.Sprintf("Showing first 25 of %d guilds", len(guilds))
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleBackupCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	conf := config.GetConf()

	if conf.Database.Driver != "sqlite3" {
		respondError(s, i, "バックアップはSQLiteのみ対応しています")
		return
	}

	if backupManager == nil {
		respondError(s, i, "バックアップマネージャーが初期化されていません")
		return
	}

	// Defer response for long-running operation
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if err := backupManager.CreateBackup(); err != nil {
		logger.Error("Manual backup failed", "error", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("バックアップの作成に失敗しました: " + err.Error()),
		})
		return
	}

	// Get backup list
	backups, err := backupManager.ListBackups()
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("バックアップは作成されましたが、一覧の取得に失敗しました"),
		})
		return
	}

	description := "最新のバックアップ:\n"
	for idx, b := range backups {
		if idx >= 5 {
			break
		}
		description += fmt.Sprintf("- `%s` (%s)\n", b.Name, b.CreatedAt.Format("2006-01-02 15:04:05"))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Backup Created",
		Description: description,
		Color:       0x00ff00,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

func getUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func isServerAdmin(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	return i.Member.Permissions&discordgo.PermissionAdministrator != 0
}

func respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func strPtr(s string) *string {
	return &s
}
