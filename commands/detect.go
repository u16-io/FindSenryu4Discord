package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/service"
)

// HandleDetectCommand handles the /detect slash command
func HandleDetectCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("detect")

	if i.GuildID == "" {
		respondError(s, i, "このコマンドはサーバー内でのみ使用できます")
		return
	}

	userID := getUserID(i)
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		respondError(s, i, "サブコマンドを指定してください")
		return
	}

	subCmd := options[0].Name

	switch subCmd {
	case "on":
		if err := service.OptInDetection(i.GuildID, userID); err != nil {
			logger.Error("Failed to opt in detection", "error", err, "user_id", userID, "guild_id", i.GuildID)
			respondEphemeral(s, i, "川柳検出の有効化に失敗しました")
			return
		}
		respondEphemeral(s, i, "川柳検出を有効にしました ✅")

	case "off":
		if err := service.OptOutDetection(i.GuildID, userID); err != nil {
			logger.Error("Failed to opt out detection", "error", err, "user_id", userID, "guild_id", i.GuildID)
			respondEphemeral(s, i, "川柳検出の無効化に失敗しました")
			return
		}
		respondEphemeral(s, i, "川柳検出を無効にしました ✅")

	case "status":
		if service.IsDetectionOptedOut(i.GuildID, userID) {
			respondEphemeral(s, i, "現在の設定: 川柳検出 **無効**")
		} else {
			respondEphemeral(s, i, "現在の設定: 川柳検出 **有効**")
		}
	}
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
