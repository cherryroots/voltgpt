// Package handler contains all handlers for components.
package handler

import (
	"log"
	"strings"

	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

var Components = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
		})

		if len(gamble.Wheel.Rounds) == 0 {
			gamble.Wheel.AddRound()
		}

		embed := gamble.Wheel.StatusEmbed(gamble.Wheel.CurrentRound())

		_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel:    i.ChannelID,
			ID:         i.Message.ID,
			Embeds:     &[]*discordgo.MessageEmbed{&embed},
			Components: &gamble.RoundMessageComponents,
		})
		if err != nil {
			log.Println(err)
		}
	},
	"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		player := gamble.Player{
			User: i.Interaction.Member.User,
		}

		gamble.Wheel.AddPlayer(player)

		message := ""
		hasClaimed := false

		for _, claim := range gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].Claims {
			if claim.ID() == player.ID() {
				message = "You've already claimed!"
				hasClaimed = true
				break
			}
		}

		if !hasClaimed {
			gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].AddClaim(player)
			message = "Claimed 100!"
		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"button_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

		gamble.Wheel.AddPlayer(gamble.Player{User: i.Interaction.Member.User})

		var remove bool
		if strings.Contains(i.MessageComponentData().CustomID, "remove") {
			remove = true
		}
		gamble.Wheel.SendMenu(s, i, remove, false)
	},
	"button_winner": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			discord.DeferEphemeralResponse(s, i)
			_, err := discord.SendFollowup(s, i, "Only admins can pick winners!")
			if err != nil {
				log.Println(err)
			}
			return
		}
		gamble.Wheel.SendMenu(s, i, false, true)
	},
	"menu_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		selectedUser := i.MessageComponentData().Values
		round := gamble.Wheel.CurrentRound().ID

		if strings.Contains(i.MessageComponentData().CustomID, "remove") {
			member, _ := s.GuildMember(i.GuildID, selectedUser[0])
			by := gamble.Player{
				User: i.Interaction.Member.User,
			}
			on := gamble.Player{
				User: member.User,
			}
			gamble.Wheel.Rounds[round].RemoveBet(by, on)
			err := discord.UpdateResponse(s, i, "Removed bet!")
			if err != nil {
				log.Println(err)
			}
			return
		}
		if strings.Contains(i.MessageComponentData().CustomID, "winner") {
			member, _ := s.GuildMember(i.GuildID, selectedUser[0])
			player := gamble.Player{
				User: member.User,
			}

			if len(gamble.Wheel.Rounds[round].Bets) == 0 {
				err := discord.UpdateResponse(s, i, "No bets!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			if !gamble.Wheel.Rounds[round].HasWinner() {
				gamble.Wheel.AddRound()
			}

			gamble.Wheel.Rounds[round].SetWinner(player)
			err := discord.UpdateResponse(s, i, "Set winner!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		gamble.Wheel.SendModal(s, i, selectedUser[0])
	},
}
