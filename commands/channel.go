package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/service"
)

// ChannelTogglePrefix is the custom ID prefix for channel type toggle buttons.
const ChannelTogglePrefix = "channel_toggle:"

// channelTypeInfo holds display information for each configurable channel type.
type channelTypeInfo struct {
	Type     discordgo.ChannelType
	Label    string // short label for button
	FullName string // full name for embed
}

// channelTypeOrder defines the display and button order.
var channelTypeOrder = []channelTypeInfo{
	{discordgo.ChannelTypeGuildText, "テキスト", "テキストチャンネル"},
	{discordgo.ChannelTypeGuildVoice, "ボイス", "ボイスチャンネル"},
	{discordgo.ChannelTypeGuildStageVoice, "ステージ", "ステージチャンネル"},
	{discordgo.ChannelTypeGuildNews, "アナウンス", "アナウンスチャンネル"},
	{discordgo.ChannelTypeGuildForum, "フォーラム", "フォーラムチャンネル"},
	{discordgo.ChannelTypeGuildNewsThread, "ニュース", "ニューススレッド"},
	{discordgo.ChannelTypeGuildPublicThread, "公開", "公開スレッド"},
	{discordgo.ChannelTypeGuildPrivateThread, "プライベート", "プライベートスレッド"},
}

// validToggleChannelTypes is a set of channel types that can be toggled via buttons.
var validToggleChannelTypes map[int]bool

func init() {
	validToggleChannelTypes = make(map[int]bool, len(channelTypeOrder))
	for _, info := range channelTypeOrder {
		validToggleChannelTypes[int(info.Type)] = true
	}
}

// HandleChannelCommand handles the /channel slash command.
func HandleChannelCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("channel")

	if i.GuildID == "" {
		respondError(s, i, "このコマンドはサーバー内でのみ使用できます")
		return
	}

	if !isServerAdmin(i) {
		respondError(s, i, "このコマンドはサーバー管理者のみ使用できます")
		return
	}

	data, err := buildChannelSettingsResponse(i.GuildID)
	if err != nil {
		logger.Error("Failed to build channel settings response", "error", err, "guild_id", i.GuildID)
		respondError(s, i, "設定の取得に失敗しました")
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	}); err != nil {
		logger.Error("Failed to respond to channel command", "error", err, "guild_id", i.GuildID)
	}
}

// HandleChannelToggle handles button clicks for channel type toggles.
func HandleChannelToggle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		respondError(s, i, "このボタンはサーバー内でのみ使用できます")
		return
	}

	if !isServerAdmin(i) {
		respondError(s, i, "このボタンはサーバー管理者のみ使用できます")
		return
	}

	customID := i.MessageComponentData().CustomID
	channelTypeStr := strings.TrimPrefix(customID, ChannelTogglePrefix)
	channelTypeInt, err := strconv.Atoi(channelTypeStr)
	if err != nil {
		logger.Error("Failed to parse channel type from button", "custom_id", customID)
		respondError(s, i, "ボタンの解析に失敗しました")
		return
	}

	// Validate that the channel type is one we support
	if !validToggleChannelTypes[channelTypeInt] {
		logger.Error("Invalid channel type in toggle button", "channel_type", channelTypeInt)
		respondError(s, i, "不正なチャンネルタイプです")
		return
	}

	channelType := discordgo.ChannelType(channelTypeInt)

	// Atomic toggle
	if _, err := service.ToggleChannelTypeEnabled(i.GuildID, channelType); err != nil {
		logger.Error("Failed to toggle channel type",
			"error", err,
			"guild_id", i.GuildID,
			"channel_type", channelTypeInt,
		)
		respondError(s, i, "設定の更新に失敗しました")
		return
	}

	// Rebuild and update the message
	data, err := buildChannelSettingsResponse(i.GuildID)
	if err != nil {
		logger.Error("Failed to build updated channel settings response", "error", err, "guild_id", i.GuildID)
		respondError(s, i, "設定の取得に失敗しました")
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: data,
	}); err != nil {
		logger.Error("Failed to respond to channel toggle", "error", err, "guild_id", i.GuildID)
	}
}

// buildChannelSettingsResponse creates the embed + buttons response data.
func buildChannelSettingsResponse(guildID string) (*discordgo.InteractionResponseData, error) {
	settings, err := service.GetGuildChannelSettings(guildID)
	if err != nil {
		return nil, err
	}

	// Build embed description
	var desc strings.Builder
	for _, info := range channelTypeOrder {
		enabled := settings[int(info.Type)]
		icon := "❌"
		if enabled {
			icon = "✅"
		}
		fmt.Fprintf(&desc, "%s %s\n", icon, info.FullName)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "チャンネルタイプ別 川柳検出設定",
		Description: desc.String(),
		Color:       0x00bfff,
	}

	// Build buttons in rows (max 5 per row)
	var components []discordgo.MessageComponent
	var currentRow []discordgo.MessageComponent

	for _, info := range channelTypeOrder {
		enabled := settings[int(info.Type)]
		style := discordgo.SecondaryButton
		if enabled {
			style = discordgo.SuccessButton
		}

		currentRow = append(currentRow, discordgo.Button{
			Label:    info.Label,
			Style:    style,
			CustomID: ChannelTogglePrefix + strconv.Itoa(int(info.Type)),
		})

		if len(currentRow) == 5 {
			components = append(components, discordgo.ActionsRow{Components: currentRow})
			currentRow = nil
		}
	}
	if len(currentRow) > 0 {
		components = append(components, discordgo.ActionsRow{Components: currentRow})
	}

	return &discordgo.InteractionResponseData{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
		Flags:      discordgo.MessageFlagsEphemeral,
	}, nil
}
