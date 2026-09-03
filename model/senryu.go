package model

import "time"

// Senryu is struct of senryu.
type Senryu struct {
	ID        int       `gorm:"primaryKey;autoIncrement"`
	ServerID  string    `gorm:"column:server_id;index"`
	AuthorID  string    `gorm:"column:author_id;index"`
	Kamigo    string    `gorm:"column:kamigo"`
	Nakasichi string    `gorm:"column:nakasichi"`
	Simogo    string    `gorm:"column:simogo"`
	Spoiler   *bool     `gorm:"column:spoiler;not null"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// MutedChannel is struct of muted channel.
type MutedChannel struct {
	ChannelID string `gorm:"primaryKey"`
	GuildID   string `gorm:"column:guild_id;index"`
}

// GuildChannelTypeSetting stores per-guild channel type overrides.
// Only rows that differ from the default are stored.
type GuildChannelTypeSetting struct {
	GuildID     string `gorm:"primaryKey;column:guild_id"`
	ChannelType int    `gorm:"primaryKey;column:channel_type"`
	Enabled     bool   `gorm:"column:enabled"`
}

// DetectionOptOut is struct of per-user detection opt-out.
type DetectionOptOut struct {
	ServerID string `gorm:"primaryKey"`
	UserID   string `gorm:"primaryKey"`
	SetBy    string `gorm:"column:set_by;not null;default:'self'"`
}

// Metadata is a key-value store for bot-wide settings.
type Metadata struct {
	Key   string `gorm:"primaryKey;column:key"`
	Value string `gorm:"column:value;not null"`
}

// UnavailableGuild records a guild hidden by a temporary Discord outage.
type UnavailableGuild struct {
	GuildID string `gorm:"primaryKey;column:guild_id"`
}
