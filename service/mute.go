package service

import (
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
)

// IsMute is true if the channel is muted.
func IsMute(id string) bool {
	var muted model.MutedChannel
	if err := db.DB.Where(&model.MutedChannel{ChannelID: id}).First(&muted).Error; err != nil {
		// Record not found means not muted
		return false
	}
	return true
}

// ToMute is to mute.
func ToMute(id string) error {
	muted := model.MutedChannel{ChannelID: id}
	return db.DB.FirstOrCreate(&muted, &model.MutedChannel{ChannelID: id}).Error
}

// ToUnMute is to unmute.
func ToUnMute(id string) error {
	return db.DB.Where(&model.MutedChannel{ChannelID: id}).Delete(&model.MutedChannel{}).Error
}
