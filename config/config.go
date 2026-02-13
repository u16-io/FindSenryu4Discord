package config

import (
	"errors"
	"log"
	"strings"
	"sync"

	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

var (
	k    = koanf.New(".")
	conf *Config
	once sync.Once
)

// Config holds all configuration
type Config struct {
	Discord  DiscordConfig  `koanf:"discord"`
	Database DatabaseConfig `koanf:"database"`
	Log      LogConfig      `koanf:"log"`
	Admin    AdminConfig    `koanf:"admin"`
	Server   ServerConfig   `koanf:"server"`
	Backup   BackupConfig   `koanf:"backup"`
}

// DiscordConfig holds Discord-related configuration
type DiscordConfig struct {
	Token   string `koanf:"token"`
	Playing string `koanf:"playing"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Driver string `koanf:"driver"` // sqlite3 or postgres
	Path   string `koanf:"path"`   // SQLite file path
	DSN    string `koanf:"dsn"`    // PostgreSQL DSN
}

// LogConfig holds logging configuration
type LogConfig struct {
	Level  string `koanf:"level"`  // debug, info, warn, error
	Format string `koanf:"format"` // json, text
}

// AdminConfig holds admin-related configuration
type AdminConfig struct {
	OwnerIDs         []string `koanf:"owner_ids"`         // Bot admin Discord IDs
	GuildID          string   `koanf:"guild_id"`          // Guild ID for admin commands
	LogChannelID     string   `koanf:"log_channel_id"`    // Log notification channel (join notifications, daily summary)
	ContactChannelID string   `koanf:"contact_channel_id"` // Contact notification channel (future use)
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port    int  `koanf:"port"`    // HTTP server port for metrics/health
	Enabled bool `koanf:"enabled"` // Enable HTTP server
}

// BackupConfig holds backup configuration
type BackupConfig struct {
	Enabled      bool   `koanf:"enabled"`       // Enable automatic backup
	IntervalHour int    `koanf:"interval_hour"` // Backup interval in hours
	Path         string `koanf:"path"`          // Backup directory path
	MaxBackups   int    `koanf:"max_backups"`   // Maximum number of backups to keep
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	var loadErr error
	once.Do(func() {
		// Load from TOML file
		if err := k.Load(file.Provider(configPath), toml.Parser()); err != nil {
			loadErr = err
			return
		}

		// Load from environment variables (FINDSENRYU_ prefix)
		// Environment variables override file config
		if err := k.Load(env.Provider("FINDSENRYU_", ".", func(s string) string {
			return strings.Replace(
				strings.ToLower(strings.TrimPrefix(s, "FINDSENRYU_")),
				"_", ".", -1)
		}), nil); err != nil {
			loadErr = err
			return
		}

		conf = &Config{}
		if err := k.Unmarshal("", conf); err != nil {
			loadErr = err
			return
		}

		// Set defaults
		setDefaults(conf)

		// Validate required fields
		if err := validate(conf); err != nil {
			loadErr = err
			return
		}
	})

	return conf, loadErr
}

func setDefaults(c *Config) {
	if c.Database.Driver == "" {
		c.Database.Driver = "sqlite3"
	}
	if c.Database.Path == "" {
		c.Database.Path = "data/senryu.db"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 9090
	}
	if c.Backup.IntervalHour == 0 {
		c.Backup.IntervalHour = 24
	}
	if c.Backup.Path == "" {
		c.Backup.Path = "data/backups"
	}
	if c.Backup.MaxBackups == 0 {
		c.Backup.MaxBackups = 7
	}
}

func validate(c *Config) error {
	if c.Discord.Token == "" {
		return errors.New("discord.token is required")
	}
	if c.Admin.GuildID != "" && len(c.Admin.OwnerIDs) == 0 {
		log.Println("WARNING: admin.guild_id is set but admin.owner_ids is empty; admin commands will be registered but unusable")
	}
	return nil
}

// GetConf returns the loaded configuration (legacy compatibility)
func GetConf() *Config {
	if conf == nil {
		var err error
		conf, err = Load("config.toml")
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	}
	return conf
}

// Get returns a value from config by key
func Get(key string) interface{} {
	return k.Get(key)
}

// String returns a string value from config by key
func String(key string) string {
	return k.String(key)
}

// Int returns an int value from config by key
func Int(key string) int {
	return k.Int(key)
}

// Bool returns a bool value from config by key
func Bool(key string) bool {
	return k.Bool(key)
}
