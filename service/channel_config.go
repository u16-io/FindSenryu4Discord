package service

import (
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

// defaultEnabledChannelTypes defines the default enabled state for each channel type.
// Unexported to prevent external mutation.
var defaultEnabledChannelTypes = map[discordgo.ChannelType]bool{
	discordgo.ChannelTypeGuildText:          true,
	discordgo.ChannelTypeGuildVoice:         true,
	discordgo.ChannelTypeGuildStageVoice:    true,
	discordgo.ChannelTypeGuildNews:          false,
	discordgo.ChannelTypeGuildForum:         false,
	discordgo.ChannelTypeGuildNewsThread:    true,
	discordgo.ChannelTypeGuildPublicThread:  true,
	discordgo.ChannelTypeGuildPrivateThread: true,
}

// ConfigurableChannelTypes returns the list of channel types that can be configured,
// along with their default enabled state (as a copy to prevent mutation).
func ConfigurableChannelTypes() map[discordgo.ChannelType]bool {
	result := make(map[discordgo.ChannelType]bool, len(defaultEnabledChannelTypes))
	for k, v := range defaultEnabledChannelTypes {
		result[k] = v
	}
	return result
}

// channelConfigCache caches per-guild channel type settings in memory.
// Key: guildID, Value: map[channelType]enabled (overrides only).
var channelConfigCache sync.Map

// IsChannelTypeEnabled checks if a channel type is enabled for a guild.
// Uses an in-memory cache to avoid DB queries on every message.
func IsChannelTypeEnabled(guildID string, channelType discordgo.ChannelType) bool {
	defaultVal, known := defaultEnabledChannelTypes[channelType]
	if !known {
		return false
	}

	overrides := getGuildOverrides(guildID)
	if overrides == nil {
		return defaultVal
	}

	if enabled, ok := overrides[int(channelType)]; ok {
		return enabled
	}
	return defaultVal
}

// getGuildOverrides returns cached overrides for a guild, loading from DB on cache miss.
func getGuildOverrides(guildID string) map[int]bool {
	if cached, ok := channelConfigCache.Load(guildID); ok {
		return cached.(map[int]bool)
	}

	// Cache miss — load from DB
	var settings []model.GuildChannelTypeSetting
	if err := db.DB.Where("guild_id = ?", guildID).Find(&settings).Error; err != nil {
		logger.Error("Failed to load guild channel settings into cache",
			"error", err,
			"guild_id", guildID,
		)
		return nil
	}

	overrides := make(map[int]bool, len(settings))
	for _, s := range settings {
		overrides[s.ChannelType] = s.Enabled
	}
	channelConfigCache.Store(guildID, overrides)
	return overrides
}

// invalidateGuildCache removes the cached settings for a guild,
// forcing the next read to reload from DB.
func invalidateGuildCache(guildID string) {
	channelConfigCache.Delete(guildID)
}

// SetChannelTypeEnabled sets the enabled state for a channel type in a guild.
// If the value matches the default, the row is deleted to keep the table minimal.
func SetChannelTypeEnabled(guildID string, channelType discordgo.ChannelType, enabled bool) error {
	metrics.RecordDatabaseOperation("set_channel_type_enabled")

	defaultVal := defaultEnabledChannelTypes[channelType]

	if enabled == defaultVal {
		// Remove override — revert to default
		if err := db.DB.Where("guild_id = ? AND channel_type = ?", guildID, int(channelType)).
			Delete(&model.GuildChannelTypeSetting{}).Error; err != nil {
			metrics.RecordError("database")
			logger.Error("Failed to delete channel type setting",
				"error", err,
				"guild_id", guildID,
				"channel_type", int(channelType),
			)
			return errors.Wrap(err, "failed to delete channel type setting")
		}
		invalidateGuildCache(guildID)
		return nil
	}

	// Upsert: Assign sets the value on both create and update
	setting := model.GuildChannelTypeSetting{
		GuildID:     guildID,
		ChannelType: int(channelType),
		Enabled:     enabled,
	}
	if err := db.DB.Where("guild_id = ? AND channel_type = ?", guildID, int(channelType)).
		Assign(model.GuildChannelTypeSetting{Enabled: enabled}).
		FirstOrCreate(&setting).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to upsert channel type setting",
			"error", err,
			"guild_id", guildID,
			"channel_type", int(channelType),
		)
		return errors.Wrap(err, "failed to upsert channel type setting")
	}

	invalidateGuildCache(guildID)

	logger.Info("Channel type setting updated",
		"guild_id", guildID,
		"channel_type", int(channelType),
		"enabled", enabled,
	)
	return nil
}

// ToggleChannelTypeEnabled atomically toggles the enabled state for a channel type in a guild.
// Returns the new enabled state after toggling.
func ToggleChannelTypeEnabled(guildID string, channelType discordgo.ChannelType) (bool, error) {
	current := IsChannelTypeEnabled(guildID, channelType)
	newValue := !current
	if err := SetChannelTypeEnabled(guildID, channelType, newValue); err != nil {
		return false, err
	}
	return newValue, nil
}

// GetGuildChannelSettings returns the effective settings for all configurable channel types in a guild.
func GetGuildChannelSettings(guildID string) (map[int]bool, error) {
	metrics.RecordDatabaseOperation("get_guild_channel_settings")

	// Start with defaults
	result := make(map[int]bool, len(defaultEnabledChannelTypes))
	for ct, enabled := range defaultEnabledChannelTypes {
		result[int(ct)] = enabled
	}

	// Apply guild overrides from cache
	overrides := getGuildOverrides(guildID)
	for ct, enabled := range overrides {
		result[ct] = enabled
	}

	return result, nil
}

// DeleteChannelConfigByGuild deletes all channel type settings for a guild.
func DeleteChannelConfigByGuild(guildID string) (int64, error) {
	metrics.RecordDatabaseOperation("delete_channel_config_by_guild")

	result := db.DB.Where("guild_id = ?", guildID).Delete(&model.GuildChannelTypeSetting{})
	if result.Error != nil {
		metrics.RecordError("database")
		logger.Error("Failed to delete channel config by guild",
			"error", result.Error,
			"guild_id", guildID,
		)
		return 0, errors.Wrap(result.Error, "failed to delete channel config by guild")
	}

	invalidateGuildCache(guildID)

	logger.Info("Channel config deleted by guild",
		"guild_id", guildID,
		"count", result.RowsAffected,
	)
	return result.RowsAffected, nil
}
