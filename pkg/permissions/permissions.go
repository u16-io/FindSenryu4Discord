package permissions

import (
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
)

// IsOwner checks if the given user ID is a bot owner
func IsOwner(userID string) bool {
	conf := config.GetConf()
	for _, ownerID := range conf.Admin.OwnerIDs {
		if ownerID == userID {
			return true
		}
	}
	return false
}

// CheckOwnerPermission checks if the user has owner permission and logs the attempt
func CheckOwnerPermission(userID string, action string) bool {
	if !IsOwner(userID) {
		logger.Warn("Unauthorized admin action attempt",
			"user_id", userID,
			"action", action,
		)
		return false
	}
	logger.Info("Admin action authorized",
		"user_id", userID,
		"action", action,
	)
	return true
}

// GetAdminGuildID returns the guild ID where admin commands should be registered
func GetAdminGuildID() string {
	return config.GetConf().Admin.GuildID
}
