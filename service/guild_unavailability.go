package service

import (
	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

// ListUnavailableGuildIDs returns guilds that were unavailable when the bot
// last stopped. These IDs suppress recovery events from being treated as joins.
func ListUnavailableGuildIDs() ([]string, error) {
	metrics.RecordDatabaseOperation("list_unavailable_guilds")

	var guilds []model.UnavailableGuild
	if err := db.DB.Find(&guilds).Error; err != nil {
		metrics.RecordError("database")
		return nil, errors.Wrap(err, "failed to list unavailable guilds")
	}

	guildIDs := make([]string, 0, len(guilds))
	for _, guild := range guilds {
		guildIDs = append(guildIDs, guild.GuildID)
	}
	return guildIDs, nil
}

// MarkGuildUnavailable persists a temporary outage marker for a guild.
func MarkGuildUnavailable(guildID string) error {
	metrics.RecordDatabaseOperation("mark_guild_unavailable")

	guild := model.UnavailableGuild{GuildID: guildID}
	if err := db.DB.FirstOrCreate(&guild).Error; err != nil {
		metrics.RecordError("database")
		return errors.Wrap(err, "failed to mark guild unavailable")
	}
	return nil
}

// ClearGuildUnavailable removes a consumed or superseded outage marker.
func ClearGuildUnavailable(guildID string) error {
	metrics.RecordDatabaseOperation("clear_guild_unavailable")

	if err := db.DB.Where("guild_id = ?", guildID).Delete(&model.UnavailableGuild{}).Error; err != nil {
		metrics.RecordError("database")
		return errors.Wrap(err, "failed to clear unavailable guild")
	}
	return nil
}
