package service

import (
	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

var (
	ErrMuteFailed   = errors.New("failed to mute channel")
	ErrUnmuteFailed = errors.New("failed to unmute channel")
)

// IsMute checks if a channel is muted
func IsMute(id string) bool {
	var muted model.MutedChannel
	if err := db.DB.Where(&model.MutedChannel{ChannelID: id}).First(&muted).Error; err != nil {
		return false
	}
	return true
}

// ToMute mutes a channel
func ToMute(id string) error {
	metrics.RecordDatabaseOperation("mute_channel")

	muted := model.MutedChannel{ChannelID: id}
	if err := db.DB.FirstOrCreate(&muted, &model.MutedChannel{ChannelID: id}).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to mute channel",
			"error", err,
			"channel_id", id,
		)
		return errors.Wrap(err, "failed to mute channel")
	}

	logger.Info("Channel muted", "channel_id", id)
	return nil
}

// ToUnMute unmutes a channel
func ToUnMute(id string) error {
	metrics.RecordDatabaseOperation("unmute_channel")

	if err := db.DB.Where(&model.MutedChannel{ChannelID: id}).Delete(&model.MutedChannel{}).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to unmute channel",
			"error", err,
			"channel_id", id,
		)
		return errors.Wrap(err, "failed to unmute channel")
	}

	logger.Info("Channel unmuted", "channel_id", id)
	return nil
}
