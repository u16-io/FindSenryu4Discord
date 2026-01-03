package model

// Senryu is struct of senryu.
type Senryu struct {
	ID        int
	ServerID  string
	AuthorID  string
	Kamigo    string
	Nakasichi string
	Simogo    string
}

// MutedChannel is struct of muted channel.
type MutedChannel struct {
	ChannelID string `gorm:"primaryKey"`
}
