package service

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	db.DB, err = gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.DB.AutoMigrate(&model.MutedChannel{}, &model.GuildChannelTypeSetting{})
	// Clear in-memory cache for test isolation
	channelConfigCache.Range(func(key, _ interface{}) bool {
		channelConfigCache.Delete(key)
		return true
	})
	t.Cleanup(func() {
		db.DB.Close()
	})
}

func TestIsChannelTypeEnabled_デフォルト有効タイプは有効(t *testing.T) {
	setupTestDB(t)

	enabledTypes := []discordgo.ChannelType{
		discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildVoice,
		discordgo.ChannelTypeGuildStageVoice,
		discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread,
	}

	for _, ct := range enabledTypes {
		if !IsChannelTypeEnabled("guild1", ct) {
			t.Errorf("channel type %d should be enabled by default", ct)
		}
	}
}

func TestIsChannelTypeEnabled_デフォルト無効タイプは無効(t *testing.T) {
	setupTestDB(t)

	disabledTypes := []discordgo.ChannelType{
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
	}

	for _, ct := range disabledTypes {
		if IsChannelTypeEnabled("guild1", ct) {
			t.Errorf("channel type %d should be disabled by default", ct)
		}
	}
}

func TestIsChannelTypeEnabled_未知のタイプは無効(t *testing.T) {
	setupTestDB(t)

	if IsChannelTypeEnabled("guild1", discordgo.ChannelType(999)) {
		t.Error("unknown channel type should be disabled")
	}
}

func TestSetChannelTypeEnabled_タイプの無効化(t *testing.T) {
	setupTestDB(t)

	// GuildText is enabled by default; disable it
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	if IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText) {
		t.Error("GuildText should be disabled after setting to false")
	}
}

func TestSetChannelTypeEnabled_タイプの有効化(t *testing.T) {
	setupTestDB(t)

	// GuildNews is disabled by default; enable it
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildNews, true); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	if !IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildNews) {
		t.Error("GuildNews should be enabled after setting to true")
	}
}

func TestSetChannelTypeEnabled_デフォルトと同じ設定は行削除(t *testing.T) {
	setupTestDB(t)

	// Disable GuildText (differs from default)
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	// Verify row exists
	var count int
	if err := db.DB.Model(&model.GuildChannelTypeSetting{}).Where("guild_id = ?", "guild1").Count(&count).Error; err != nil {
		t.Fatalf("failed to count settings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}

	// Set back to default (true) — should delete the row
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, true); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	if err := db.DB.Model(&model.GuildChannelTypeSetting{}).Where("guild_id = ?", "guild1").Count(&count).Error; err != nil {
		t.Fatalf("failed to count settings: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after reverting to default, got %d", count)
	}
}

func TestSetChannelTypeEnabled_冪等性(t *testing.T) {
	setupTestDB(t)

	// Set the same value twice
	for i := 0; i < 2; i++ {
		if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildNews, true); err != nil {
			t.Fatalf("iteration %d: failed to set channel type: %v", i, err)
		}
	}

	if !IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildNews) {
		t.Error("GuildNews should still be enabled after idempotent set")
	}
}

func TestIsChannelTypeEnabled_ギルド間独立性(t *testing.T) {
	setupTestDB(t)

	// Disable GuildText for guild1
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	// guild2 should still have default (enabled)
	if !IsChannelTypeEnabled("guild2", discordgo.ChannelTypeGuildText) {
		t.Error("guild2 GuildText should still be enabled (independent of guild1)")
	}

	if IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText) {
		t.Error("guild1 GuildText should be disabled")
	}
}

func TestGetGuildChannelSettings_デフォルト設定を返す(t *testing.T) {
	setupTestDB(t)

	settings, err := GetGuildChannelSettings("guild1")
	if err != nil {
		t.Fatalf("failed to get settings: %v", err)
	}

	for ct, expected := range ConfigurableChannelTypes() {
		got, ok := settings[int(ct)]
		if !ok {
			t.Errorf("channel type %d missing from settings", ct)
			continue
		}
		if got != expected {
			t.Errorf("channel type %d: expected %v, got %v", ct, expected, got)
		}
	}
}

func TestGetGuildChannelSettings_オーバーライドを反映(t *testing.T) {
	setupTestDB(t)

	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	settings, err := GetGuildChannelSettings("guild1")
	if err != nil {
		t.Fatalf("failed to get settings: %v", err)
	}

	if settings[int(discordgo.ChannelTypeGuildText)] != false {
		t.Error("GuildText should be false after override")
	}
	// Others should remain at defaults
	if settings[int(discordgo.ChannelTypeGuildVoice)] != true {
		t.Error("GuildVoice should still be true")
	}
}

func TestDeleteChannelConfigByGuild_一括削除(t *testing.T) {
	setupTestDB(t)

	// Create several overrides
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}
	if err := SetChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildNews, true); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}
	if err := SetChannelTypeEnabled("guild2", discordgo.ChannelTypeGuildText, false); err != nil {
		t.Fatalf("failed to set channel type: %v", err)
	}

	count, err := DeleteChannelConfigByGuild("guild1")
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows deleted, got %d", count)
	}

	// guild1 should revert to defaults
	if !IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText) {
		t.Error("guild1 GuildText should revert to default (enabled)")
	}

	// guild2 should be unaffected
	if IsChannelTypeEnabled("guild2", discordgo.ChannelTypeGuildText) {
		t.Error("guild2 GuildText should still be disabled")
	}
}

func TestToggleChannelTypeEnabled_トグル動作(t *testing.T) {
	setupTestDB(t)

	// GuildText is enabled by default — toggle should disable it
	newVal, err := ToggleChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText)
	if err != nil {
		t.Fatalf("failed to toggle: %v", err)
	}
	if newVal != false {
		t.Error("expected false after toggling default-enabled type")
	}
	if IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText) {
		t.Error("GuildText should be disabled after toggle")
	}

	// Toggle again — should re-enable
	newVal, err = ToggleChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText)
	if err != nil {
		t.Fatalf("failed to toggle: %v", err)
	}
	if newVal != true {
		t.Error("expected true after second toggle")
	}
	if !IsChannelTypeEnabled("guild1", discordgo.ChannelTypeGuildText) {
		t.Error("GuildText should be enabled after second toggle")
	}
}

func TestConfigurableChannelTypes_コピーを返す(t *testing.T) {
	types := ConfigurableChannelTypes()
	original := len(types)

	// Mutate the returned map
	types[discordgo.ChannelType(999)] = true

	// Original should be unaffected
	types2 := ConfigurableChannelTypes()
	if len(types2) != original {
		t.Error("ConfigurableChannelTypes should return a fresh copy each time")
	}
}

func TestDeleteChannelConfigByGuild_存在しないGuildは0件削除(t *testing.T) {
	setupTestDB(t)

	count, err := DeleteChannelConfigByGuild("non-existent-guild")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows deleted for non-existent guild, got %d", count)
	}
}
