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

func TestIsSenryuTargetChannel_検出対象のチャンネルタイプでtrueを返す(t *testing.T) {
	targetTypes := []struct {
		name        string
		channelType discordgo.ChannelType
	}{
		{"テキストチャンネル", discordgo.ChannelTypeGuildText},
		{"ボイスチャンネル", discordgo.ChannelTypeGuildVoice},
		{"ステージチャンネル", discordgo.ChannelTypeGuildStageVoice},
		{"ニューススレッド", discordgo.ChannelTypeGuildNewsThread},
		{"公開スレッド", discordgo.ChannelTypeGuildPublicThread},
		{"プライベートスレッド", discordgo.ChannelTypeGuildPrivateThread},
	}

	for _, tt := range targetTypes {
		t.Run(tt.name, func(t *testing.T) {
			if !isSenryuTargetChannel(tt.channelType) {
				t.Errorf("%s (type=%d) は検出対象であるべき", tt.name, tt.channelType)
			}
		})
	}
}

func TestIsSenryuTargetChannel_検出対象外のチャンネルタイプでfalseを返す(t *testing.T) {
	nonTargetTypes := []struct {
		name        string
		channelType discordgo.ChannelType
	}{
		{"DM", discordgo.ChannelTypeDM},
		{"グループDM", discordgo.ChannelTypeGroupDM},
		{"カテゴリ", discordgo.ChannelTypeGuildCategory},
		{"アナウンスチャンネル", discordgo.ChannelTypeGuildNews},
		{"ストアチャンネル", discordgo.ChannelTypeGuildStore},
		{"ディレクトリ", discordgo.ChannelTypeGuildDirectory},
		{"フォーラムチャンネル", discordgo.ChannelTypeGuildForum},
		{"メディアチャンネル", discordgo.ChannelTypeGuildMedia},
	}

	for _, tt := range nonTargetTypes {
		t.Run(tt.name, func(t *testing.T) {
			if isSenryuTargetChannel(tt.channelType) {
				t.Errorf("%s (type=%d) は検出対象外であるべき", tt.name, tt.channelType)
			}
		})
	}
}

func TestIsSenryuTargetChannel_未知のチャンネルタイプでfalseを返す(t *testing.T) {
	unknownType := discordgo.ChannelType(999)
	if isSenryuTargetChannel(unknownType) {
		t.Error("未知のチャンネルタイプは検出対象外であるべき")
	}
}

func TestIsParentChannelMuted_ParentIDが空の場合はfalseを返す(t *testing.T) {
	ch := &discordgo.Channel{ParentID: ""}
	if isParentChannelMuted(ch) {
		t.Error("ParentIDが空のチャンネルはミュート判定されるべきではない")
	}
}

func TestIsParentChannelMuted_親チャンネルがミュートされていない場合はfalseを返す(t *testing.T) {
	setupTestDB(t)

	ch := &discordgo.Channel{ParentID: "not-muted-parent"}
	if isParentChannelMuted(ch) {
		t.Error("ミュートされていない親チャンネルに対してtrueが返された")
	}
}

func TestIsParentChannelMuted_親チャンネルがミュートされている場合はtrueを返す(t *testing.T) {
	setupTestDB(t)

	parentID := "muted-parent"
	if err := service.ToMute(parentID); err != nil {
		t.Fatalf("ミュート設定に失敗: %v", err)
	}

	ch := &discordgo.Channel{ParentID: parentID}
	if !isParentChannelMuted(ch) {
		t.Error("ミュートされた親チャンネルに対してfalseが返された")
	}
}

func TestIsParentChannelMuted_親チャンネルのミュート解除後はfalseを返す(t *testing.T) {
	setupTestDB(t)

	parentID := "mute-then-unmute-parent"
	if err := service.ToMute(parentID); err != nil {
		t.Fatalf("ミュート設定に失敗: %v", err)
	}
	if err := service.ToUnMute(parentID); err != nil {
		t.Fatalf("ミュート解除に失敗: %v", err)
	}

	ch := &discordgo.Channel{ParentID: parentID}
	if isParentChannelMuted(ch) {
		t.Error("ミュート解除済みの親チャンネルに対してtrueが返された")
	}
}
