package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
)

// Manager handles automatic backups
type Manager struct {
	config    config.BackupConfig
	dbPath    string
	stopCh    chan struct{}
	stoppedCh chan struct{}
}

// NewManager creates a new backup manager
func NewManager(cfg config.BackupConfig, dbPath string) *Manager {
	return &Manager{
		config:    cfg,
		dbPath:    dbPath,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// Start starts the automatic backup scheduler
func (m *Manager) Start() {
	if !m.config.Enabled {
		logger.Info("Automatic backup is disabled")
		return
	}

	// Ensure backup directory exists
	if err := os.MkdirAll(m.config.Path, 0755); err != nil {
		logger.Error("Failed to create backup directory", "error", err, "path", m.config.Path)
		return
	}

	logger.Info("Starting backup scheduler",
		"interval_hour", m.config.IntervalHour,
		"path", m.config.Path,
		"max_backups", m.config.MaxBackups,
	)

	go m.run()
}

// Stop stops the backup scheduler
func (m *Manager) Stop(ctx context.Context) {
	if !m.config.Enabled {
		return
	}

	close(m.stopCh)
	select {
	case <-m.stoppedCh:
		logger.Info("Backup scheduler stopped")
	case <-ctx.Done():
		logger.Warn("Backup scheduler stop timeout")
	}
}

func (m *Manager) run() {
	defer close(m.stoppedCh)

	ticker := time.NewTicker(time.Duration(m.config.IntervalHour) * time.Hour)
	defer ticker.Stop()

	// Run initial backup
	if err := m.CreateBackup(); err != nil {
		logger.Error("Initial backup failed", "error", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := m.CreateBackup(); err != nil {
				logger.Error("Scheduled backup failed", "error", err)
			}
		case <-m.stopCh:
			return
		}
	}
}

// CreateBackup creates a backup of the database
func (m *Manager) CreateBackup() error {
	timestamp := time.Now().Format("20060102_150405")
	backupName := "senryu_" + timestamp + ".db"
	backupPath := filepath.Join(m.config.Path, backupName)

	logger.Info("Creating backup", "path", backupPath)

	// Copy the database file
	if err := copyFile(m.dbPath, backupPath); err != nil {
		logger.Error("Failed to create backup", "error", err)
		return err
	}

	logger.Info("Backup created successfully", "path", backupPath)

	// Clean up old backups
	if err := m.cleanupOldBackups(); err != nil {
		logger.Warn("Failed to cleanup old backups", "error", err)
	}

	return nil
}

func (m *Manager) cleanupOldBackups() error {
	files, err := filepath.Glob(filepath.Join(m.config.Path, "senryu_*.db"))
	if err != nil {
		return err
	}

	if len(files) <= m.config.MaxBackups {
		return nil
	}

	// Sort by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		fi, _ := os.Stat(files[i])
		fj, _ := os.Stat(files[j])
		return fi.ModTime().Before(fj.ModTime())
	})

	// Remove oldest backups
	toRemove := len(files) - m.config.MaxBackups
	for i := 0; i < toRemove; i++ {
		logger.Info("Removing old backup", "path", files[i])
		if err := os.Remove(files[i]); err != nil {
			logger.Warn("Failed to remove old backup", "error", err, "path", files[i])
		}
	}

	return nil
}

// ListBackups returns a list of available backups
func (m *Manager) ListBackups() ([]BackupInfo, error) {
	files, err := filepath.Glob(filepath.Join(m.config.Path, "senryu_*.db"))
	if err != nil {
		return nil, err
	}

	var backups []BackupInfo
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{
			Name:      filepath.Base(file),
			Path:      file,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	// Sort by creation time (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})

	return backups, nil
}

// BackupInfo holds information about a backup
type BackupInfo struct {
	Name      string
	Path      string
	Size      int64
	CreatedAt time.Time
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// FormatSize formats a file size in bytes to a human-readable string
func FormatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}

	units := []string{"B", "KB", "MB", "GB"}
	exp := 0
	val := float64(size)
	for val >= unit && exp < len(units)-1 {
		val /= unit
		exp++
	}

	if val < 10 {
		return fmt.Sprintf("%.2g%s", val, units[exp])
	}
	return fmt.Sprintf("%.1f%s", val, units[exp])
}
