package adminnotify

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/service"
)

// Manager handles admin notifications (guild join/leave, daily summary).
type Manager struct {
	session        *discordgo.Session
	logChannelID   string
	prevGuildCount int
	stopCh         chan struct{}
	stoppedCh      chan struct{}
}

// NewManager creates a new admin notification manager.
// It captures the current guild count as the baseline for daily diff.
func NewManager(session *discordgo.Session, logChannelID string) *Manager {
	return &Manager{
		session:        session,
		logChannelID:   logChannelID,
		prevGuildCount: len(session.State.Guilds),
		stopCh:         make(chan struct{}),
		stoppedCh:      make(chan struct{}),
	}
}

// Start starts the daily summary scheduler in a goroutine.
// If logChannelID is empty, it does nothing.
func (m *Manager) Start() {
	if m.logChannelID == "" {
		logger.Info("Admin notification manager disabled (log_channel_id is empty)")
		return
	}

	logger.Info("Starting admin notification manager", "log_channel_id", m.logChannelID)
	go m.run()
}

// Stop gracefully stops the scheduler.
func (m *Manager) Stop(ctx context.Context) {
	if m.logChannelID == "" {
		return
	}

	close(m.stopCh)
	select {
	case <-m.stoppedCh:
		logger.Info("Admin notification manager stopped")
	case <-ctx.Done():
		logger.Warn("Admin notification manager stop timeout")
	}
}

// NotifyGuildJoin sends a guild join notification to the log channel.
func (m *Manager) NotifyGuildJoin(guild *discordgo.Guild) {
	if m.logChannelID == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🎉 新たな出会い！",
		Description: "新しいサーバーに招待されました！川柳の輪が広がっていく…！",
		Color:       0x57F287,
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: guild.IconURL(""),
		},
		Fields: []*discordgo.MessageEmbedField{
			{Name: "🏠 サーバー名", Value: guild.Name, Inline: true},
			{Name: "🆔 サーバーID", Value: guild.ID, Inline: true},
			{Name: "👥 メンバー数", Value: fmt.Sprintf("%d 人", guild.MemberCount), Inline: true},
		},
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.logChannelID, embed); err != nil {
		logger.Error("Failed to send guild join notification",
			"error", err,
			"guild_id", guild.ID,
			"guild_name", guild.Name,
		)
	}
}

// NotifyGuildLeave sends a guild leave notification to the log channel.
func (m *Manager) NotifyGuildLeave(guild *discordgo.GuildDelete, deletedSenryus, deletedOptOuts int64) {
	if m.logChannelID == "" {
		return
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "🆔 サーバーID", Value: guild.ID, Inline: true},
	}
	if guild.Name != "" {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "🏠 サーバー名", Value: guild.Name, Inline: true,
		})
	}
	fields = append(fields,
		&discordgo.MessageEmbedField{
			Name: "🗑️ 削除した川柳", Value: fmt.Sprintf("%d 句", deletedSenryus), Inline: true,
		},
		&discordgo.MessageEmbedField{
			Name: "🗑️ 削除したオプトアウト", Value: fmt.Sprintf("%d 件", deletedOptOuts), Inline: true,
		},
	)

	embed := &discordgo.MessageEmbed{
		Title:       "💔 別れの時…",
		Description: "サーバーから追い出されてしまいました…。すべての句は涙とともに消えていく。",
		Color:       0xED4245,
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields:      fields,
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.logChannelID, embed); err != nil {
		logger.Error("Failed to send guild leave notification",
			"error", err,
			"guild_id", guild.ID,
		)
	}
}

func (m *Manager) run() {
	defer close(m.stoppedCh)

	for {
		d := durationUntilNextMidnightJST()
		logger.Debug("Next daily summary in", "duration", d)

		timer := time.NewTimer(d)
		select {
		case <-timer.C:
			m.sendDailySummary()
		case <-m.stopCh:
			timer.Stop()
			return
		}
	}
}

func (m *Manager) sendDailySummary() {
	jst := loadJST()
	now := time.Now().In(jst)
	yesterday := now.AddDate(0, 0, -1)

	from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, jst)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, jst)

	count, err := service.CountSenryuByDateRange(from, to)
	if err != nil {
		logger.Error("Failed to count senryus for daily summary", "error", err)
		count = -1
	}

	currentGuilds := len(m.session.State.Guilds)
	guildDiff := currentGuilds - m.prevGuildCount
	m.prevGuildCount = currentGuilds

	diffStr := fmt.Sprintf("%d", guildDiff)
	if guildDiff > 0 {
		diffStr = "+" + diffStr
	}

	var countStr string
	if count < 0 {
		countStr = "💀 取得失敗"
	} else {
		countStr = fmt.Sprintf("%d 句", count)
	}

	var guildEmoji string
	switch {
	case guildDiff > 0:
		guildEmoji = "📈"
	case guildDiff < 0:
		guildEmoji = "📉"
	default:
		guildEmoji = "➡️"
	}

	embed := &discordgo.MessageEmbed{
		Title:       "📊 デイリーレポート",
		Description: fmt.Sprintf("**%s** の一日をお届けします！", from.Format("2006/01/02")),
		Color:       0x5865F2,
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "✍️ 前日の川柳数", Value: countStr, Inline: true},
			{Name: fmt.Sprintf("%s 接続サーバー数", guildEmoji), Value: fmt.Sprintf("%d (%s)", currentGuilds, diffStr), Inline: true},
		},
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.logChannelID, embed); err != nil {
		logger.Error("Failed to send daily summary", "error", err)
	}
}

// loadJST returns the Asia/Tokyo location, falling back to a fixed UTC+9 zone.
func loadJST() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return time.FixedZone("JST", 9*60*60)
	}
	return loc
}

// durationUntilNextMidnightJST returns the duration until the next 0:00 JST.
func durationUntilNextMidnightJST() time.Duration {
	jst := loadJST()
	now := time.Now().In(jst)
	nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, jst)
	return nextMidnight.Sub(now)
}
