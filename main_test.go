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
	if err := db.DB.AutoMigrate(&model.MutedChannel{}, &model.GuildChannelTypeSetting{}).Error; err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	t.Cleanup(func() {
		db.DB.Close()
	})
}

func TestIsChannelTypeEnabled_デフォルト有効タイプ(t *testing.T) {
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
		if !service.IsChannelTypeEnabled("test-guild", ct) {
			t.Errorf("channel type %d should be enabled by default", ct)
		}
	}
}

func TestIsChannelTypeEnabled_デフォルト無効タイプ(t *testing.T) {
	setupTestDB(t)

	disabledTypes := []discordgo.ChannelType{
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
	}

	for _, ct := range disabledTypes {
		if service.IsChannelTypeEnabled("test-guild", ct) {
			t.Errorf("channel type %d should be disabled by default", ct)
		}
	}
}

func TestIsChannelTypeEnabled_未知のタイプは無効(t *testing.T) {
	setupTestDB(t)

	if service.IsChannelTypeEnabled("test-guild", discordgo.ChannelType(999)) {
		t.Error("unknown channel type should be disabled")
	}
}

func TestContainsDiscordTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"ユーザーメンション", "<@123456789> こんにちは", true},
		{"ニックネーム付きメンション", "<@!123456789> こんにちは", true},
		{"チャンネルメンション", "<#987654321> で話しましょう", true},
		{"ロールメンション", "<@&111222333> に連絡", true},
		{"カスタム絵文字", "すごい <:emoji:123456> ですね", true},
		{"アニメーション絵文字", "楽しい <a:dance:789012> 時間", true},
		{"URL_https", "詳細は https://example.com を参照", true},
		{"URL_http", "リンク http://example.com です", true},
		{"トークンなし", "古池や蛙飛び込む水の音", false},
		{"空文字列", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsDiscordTokens(tt.input)
			if got != tt.want {
				t.Errorf("containsDiscordTokens(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestContainsSpoiler(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"スポイラーあり", "これは||ネタバレ||です", true},
		{"スポイラーなし", "古池や蛙飛び込む水の音", false},
		{"複数スポイラー", "||秘密||と||内緒||の話", true},
		{"パイプ1本", "条件A|条件B", false},
		{"空文字列", "", false},
		{"スポイラー内が空", "||||", false},
		{"スポイラー内にスペース", "||秘密の 内容||です", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsSpoiler(tt.input)
			if got != tt.want {
				t.Errorf("containsSpoiler(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripSpoilerMarkers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"スポイラーあり", "これは||ネタバレ||です", "これはネタバレです"},
		{"スポイラーなし", "普通のテキスト", "普通のテキスト"},
		{"複数スポイラー", "||秘密||と||内緒||の話", "秘密と内緒の話"},
		{"空文字列", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripSpoilerMarkers(tt.input)
			if got != tt.want {
				t.Errorf("stripSpoilerMarkers(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsParentChannelMuted_親チャンネルがミュート(t *testing.T) {
	setupTestDB(t)

	if err := service.ToMute("parent-channel", "test-guild"); err != nil {
		t.Fatalf("failed to mute parent channel: %v", err)
	}

	ch := &discordgo.Channel{ParentID: "parent-channel"}
	if !isParentChannelMuted(ch) {
		t.Error("should detect parent channel as muted")
	}
}

func TestIsParentChannelMuted_親チャンネルがミュートされていない(t *testing.T) {
	setupTestDB(t)

	ch := &discordgo.Channel{ParentID: "unmuted-parent"}
	if isParentChannelMuted(ch) {
		t.Error("should not detect unmuted parent channel as muted")
	}
}

func TestIsParentChannelMuted_親チャンネルなし(t *testing.T) {
	setupTestDB(t)

	ch := &discordgo.Channel{ParentID: ""}
	if isParentChannelMuted(ch) {
		t.Error("channel with no parent should not be considered muted")
	}
}

func TestIsParentChannelMuted_自チャンネルのミュートは親に影響しない(t *testing.T) {
	setupTestDB(t)

	if err := service.ToMute("thread-channel", "test-guild"); err != nil {
		t.Fatalf("failed to mute thread channel: %v", err)
	}

	ch := &discordgo.Channel{
		ID:       "thread-channel",
		ParentID: "other-parent",
	}
	if isParentChannelMuted(ch) {
		t.Error("muting the thread itself should not affect parent mute check")
	}
}
