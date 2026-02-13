package commands

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

const (
	ContactModalCustomID  = "contact_modal"
	ContactSubjectInputID = "contact_subject"
	ContactMessageInputID = "contact_message"
	ContactReplyPrefix    = "contact_reply:"
	ReplyModalPrefix      = "reply_modal:"
	ReplyMessageInputID   = "reply_message"
	contactCooldown       = 5 * time.Minute
)

var contactCooldowns sync.Map // userID -> time.Time

// HandleContactCommand handles the /contact slash command
func HandleContactCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	metrics.RecordCommandExecuted("contact")

	userID := getUserID(i)

	// クールダウンチェック
	if last, ok := contactCooldowns.Load(userID); ok {
		lastTime := last.(time.Time)
		remaining := contactCooldown - time.Since(lastTime)
		if remaining > 0 {
			minutes := int(remaining.Minutes())
			seconds := int(remaining.Seconds()) % 60
			respondEphemeral(s, i, fmt.Sprintf("お問い合わせのクールダウン中です。あと %d分%d秒 お待ちください", minutes, seconds))
			return
		}
	}

	// モーダル表示
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: ContactModalCustomID,
			Title:    "お問い合わせ",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    ContactSubjectInputID,
							Label:       "件名",
							Style:       discordgo.TextInputShort,
							Placeholder: "件名を入力してください",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    ContactMessageInputID,
							Label:       "内容",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "お問い合わせ内容を入力してください",
							Required:    true,
							MaxLength:   2000,
						},
					},
				},
			},
		},
	})
}

// HandleContactModalSubmit handles the contact modal submission
func HandleContactModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	subject := getModalInputValue(data, ContactSubjectInputID)
	message := getModalInputValue(data, ContactMessageInputID)

	userID := getUserID(i)

	// クールダウン記録
	contactCooldowns.Store(userID, time.Now())

	// ユーザー情報取得
	var userName, avatarURL string
	if i.Member != nil {
		userName = i.Member.User.Username
		avatarURL = i.Member.User.AvatarURL("")
	} else if i.User != nil {
		userName = i.User.Username
		avatarURL = i.User.AvatarURL("")
	}

	// Embed構築
	embed := &discordgo.MessageEmbed{
		Title:       subject,
		Description: message,
		Color:       0x5865F2,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    userName,
			IconURL: avatarURL,
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Contact Form",
		},
	}

	// Fields
	fields := []*discordgo.MessageEmbedField{
		{
			Name:   "ユーザー",
			Value:  fmt.Sprintf("<@%s> (`%s`)", userID, userID),
			Inline: true,
		},
	}

	if i.GuildID != "" {
		guildName := "Unknown"
		guild, err := s.Guild(i.GuildID)
		if err == nil {
			guildName = guild.Name
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "サーバー",
			Value:  fmt.Sprintf("%s (`%s`)", guildName, i.GuildID),
			Inline: true,
		})
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "チャンネル",
			Value:  fmt.Sprintf("<#%s> (`%s`)", i.ChannelID, i.ChannelID),
			Inline: true,
		})
	} else {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "サーバー",
			Value:  "DM",
			Inline: true,
		})
	}

	embed.Fields = fields

	// contact_channel_id に送信
	conf := config.GetConf()
	contactChannelID := conf.Admin.ContactChannelID

	_, err := s.ChannelMessageSendComplex(contactChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "返信",
						Style:    discordgo.PrimaryButton,
						CustomID: ContactReplyPrefix + userID,
					},
				},
			},
		},
	})
	if err != nil {
		logger.Error("Failed to send contact message", "error", err, "channel_id", contactChannelID)
		respondEphemeral(s, i, "お問い合わせの送信に失敗しました")
		return
	}

	respondEphemeral(s, i, "お問い合わせを送信しました ✅")
}

// HandleContactReplyButton handles the reply button click on contact messages
func HandleContactReplyButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	targetUserID := strings.TrimPrefix(i.MessageComponentData().CustomID, ContactReplyPrefix)

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: ReplyModalPrefix + targetUserID,
			Title:    "返信",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    ReplyMessageInputID,
							Label:       "返信内容",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "返信内容を入力してください",
							Required:    true,
							MaxLength:   2000,
						},
					},
				},
			},
		},
	})
}

// HandleContactReplyModalSubmit handles the reply modal submission
func HandleContactReplyModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	targetUserID := strings.TrimPrefix(data.CustomID, ReplyModalPrefix)
	replyMessage := getModalInputValue(data, ReplyMessageInputID)

	// DM チャンネル作成
	dmChannel, err := s.UserChannelCreate(targetUserID)
	if err != nil {
		logger.Error("Failed to create DM channel", "error", err, "user_id", targetUserID)
		respondEphemeral(s, i, "ユーザーへの DM 送信に失敗しました")
		return
	}

	// DM に返信 Embed を送信
	replyEmbed := &discordgo.MessageEmbed{
		Title:       "お問い合わせへの返信",
		Description: replyMessage,
		Color:       0x5865F2,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    s.State.User.Username,
			IconURL: s.State.User.AvatarURL(""),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	_, err = s.ChannelMessageSendEmbed(dmChannel.ID, replyEmbed)
	if err != nil {
		logger.Error("Failed to send DM reply", "error", err, "user_id", targetUserID)
		respondEphemeral(s, i, "ユーザーへの DM 送信に失敗しました")
		return
	}

	// 元のメッセージを更新: 返信済みフィールド追加 + ボタン無効化
	if i.Message != nil {
		var replierName string
		if i.Member != nil {
			replierName = i.Member.User.Username
		} else if i.User != nil {
			replierName = i.User.Username
		}

		embeds := i.Message.Embeds
		if len(embeds) > 0 {
			embeds[0].Fields = append(embeds[0].Fields, &discordgo.MessageEmbedField{
				Name:   "返信済み",
				Value:  fmt.Sprintf("%s が返信しました", replierName),
				Inline: false,
			})
		}

		s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel: i.ChannelID,
			ID:      i.Message.ID,
			Embeds:  &embeds,
			Components: &[]discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "返信済み",
							Style:    discordgo.SecondaryButton,
							CustomID: ContactReplyPrefix + targetUserID,
							Disabled: true,
						},
					},
				},
			},
		})
	}

	respondEphemeral(s, i, "返信を送信しました ✅")
}

// getModalInputValue extracts a text input value from modal submit data
func getModalInputValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, comp := range data.Components {
		row, ok := comp.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, rowComp := range row.Components {
			input, ok := rowComp.(*discordgo.TextInput)
			if !ok {
				continue
			}
			if input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}
