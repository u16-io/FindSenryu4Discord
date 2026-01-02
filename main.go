package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/model"
	"github.com/u16-io/FindSenryu4Discord/service"

	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/0x307e/go-haiku"
	"github.com/bwmarrin/discordgo"
)

func main() {
	var (
		err error
	)

	log.SetFlags(log.Lshortfile)
	conf := config.GetConf()
	dg, err := discordgo.New("Bot " + conf.Discord.Token)
	if err != nil {
		log.Fatal("error creating Discord session")
	}
	dg.AddHandler(messageCreate)
	err = dg.Open()
	if err != nil {
		fmt.Println(err)
		log.Fatal("error opening connection")
	}

	db.Init()

	dg.UpdateGameStatus(1, conf.Discord.Playing)
	fmt.Println("[Servers]")
	for _, guild := range dg.State.Guilds {
		fmt.Println(guild.Name)
	}
	fmt.Println("")

	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	dg.Close()
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	ch, err := s.Channel(m.ChannelID)
	if err != nil {
		fmt.Println(err)
		return
	}

	if ch.Type != discordgo.ChannelTypeGuildText {
		s.ChannelMessageSend(m.ChannelID, "個チャはダメです")
		return
	}

	if handleCommand(m, s) || handleYomeYomuna(m, s) {
		return
	}

	if !service.IsMute(m.ChannelID) {
		if m.Author.ID != s.State.User.ID {
			h := haiku.Find(m.Content, []int{5, 7, 5})
			if len(h) != 0 {
				senryu := strings.Split(h[0], " ")
				service.CreateSenryu(
					model.Senryu{
						ServerID:  m.GuildID,
						AuthorID:  m.Author.ID,
						Kamigo:    senryu[0],
						Nakasichi: senryu[1],
						Simogo:    senryu[2],
					},
				)
				s.ChannelMessageSendReply(
					m.ChannelID,
					fmt.Sprintf("川柳を検出しました！\n「%s」", h[0]),
					m.Reference(),
				)
			}
		}
	}
}

var medals = []string{"🥇", "🥈", "🥉", "🎖️", "🎖️"}

func handleCommand(m *discordgo.MessageCreate, s *discordgo.Session) bool {
	prefix := config.GetPrefix()
	cmd := strings.Replace(m.Content, prefix, "", 1)

	if strings.HasPrefix(cmd, "mute") {
		if err := service.ToMute(m.ChannelID); err != nil {
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		} else {
			s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
		}
		return true
	}

	if strings.HasPrefix(cmd, "unmute") {
		if err := service.ToUnMute(m.ChannelID); err != nil {
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		} else {
			s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
		}
		return true
	}

	if strings.HasPrefix(cmd, "rank") {
		handleRanking(m, s)
		return true
	}

	return false
}

func handleRanking(m *discordgo.MessageCreate, s *discordgo.Session) {
	var (
		ranks  []service.RankResult
		errArr []error
	)
	if ranks, errArr = service.GetRanking(m.GuildID); len(errArr) != 0 {
		fmt.Println(errArr)
	} else {
		embed := discordgo.MessageEmbed{
			Type:      discordgo.EmbedTypeRich,
			Title:     "サーバー内ランキング",
			Timestamp: time.Now().Format(time.RFC3339),
			Footer: &discordgo.MessageEmbedFooter{
				Text:    "This bot was made by 0x307e.",
				IconURL: "https://github.com/0x307e.png",
			},
			Thumbnail: &discordgo.MessageEmbedThumbnail{
				URL: s.State.User.AvatarURL(""),
			},
			Author: &discordgo.MessageEmbedAuthor{
				Name:    m.Author.Username,
				IconURL: m.Author.AvatarURL(""),
			},
			Fields: []*discordgo.MessageEmbedField{},
		}
		for _, rank := range ranks {
			user, err := s.User(rank.AuthorId)
			if err != nil {
				continue
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   fmt.Sprintf("%s 第%d位: %d回", medals[rank.Rank-1], rank.Rank, rank.Count),
				Value:  user.Username,
				Inline: true,
			})
		}
		s.ChannelMessageSendEmbed(m.ChannelID, &embed)
	}
}

func handleYomeYomuna(m *discordgo.MessageCreate, s *discordgo.Session) bool {
	var errArr []error
	switch m.Content {
	case "詠め":
		var senryus []model.Senryu
		if senryus, errArr = service.GetThreeRandomSenryus(m.GuildID); len(errArr) != 0 {
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
			return true
		}
		if len(senryus) == 0 {
			s.ChannelMessageSend(m.ChannelID, "まだ誰も詠んでいません。あなたが先に詠んでください。")
		} else {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("ここで一句\n「%s」\n詠み手: %s",
				strings.Join([]string{
					senryus[0].Kamigo,
					senryus[1].Nakasichi,
					senryus[2].Simogo,
				}, " "), strings.Join(getWriters(senryus, m.GuildID, s), ", ")))
		}
		return true
	case "詠むな":
		var senryu string
		if senryu, errArr = service.GetLastSenryu(m.GuildID, m.Author.ID); len(errArr) != 0 {
			s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		} else {
			s.ChannelMessageSendReply(
				m.ChannelID,
				senryu,
				m.Reference(),
			)
		}
		return true
	}
	return false
}

func sliceUnique(target []string) (unique []string) {
	m := map[string]bool{}
	for _, v := range target {
		if !m[v] {
			m[v] = true
			unique = append(unique, v)
		}
	}
	return unique
}

func getWriters(senryus []model.Senryu, guildID string, session *discordgo.Session) []string {
	var writers []string
	for _, senryu := range senryus {
		member, err := session.GuildMember(guildID, senryu.AuthorID)
		if err != nil {
			continue
		}
		if member.Nick != "" {
			writers = append(writers, member.Nick)
		} else {
			writers = append(writers, member.User.Username)
		}
	}
	return sliceUnique(writers)
}
