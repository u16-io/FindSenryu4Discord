package main

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/jinzhu/gorm"
	_ "github.com/mattn/go-sqlite3"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/service"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	db.DB, err = gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.DB.AutoMigrate(&model.MutedChannel{})
	t.Cleanup(func() {
		db.DB.Close()
	})
}

func TestIsSenryuTargetChannel(t *testing.T) {
	tests := []struct {
		name        string
		channelType discordgo.ChannelType
		want        bool
	}{
		{
			name:        "テキストチャンネルは検出対象",
			channelType: discordgo.ChannelTypeGuildText,
			want:        true,
		},
		{
			name:        "ボイスチャンネルは検出対象",
			channelType: discordgo.ChannelTypeGuildVoice,
			want:        true,
		},
		{
			name:        "公開スレッドは検出対象",
			channelType: discordgo.ChannelTypeGuildPublicThread,
			want:        true,
		},
		{
			name:        "プライベートスレッドは検出対象",
			channelType: discordgo.ChannelTypeGuildPrivateThread,
			want:        true,
		},
		{
			name:        "ニューススレッドは検出対象",
			channelType: discordgo.ChannelTypeGuildNewsThread,
			want:        true,
		},
		{
			name:        "DMは検出対象外",
			channelType: discordgo.ChannelTypeDM,
			want:        false,
		},
		{
			name:        "グループDMは検出対象外",
			channelType: discordgo.ChannelTypeGroupDM,
			want:        false,
		},
		{
			name:        "ステージチャンネルは検出対象外",
			channelType: discordgo.ChannelTypeGuildStageVoice,
			want:        false,
		},
		{
			name:        "カテゴリは検出対象外",
			channelType: discordgo.ChannelTypeGuildCategory,
			want:        false,
		},
		{
			name:        "フォーラムチャンネル自体は検出対象外",
			channelType: discordgo.ChannelTypeGuildForum,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSenryuTargetChannel(tt.channelType)
			if got != tt.want {
				t.Errorf("isSenryuTargetChannel(%d) = %v, want %v", tt.channelType, got, tt.want)
			}
		})
	}
}

func TestIsParentChannelMuted(t *testing.T) {
	setupTestDB(t)

	t.Run("ParentIDが空の場合はfalse", func(t *testing.T) {
		ch := &discordgo.Channel{ParentID: ""}
		if isParentChannelMuted(ch) {
			t.Error("ParentIDが空なのにtrueが返された")
		}
	})

	t.Run("親チャンネルがミュートされていない場合はfalse", func(t *testing.T) {
		ch := &discordgo.Channel{ParentID: "parent-123"}
		if isParentChannelMuted(ch) {
			t.Error("親チャンネルがミュートされていないのにtrueが返された")
		}
	})

	t.Run("親チャンネルがミュートされている場合はtrue", func(t *testing.T) {
		parentID := "muted-parent-456"
		if err := service.ToMute(parentID); err != nil {
			t.Fatalf("failed to mute parent channel: %v", err)
		}

		ch := &discordgo.Channel{ParentID: parentID}
		if !isParentChannelMuted(ch) {
			t.Error("親チャンネルがミュートされているのにfalseが返された")
		}
	})

	t.Run("ミュート解除後はfalse", func(t *testing.T) {
		parentID := "unmute-test-789"
		if err := service.ToMute(parentID); err != nil {
			t.Fatalf("failed to mute: %v", err)
		}
		if err := service.ToUnMute(parentID); err != nil {
			t.Fatalf("failed to unmute: %v", err)
		}

		ch := &discordgo.Channel{ParentID: parentID}
		if isParentChannelMuted(ch) {
			t.Error("ミュート解除されたのにtrueが返された")
		}
	})
}
