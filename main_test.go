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
	if err := db.DB.AutoMigrate(&model.MutedChannel{}, &model.GuildChannelTypeSetting{}, &model.UnavailableGuild{}).Error; err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	t.Cleanup(func() {
		db.DB.Close()
	})
}

func TestRegisterGatewayHandlers_Guildイベントを同期ディスパッチする(t *testing.T) {
	session := &discordgo.Session{}

	registerGatewayHandlers(session, nil)

	if !session.SyncEvents {
		t.Error("guild availability handlers require ordered synchronous dispatch")
	}
}

func TestReady_Readyに含まれるGuildだけを初期キャッシュ扱いにする(t *testing.T) {
	tracker := newGuildEventTracker()
	tracker.ready(&discordgo.Session{ShardID: 0}, &discordgo.Ready{
		Guilds: []*discordgo.Guild{{ID: "existing-guild"}},
	})

	if !tracker.consumeInitialGuild("existing-guild") {
		t.Error("a guild listed in Ready should be treated as initial cache")
	}
	if tracker.consumeInitialGuild("existing-guild") {
		t.Error("an initial guild should be consumed only once")
	}
	if tracker.consumeInitialGuild("new-guild") {
		t.Error("a guild not listed in Ready should be treated as a new join")
	}
}

func TestGuildCreate_Bot再起動後もUnavailableDeleteからの復旧扱いにする(t *testing.T) {
	setupTestDB(t)
	const guildID = "temporarily-unavailable-guild"
	beforeRestart := newGuildEventTracker()

	if beforeRestart.prepareGuildDelete(&discordgo.GuildDelete{
		Guild: &discordgo.Guild{ID: guildID, Unavailable: true},
	}) {
		t.Fatal("temporary guild unavailability must not trigger data cleanup")
	}

	persistedGuildIDs, err := service.ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to reload unavailable guilds: %v", err)
	}
	afterRestart := newGuildEventTracker(persistedGuildIDs...)
	if !afterRestart.consumeRecovery(guildID) {
		t.Error("the first available event after restart should be treated as recovery")
	}
	if afterRestart.consumeRecovery(guildID) {
		t.Error("the recovery marker should be consumed only once")
	}
	persistedGuildIDs, err = service.ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to reload unavailable guilds after recovery: %v", err)
	}
	if len(persistedGuildIDs) != 0 {
		t.Errorf("the recovered guild marker should be removed: %v", persistedGuildIDs)
	}
}

func TestGuildCreate_Unavailableイベント自体を復旧判定に使う(t *testing.T) {
	setupTestDB(t)
	const guildID = "unavailable-on-startup"
	tracker := newGuildEventTracker()
	previousBotReady := botReady.Load()
	botReady.Store(true)
	t.Cleanup(func() { botReady.Store(previousBotReady) })

	tracker.guildCreate(nil, &discordgo.GuildCreate{
		Guild: &discordgo.Guild{ID: guildID, Unavailable: true},
	})

	persistedGuildIDs, err := service.ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to reload unavailable guilds: %v", err)
	}
	afterRestart := newGuildEventTracker(persistedGuildIDs...)
	if !afterRestart.consumeRecovery(guildID) {
		t.Error("an unavailable GuildCreate should make the next available event a recovery")
	}
}

