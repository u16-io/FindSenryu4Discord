package service

import (
	"testing"

	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
)

func setupGuildUnavailabilityTestDB(t *testing.T) {
	t.Helper()

	var err error
	db.DB, err = gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.DB.AutoMigrate(&model.UnavailableGuild{}).Error; err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
}

func TestGuildUnavailability(t *testing.T) {
	setupGuildUnavailabilityTestDB(t)

	if err := MarkGuildUnavailable("unavailable-guild"); err != nil {
		t.Fatalf("failed to mark unavailable guild: %v", err)
	}
	if err := MarkGuildUnavailable("unavailable-guild"); err != nil {
		t.Fatalf("marking a guild twice should be idempotent: %v", err)
	}

	guildIDs, err := ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to list unavailable guilds: %v", err)
	}
	if len(guildIDs) != 1 || guildIDs[0] != "unavailable-guild" {
		t.Fatalf("unexpected unavailable guilds: %v", guildIDs)
	}

	if err := ClearGuildUnavailable("unavailable-guild"); err != nil {
		t.Fatalf("failed to clear unavailable guild: %v", err)
	}
	guildIDs, err = ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to list unavailable guilds after clearing: %v", err)
	}
	if len(guildIDs) != 0 {
		t.Fatalf("cleared unavailable guild should not remain: %v", guildIDs)
	}
}
