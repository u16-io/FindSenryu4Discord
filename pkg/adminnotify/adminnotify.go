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
	session         *discordgo.Session
	allSessions     []*discordgo.Session
	logChannelID    string // real-time notifications (guild join/leave)
	reportChannelID string // daily report
	prevGuildCount  int
	prevUserCount   int
	stopCh          chan struct{}
	stoppedCh       chan struct{}
}

// NewManager creates a new admin notification manager.
func NewManager(session *discordgo.Session, logChannelID, reportChannelID string) *Manager {
	return &Manager{
		session:         session,
		logChannelID:    logChannelID,
		reportChannelID: reportChannelID,
		stopCh:          make(chan struct{}),
		stoppedCh:       make(chan struct{}),
	}
}

// SetAllSessions sets all shard sessions for cross-shard guild counting.
// prevGuildCount is initialized here once all shards are connected.
func (m *Manager) SetAllSessions(sessions []*discordgo.Session) {
	m.allSessions = sessions
	m.prevGuildCount = m.countAllGuilds()
	m.prevUserCount = m.countAllUsers()
}

func (m *Manager) countAllGuilds() int {
	total := 0
	for _, s := range m.allSessions {
		if s != nil {
			total += len(s.State.Guilds)
		}
	}
	return total
}

func (m *Manager) countAllUsers() int {
	total := 0
	for _, s := range m.allSessions {
		if s != nil {
			for _, g := range s.State.Guilds {
				total += g.MemberCount
			}
		}
	}
	return total
}

// Start starts the daily summary scheduler in a goroutine.
func (m *Manager) Start() {
	if m.reportChannelID == "" {
		logger.Info("Daily report disabled (report_channel_id is empty)")
		return
	}

	logger.Info("Starting admin notification manager", "report_channel_id", m.reportChannelID)
	go m.run()
}

// Stop gracefully stops the scheduler.
func (m *Manager) Stop(ctx context.Context) {
	if m.reportChannelID == "" {
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

// NotifyStarted sends a bot started notification to the report channel.
func (m *Manager) NotifyStarted() {
	if m.reportChannelID == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:     "🟢 Bot 起動完了",
		Color:     0x57F287,
		Timestamp: time.Now().Format(time.RFC3339),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "接続サーバー数", Value: fmt.Sprintf("%d", m.countAllGuilds()), Inline: true},
			{Name: "延べユーザー数", Value: fmt.Sprintf("%d 人", m.countAllUsers()), Inline: true},
		},
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.reportChannelID, embed); err != nil {
		logger.Error("Failed to send started notification", "error", err)
	}
}

// NotifyStopping sends a bot stopping notification to the report channel.
func (m *Manager) NotifyStopping() {
	if m.reportChannelID == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:     "🔴 Bot 停止中…",
		Color:     0xED4245,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.reportChannelID, embed); err != nil {
		logger.Error("Failed to send stopping notification", "error", err)
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

	// Senryu count
	count, err := service.CountSenryuByDateRange(from, to)
	if err != nil {
		logger.Error("Failed to count senryus for daily summary", "error", err)
		count = -1
	}

	// Guild count (API)
	currentGuilds := m.countAllGuilds()
	guildDiff := currentGuilds - m.prevGuildCount
	m.prevGuildCount = currentGuilds

	// User count (API)
	currentUsers := m.countAllUsers()
	userDiff := currentUsers - m.prevUserCount
	m.prevUserCount = currentUsers

	// Active users (DB)
	type activeUsers struct {
		current  int64
		previous int64
		ok       bool
	}
	fetchAU := func(days int) activeUsers {
		cur, err1 := service.CountUniqueAuthorsByDateRange(to.AddDate(0, 0, -days), to)
		prev, err2 := service.CountUniqueAuthorsByDateRange(to.AddDate(0, 0, -days*2), to.AddDate(0, 0, -days))
		if err1 != nil || err2 != nil {
			logger.Error("Failed to count active users", "days", days, "err_current", err1, "err_previous", err2)
			return activeUsers{ok: false}
		}
		return activeUsers{current: cur, previous: prev, ok: true}
	}
	dau := fetchAU(1)
	wau := fetchAU(7)
	mau := fetchAU(30)

	// Build fields
	fields := []*discordgo.MessageEmbedField{
		{Name: "✍️ 前日の川柳数", Value: formatCount(count, "句"), Inline: true},
		{Name: formatDiffEmoji("接続サーバー数", guildDiff), Value: formatDiffValue(currentGuilds, guildDiff), Inline: true},
		{Name: formatDiffEmoji("延べユーザー数", userDiff), Value: formatDiffValue(currentUsers, userDiff), Inline: true},
	}

	if dau.ok {
		diff := int(dau.current - dau.previous)
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: formatDiffEmoji("DAU", diff), Value: formatDiffValue64(dau.current, diff), Inline: true,
		})
	}
	if wau.ok {
		diff := int(wau.current - wau.previous)
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: formatDiffEmoji("WAU", diff), Value: formatDiffValue64(wau.current, diff), Inline: true,
		})
	}
	if mau.ok {
		diff := int(mau.current - mau.previous)
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: formatDiffEmoji("MAU", diff), Value: formatDiffValue64(mau.current, diff), Inline: true,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:       "📊 デイリーレポート",
		Description: fmt.Sprintf("**%s** の一日をお届けします！", from.Format("2006/01/02")),
		Color:       0x5865F2,
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields:      fields,
	}

	if _, err := m.session.ChannelMessageSendEmbed(m.reportChannelID, embed); err != nil {
		logger.Error("Failed to send daily summary", "error", err)
	}
}

func formatCount(count int64, unit string) string {
	if count < 0 {
		return "💀 取得失敗"
	}
	return fmt.Sprintf("%d %s", count, unit)
}

func formatDiffEmoji(label string, diff int) string {
	var emoji string
	switch {
	case diff > 0:
		emoji = "📈"
	case diff < 0:
		emoji = "📉"
	default:
		emoji = "➡️"
	}
	return fmt.Sprintf("%s %s", emoji, label)
}

func formatDiffValue(current, diff int) string {
	diffStr := fmt.Sprintf("%d", diff)
	if diff > 0 {
		diffStr = "+" + diffStr
	}
	return fmt.Sprintf("%d (%s)", current, diffStr)
}

func formatDiffValue64(current int64, diff int) string {
	return formatDiffValue(int(current), diff)
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
