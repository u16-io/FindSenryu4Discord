package model

// Senryu is struct of senryu.
type Senryu struct {
	ID        int    `gorm:"primaryKey;autoIncrement"`
	ServerID  string `gorm:"column:server_id;index"`
	AuthorID  string `gorm:"column:author_id;index"`
	Kamigo    string `gorm:"column:kamigo"`
	Nakasichi string `gorm:"column:nakasichi"`
	Simogo    string `gorm:"column:simogo"`
}

// MutedChannel is struct of muted channel.
type MutedChannel struct {
	ChannelID string `gorm:"primaryKey"`
}

// DetectionOptOut is struct of per-user detection opt-out.
type DetectionOptOut struct {
	ServerID string `gorm:"primaryKey"`
	UserID   string `gorm:"primaryKey"`
}
