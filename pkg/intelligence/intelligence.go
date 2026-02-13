package intelligence

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/service"
)

// Manager handles admin intelligence notifications (guild join, daily summary).
type Manager struct {
	session        *discordgo.Session
	logChannelID   string
	prevGuildCount int
	stopCh         chan struct{}
	stoppedCh      chan struct{}
}

// NewManager creates a new intelligence manager.
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
		logger.Info("Intelligence manager disabled (log_channel_id is empty)")
		return
	}

	logger.Info("Starting intelligence manager", "log_channel_id", m.logChannelID)
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
		logger.Info("Intelligence manager stopped")
	case <-ctx.Done():
		logger.Warn("Intelligence manager stop timeout")
	}
}

// NotifyGuildJoin sends a guild join notification to the log channel.
func (m *Manager) NotifyGuildJoin(guild *discordgo.Guild) {
	if m.logChannelID == "" {
		return
	}

	memberCount := guild.MemberCount
	msg := fmt.Sprintf("**[サーバー参加]** `%s` (ID: `%s`) — メンバー数: %d", guild.Name, guild.ID, memberCount)

	if _, err := m.session.ChannelMessageSend(m.logChannelID, msg); err != nil {
		logger.Error("Failed to send guild join notification",
			"error", err,
			"guild_id", guild.ID,
			"guild_name", guild.Name,
		)
	}
}

func (m *Manager) run() {
	defer close(m.stoppedCh)

	for {
		d := durationUntilNextMidnightJST()
		logger.Debug("Intelligence: next daily summary in", "duration", d)

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
		countStr = "取得失敗"
	} else {
		countStr = fmt.Sprintf("%d 句", count)
	}

	msg := fmt.Sprintf("**[日次サマリー]** %s\n- 前日の川柳数: %s\n- 接続サーバー数: %d (%s)",
		from.Format("2006/01/02"),
		countStr,
		currentGuilds,
		diffStr,
	)

	if _, err := m.session.ChannelMessageSend(m.logChannelID, msg); err != nil {
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
