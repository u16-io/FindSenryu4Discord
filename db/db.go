package db

import (
	"os"
	"sync"

	"github.com/jinzhu/gorm"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"

	// SQLite3 driver for Gorm
	_ "github.com/mattn/go-sqlite3"
	// PostgreSQL driver for Gorm
	_ "github.com/lib/pq"
)

var (
	DB   *gorm.DB
	once sync.Once
)

// Init initializes the database connection
func Init() error {
	var initErr error
	once.Do(func() {
		initErr = initDB()
	})
	return initErr
}

func initDB() error {
	conf := config.GetConf()

	// Ensure data directory exists for SQLite
	if conf.Database.Driver == "sqlite3" {
		if _, err := os.Stat("data"); os.IsNotExist(err) {
			if err := os.Mkdir("data", 0755); err != nil {
				logger.Error("Failed to create data directory", "error", err)
				return err
			}
		}
	}

	var err error
	switch conf.Database.Driver {
	case "postgres":
		DB, err = gorm.Open("postgres", conf.Database.DSN)
		if err != nil {
			logger.Error("Failed to connect to PostgreSQL", "error", err)
			return err
		}
		logger.Info("Connected to PostgreSQL database")
	default: // sqlite3
		DB, err = gorm.Open("sqlite3", conf.Database.Path)
		if err != nil {
			logger.Error("Failed to connect to SQLite", "error", err)
			return err
		}

		// Enable WAL mode for better concurrency
		if err := DB.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
			logger.Warn("Failed to enable WAL mode", "error", err)
		} else {
			logger.Debug("SQLite WAL mode enabled")
		}

		// Optimize SQLite settings
		DB.Exec("PRAGMA synchronous=NORMAL")
		DB.Exec("PRAGMA cache_size=10000")
		DB.Exec("PRAGMA temp_store=MEMORY")

		logger.Info("Connected to SQLite database", "path", conf.Database.Path)
	}

	// Configure connection pool
	sqlDB := DB.DB()
	if sqlDB != nil {
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(5)
	}

	// Auto migrate
	if err := DB.AutoMigrate(&model.Senryu{}, &model.MutedChannel{}, &model.DetectionOptOut{}).Error; err != nil {
		logger.Error("Failed to migrate database", "error", err)
		return err
	}
	logger.Debug("Database migration completed")

	return nil
}

// Close closes the database connection
func Close() error {
	if DB != nil {
		logger.Info("Closing database connection")
		if err := DB.Close(); err != nil {
			logger.Error("Failed to close database connection", "error", err)
			return err
		}
		logger.Info("Database connection closed")
	}
	return nil
}

// IsConnected returns true if database is connected
func IsConnected() bool {
	if DB == nil {
		return false
	}
	sqlDB := DB.DB()
	if sqlDB == nil {
		return false
	}
	return sqlDB.Ping() == nil
}

// GetDB returns the database instance
func GetDB() *gorm.DB {
	return DB
}

// Stats returns database statistics
type Stats struct {
	SenryuCount       int64
	MutedChannelCount int64
	IsConnected       bool
}

// GetStats returns database statistics
func GetStats() Stats {
	stats := Stats{
		IsConnected: IsConnected(),
	}

	if DB != nil {
		DB.Model(&model.Senryu{}).Count(&stats.SenryuCount)
		DB.Model(&model.MutedChannel{}).Count(&stats.MutedChannelCount)
	}

	return stats
}
