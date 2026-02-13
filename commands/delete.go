package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/cockroachdb/errors"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
	"github.com/u16-io/FindSenryu4Discord/service"
)

const (
	DeleteSelectCustomID = "delete_select"
	DeleteConfirmPrefix  = "delete_confirm:"
	DeleteCancelCustomID = "delete_cancel"
)

// HandleDeleteCommand handles the /delete slash command
func HandleDeleteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("delete")

	if i.GuildID == "" {
		respondError(s, i, "このコマンドはサーバー内でのみ使用できます")
		return
	}

	userID := getUserID(i)
	targetUserID := userID

	// user オプション確認
	cmdOptions := i.ApplicationCommandData().Options
	for _, opt := range cmdOptions {
		if opt.Name == "user" {
			if !isServerAdmin(i) {
				respondError(s, i, "他のユーザーの川柳を削除する権限がありません")
				return
			}
			targetUserID = opt.UserValue(s).ID
		}
	}

	// Deferred response (ephemeral)
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	senryus, err := service.GetRecentSenryusByAuthor(i.GuildID, targetUserID, 10)
	if err != nil {
		logger.Error("Failed to get senryus for delete", "error", err)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("川柳の取得に失敗しました"),
		})
		return
	}

	if len(senryus) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("削除できる川柳がありません"),
		})
		return
	}

	// セレクトメニュー作成
	menuOptions := make([]discordgo.SelectMenuOption, 0, len(senryus))
	for _, sr := range senryus {
		text := fmt.Sprintf("%s %s %s", sr.Kamigo, sr.Nakasichi, sr.Simogo)
		menuOptions = append(menuOptions, discordgo.SelectMenuOption{
			Label: text,
			Value: strconv.Itoa(sr.ID),
		})
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: strPtr("削除する川柳を選んでください:"),
		Components: &[]discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    DeleteSelectCustomID,
						Placeholder: "川柳を選択",
						Options:     menuOptions,
					},
				},
			},
		},
	})
}

// HandleDeleteSelectMenu handles the select menu interaction for delete
func HandleDeleteSelectMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}

	senryuID, err := strconv.Atoi(data.Values[0])
	if err != nil {
		respondComponentUpdate(s, i, "無効な選択です")
		return
	}

	senryu, err := service.GetSenryuByID(senryuID, i.GuildID)
	if err != nil {
		if errors.Is(err, service.ErrSenryuNotFound) {
			respondComponentUpdate(s, i, "川柳が見つかりませんでした")
		} else {
			respondComponentUpdate(s, i, "川柳の取得に失敗しました")
		}
		return
	}

	text := fmt.Sprintf("「%s %s %s」を削除しますか？", senryu.Kamigo, senryu.Nakasichi, senryu.Simogo)

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: text,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "削除する",
							Style:    discordgo.DangerButton,
							CustomID: DeleteConfirmPrefix + data.Values[0],
						},
						discordgo.Button{
							Label:    "キャンセル",
							Style:    discordgo.SecondaryButton,
							CustomID: DeleteCancelCustomID,
						},
					},
				},
			},
		},
	})
}

// HandleDeleteConfirm handles the confirm button for delete
func HandleDeleteConfirm(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	idStr := strings.TrimPrefix(data.CustomID, DeleteConfirmPrefix)

	senryuID, err := strconv.Atoi(idStr)
	if err != nil {
		respondComponentUpdate(s, i, "無効な操作です")
		return
	}

	// 再度権限チェック
	senryu, err := service.GetSenryuByID(senryuID, i.GuildID)
	if err != nil {
		if errors.Is(err, service.ErrSenryuNotFound) {
			respondComponentUpdate(s, i, "川柳が見つかりませんでした（既に削除された可能性があります）")
		} else {
			respondComponentUpdate(s, i, "川柳の取得に失敗しました")
		}
		return
	}

	userID := getUserID(i)
	if senryu.AuthorID != userID && !isServerAdmin(i) {
		respondComponentUpdate(s, i, "この川柳を削除する権限がありません")
		return
	}

	if err := service.DeleteSenryu(senryuID, i.GuildID); err != nil {
		if errors.Is(err, service.ErrSenryuNotFound) {
			respondComponentUpdate(s, i, "川柳が見つかりませんでした（既に削除された可能性があります）")
		} else {
			logger.Error("Failed to delete senryu", "error", err, "id", senryuID)
			respondComponentUpdate(s, i, "川柳の削除に失敗しました")
		}
		return
	}

	text := fmt.Sprintf("「%s %s %s」を削除しました", senryu.Kamigo, senryu.Nakasichi, senryu.Simogo)
	respondComponentUpdate(s, i, text)
}

// HandleDeleteCancel handles the cancel button for delete
func HandleDeleteCancel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	respondComponentUpdate(s, i, "削除をキャンセルしました")
}

func respondComponentUpdate(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    message,
			Components: []discordgo.MessageComponent{},
		},
	})
}
