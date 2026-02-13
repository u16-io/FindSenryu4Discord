package service

import (
	"math/rand"

	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

var (
	ErrSenryuNotFound = errors.New("senryu not found")
	ErrDatabaseError  = errors.New("database error")
)

// CreateSenryu creates a new senryu record
func CreateSenryu(s model.Senryu) (model.Senryu, error) {
	metrics.RecordDatabaseOperation("create_senryu")

	if err := db.DB.Create(&s).Error; err != nil {
		metrics.RecordError("database")
		logger.Error("Failed to create senryu",
			"error", err,
			"server_id", s.ServerID,
			"author_id", s.AuthorID,
		)
		return s, errors.Wrap(err, "failed to create senryu")
	}

	metrics.RecordSenryuDetected(s.ServerID)
	logger.Debug("Senryu created",
		"id", s.ID,
		"server_id", s.ServerID,
		"author_id", s.AuthorID,
	)
	return s, nil
}

// GetLastSenryu returns the last senryu in a server
func GetLastSenryu(serverID string, userID string) (string, error) {
	metrics.RecordDatabaseOperation("get_last_senryu")

	s := model.Senryu{}
	if err := db.DB.Where(&model.Senryu{ServerID: serverID}).Last(&s).Error; err != nil {
		metrics.RecordError("database")
		logger.Warn("Failed to get last senryu",
			"error", err,
			"server_id", serverID,
		)
		return "", errors.Wrap(err, "failed to get last senryu")
	}

	var str string
	if userID == s.AuthorID {
		str = "お前"
	} else {
		str = "<@" + s.AuthorID + "> "
	}
	str += "が「" + s.Kamigo + " " + s.Nakasichi + " " + s.Simogo + "」って詠んだのが最後やぞ"

	return str, nil
}

// GetThreeRandomSenryus returns three random senryus for generating a new one
func GetThreeRandomSenryus(serverID string) ([]model.Senryu, error) {
	metrics.RecordDatabaseOperation("get_random_senryus")

	var s []model.Senryu
	if err := db.DB.Where(&model.Senryu{ServerID: serverID}).Find(&s).Error; err != nil {
		metrics.RecordError("database")
		logger.Warn("Failed to get senryus",
			"error", err,
			"server_id", serverID,
		)
		return nil, errors.Wrap(err, "failed to get senryus")
	}

	if len(s) == 0 {
		return nil, nil
	}

	n := len(s)
	return []model.Senryu{
		s[rand.Intn(n)],
		s[rand.Intn(n)],
		s[rand.Intn(n)],
	}, nil
}

// RankResult represents a ranking entry
type RankResult struct {
	Count    int
	AuthorId string
	Rank     int
}

// GetRanking returns the senryu ranking for a server
func GetRanking(serverID string) ([]RankResult, error) {
	metrics.RecordDatabaseOperation("get_ranking")

	var ranks []RankResult
	if err := db.DB.Model(&model.Senryu{}).
		Where(&model.Senryu{ServerID: serverID}).
		Group("author_id").
		Select("COUNT(TRUE) AS count, author_id").
		Order("count DESC").
		Find(&ranks).Error; err != nil {
		metrics.RecordError("database")
		logger.Warn("Failed to get ranking",
			"error", err,
			"server_id", serverID,
		)
		return nil, errors.Wrap(err, "failed to get ranking")
	}

	var results []RankResult
	var before RankResult
	for i, rank := range ranks {
		if rank.Count == before.Count {
			rank.Rank = before.Rank
		} else {
			rank.Rank = i + 1
		}
		if rank.Rank > 5 {
			break
		}
		results = append(results, rank)
		before = rank
	}

	return results, nil
}

// GetServerStats returns statistics for a server
type ServerStats struct {
	TotalSenryus  int64
	UniqueAuthors int64
}

// GetServerStats returns statistics for a server
func GetServerStats(serverID string) (ServerStats, error) {
	metrics.RecordDatabaseOperation("get_server_stats")

	var stats ServerStats

	if err := db.DB.Model(&model.Senryu{}).Where(&model.Senryu{ServerID: serverID}).Count(&stats.TotalSenryus).Error; err != nil {
		return stats, errors.Wrap(err, "failed to count senryus")
	}

	var count int64
	if err := db.DB.Model(&model.Senryu{}).Where(&model.Senryu{ServerID: serverID}).Select("COUNT(DISTINCT author_id)").Count(&count).Error; err != nil {
		return stats, errors.Wrap(err, "failed to count unique authors")
	}
	stats.UniqueAuthors = count

	return stats, nil
}