func TestGuildDelete_確定した脱退で一時切断状態を消去する(t *testing.T) {
	setupTestDB(t)
	const guildID = "deleted-guild"
	tracker := newGuildEventTracker()
	if tracker.prepareGuildDelete(&discordgo.GuildDelete{
		Guild: &discordgo.Guild{ID: guildID, Unavailable: true},
	}) {
		t.Fatal("temporary guild unavailability must not trigger data cleanup")
	}

	if !tracker.prepareGuildDelete(&discordgo.GuildDelete{
		Guild: &discordgo.Guild{ID: guildID},
	}) {
		t.Fatal("definitive guild deletion should proceed to data cleanup")
	}

	if _, tracked := tracker.unavailableGuilds[guildID]; tracked {
		t.Error("permanently deleted guild should no longer be tracked as unavailable")
	}
	persistedGuildIDs, err := service.ListUnavailableGuildIDs()
	if err != nil {
		t.Fatalf("failed to reload unavailable guilds: %v", err)
	}
	if len(persistedGuildIDs) != 0 {
		t.Errorf("permanently deleted guild should no longer be persisted as unavailable: %v", persistedGuildIDs)
	}
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

func TestStripCodeBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"フェンスドコードブロック除去", "前文```go\nfmt.Println()\n```後文", "前文後文"},
		{"インラインコード除去", "変数`x`を使う", "変数を使う"},
		{"フェンスドとインライン混在", "```code```と`inline`", "と"},
		{"コードブロックなし", "古池や蛙飛び込む水の音", "古池や蛙飛び込む水の音"},
		{"空文字列", "", ""},
		{"複数フェンスドコードブロック", "あ```a```い```b```う", "あいう"},
		{"複数インラインコード", "`a`と`b`と`c`", "とと"},
		{"改行を含むフェンスド", "前\n```\nline1\nline2\n```\n後", "前\n\n後"},
		{"閉じられていないフェンスド", "```未閉じコード", "```未閉じコード"},
		{"閉じられていないインライン", "`未閉じインライン", "`未閉じインライン"},
		{"空のインラインコード", "空``です", "空``です"},
		{"言語指定付きフェンスド", "```python\nprint('hello')\n```", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeBlocks(tt.input)
			if got != tt.want {
				t.Errorf("stripCodeBlocks(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsJapaneseRich(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"全てひらがな", "ふるいけやかわずとびこむ", true},
		{"全てカタカナ", "フルイケヤカワズトビコム", true},
		{"全て漢字", "古池蛙飛込水音", true},
		{"日本語混合", "古池や蛙飛びこむ水の音", true},
		{"全て英語", "hello world this is a test", false},
		{"空文字列", "", false},
		{"スペースのみ", "   ", false},
		{"日本語50%ちょうど", "あa", true},
		{"日本語50%未満", "あab", false},
		{"日本語とスペース混合", "古池や 蛙飛びこむ 水の音", true},
		{"コードっぽい文字列", "fmt.Println(hello)", false},
		{"全角英数字は日本語でない", "ＡＢＣＤ", false},
		{"日本語と記号混合", "古池や！蛙飛びこむ？水の音", true},
		{"カタカナ長音符を含む", "コーヒー", true},
		{"長音符のみ", "ーーーー", true},
		{"中黒を含むカタカナ", "ワールド・カップ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isJapaneseRich(tt.input)
			if got != tt.want {
				t.Errorf("isJapaneseRich(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestHaikuSpansNewline(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		haikuResult string
		want        bool
	}{
		{"改行なし", "古池や蛙飛びこむ水の音", "古池や 蛙飛びこむ 水の音", false},
		{"改行あり結果がまたぐ", "古池や蛙飛びこむ\n水の音", "古池や 蛙飛びこむ 水の音", true},
		{"3行書き", "古池や\n蛙飛びこむ\n水の音", "古池や 蛙飛びこむ 水の音", true},
		{"改行後に完全な俳句", "こんにちは\n古池や蛙飛びこむ水の音", "古池や 蛙飛びこむ 水の音", false},
		{"俳句後に改行", "古池や蛙飛びこむ水の音\nさようなら", "古池や 蛙飛びこむ 水の音", false},
		{"空文字列", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := haikuSpansNewline(tt.content, tt.haikuResult)
			if got != tt.want {
				t.Errorf("haikuSpansNewline(%q, %q) = %v, want %v", tt.content, tt.haikuResult, got, tt.want)
			}
		})
	}
}

func TestDetectionFiltering_統合テスト(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantFilter bool // true = should be filtered out (not detected as senryu)
		reason     string
	}{
		{
			"コードブロック内の日本語",
			"```\n古池や蛙飛びこむ水の音\n```",
			true,
			"コードブロック除去後に日本語が残らない",
		},
		{
			"インラインコード内の日本語",
			"`古池や蛙飛びこむ水の音`",
			true,
			"インラインコード除去後に日本語が残らない",
		},
		{
			"英語のみのテキスト",
			"the quick brown fox jumps over lazy dog",
			true,
			"日本語比率が低い",
		},
		{
			"コードっぽいテキスト",
			"func main() { fmt.Println(hello) }",
			true,
			"日本語比率が低い",
		},
		{
			"コードブロック外の日本語テキスト",
			"```go\nfmt.Println()\n```\n普通の日本語テキスト",
			false,
			"コードブロック除去後に日本語が残る",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := stripCodeBlocks(tt.content)
			filtered := !isJapaneseRich(content)
			if filtered != tt.wantFilter {
				t.Errorf("content=%q -> stripCodeBlocks=%q -> isJapaneseRich=%v, wantFilter=%v (%s)",
					tt.content, content, !filtered, tt.wantFilter, tt.reason)
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
