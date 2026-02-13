package service

import (
	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

var (
	ErrOptOutFailed = errors.New("failed to opt out detection")
	ErrOptInFailed  = errors.New("failed to opt in detection")
)

// IsDetectionOptedOut checks if a user has opted out of detection in a server
func IsDetectionOptedOut(serverID, userID string) bool {
	var optOut model.DetectionOptOut
	if err := db.DB.Where(&model.DetectionOptOut{ServerID: serverID, UserID: userID}).First(&optOut).Error; err != nil {
		return false
	}
	return true
}

// OptOutDetection opts a user out of detection in a server
func OptOutDetection(serverID, userID string) error {
	metrics.RecordDatabaseOperation("opt_out_detection")

	optOut := model.DetectionOptOut{ServerID: serverID, UserID: userID}
	if err := db.DB.FirstOrCreate(&optOut, &model.DetectionOptOut{ServerID: serverID, UserID: userID}).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to opt out detection",
			"error", err,
			"server_id", serverID,
			"user_id", userID,
		)
		return errors.Wrap(err, "failed to opt out detection")
	}

	logger.Info("User opted out of detection", "server_id", serverID, "user_id", userID)
	return nil
}

// DeleteOptOutByServer deletes all detection opt-outs belonging to a server
func DeleteOptOutByServer(serverID string) (int64, error) {
	metrics.RecordDatabaseOperation("delete_opt_out_by_server")

	result := db.DB.Where("server_id = ?", serverID).Delete(&model.DetectionOptOut{})
	if result.Error != nil {
		metrics.RecordError("database")
		logger.Error("Failed to delete opt-outs by server",
			"error", result.Error,
			"server_id", serverID,
		)
		return 0, errors.Wrap(result.Error, "failed to delete opt-outs by server")
	}

	logger.Info("Opt-outs deleted by server",
		"server_id", serverID,
		"count", result.RowsAffected,
	)
	return result.RowsAffected, nil
}

// OptInDetection opts a user back in to detection in a server
func OptInDetection(serverID, userID string) error {
	metrics.RecordDatabaseOperation("opt_in_detection")

	if err := db.DB.Where(&model.DetectionOptOut{ServerID: serverID, UserID: userID}).Delete(&model.DetectionOptOut{}).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to opt in detection",
			"error", err,
			"server_id", serverID,
			"user_id", userID,
		)
		return errors.Wrap(err, "failed to opt in detection")
	}

	logger.Info("User opted in to detection", "server_id", serverID, "user_id", userID)
	return nil
}
