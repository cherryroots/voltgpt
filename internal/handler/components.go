// Package handler contains all handlers for components.
package handler

import (
	"log"
	"strconv"
	"strings"

	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/reminder"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

var Components = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		gamble.Mu.Lock()
		edit := buildGambleStatusMessageEditLocked(i.ChannelID, i.Message.ID, gambleRoundNumberFromMessage(i.Message))
		gamble.Mu.Unlock()

		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     *edit.Embeds,
				Components: *edit.Components,
			},
		})
		if err != nil {
			log.Println(err)
		}
	},
	"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		gamble.Mu.Lock()
		targetRound := gambleRoundNumberFromMessage(i.Message)
		if !gambleRoundIsCurrentLocked(targetRound) {
			gamble.Mu.Unlock()
			_, err := discord.SendFollowup(s, i, "Only the current round can be claimed from this embed.")
			if err != nil {
				log.Println(err)
			}
			return
		}

		player := gamble.Player{User: i.Interaction.Member.User}
		gamble.GameState.AddPlayer(player)

		if len(gamble.GameState.Rounds) == 0 {
			gamble.Mu.Unlock()
			_, err := discord.SendFollowup(s, i, "No active rounds!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		message := ""
		hasClaimed := false
		currentRoundID := gamble.GameState.CurrentRound().ID

		for _, claim := range gamble.GameState.Rounds[currentRoundID].Claims {
			if claim.ID() == player.ID() {
				message = "You've already claimed!"
				hasClaimed = true
				break
			}
		}

		var edit *discordgo.MessageEdit
		if !hasClaimed {
			gamble.GameState.Rounds[currentRoundID].AddClaim(player)
			message = "Claimed 100!"
			edit = buildGambleStatusMessageEditLocked(i.ChannelID, i.Message.ID, targetRound)
		}
		gamble.Mu.Unlock()

		if err := updateGambleStatusMessage(s, edit); err != nil {
			log.Println(err)
		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"button_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

		gamble.Mu.Lock()
		targetRound := gambleRoundNumberFromMessage(i.Message)
		if !gambleRoundIsCurrentLocked(targetRound) {
			gamble.Mu.Unlock()
			discord.DeferEphemeralResponse(s, i)
			_, err := discord.SendFollowup(s, i, "Only the current round can be changed from this embed.")
			if err != nil {
				log.Println(err)
			}
			return
		}

		var remove bool
		if strings.Contains(i.MessageComponentData().CustomID, "remove") {
			remove = true
		}
		var edit *discordgo.MessageEdit
		if !remove {
			gamble.GameState.AddPlayer(gamble.Player{User: i.Interaction.Member.User})
			edit = buildGambleStatusMessageEditLocked(i.ChannelID, i.Message.ID, targetRound)
		}

		gamble.GameState.SendMenu(s, i, remove, false, targetRound, i.Message.ID)
		gamble.Mu.Unlock()

		if err := updateGambleStatusMessage(s, edit); err != nil {
			log.Println(err)
		}
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

		gamble.Mu.Lock()
		targetRound := gambleRoundNumberFromMessage(i.Message)
		if !gambleRoundIsCurrentLocked(targetRound) {
			gamble.Mu.Unlock()
			discord.DeferEphemeralResponse(s, i)
			_, err := discord.SendFollowup(s, i, "Only the current round can be changed from this embed.")
			if err != nil {
				log.Println(err)
			}
			return
		}

		gamble.GameState.SendMenu(s, i, false, true, targetRound, i.Message.ID)
		gamble.Mu.Unlock()
	},
	"menu_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

		gamble.Mu.Lock()
		action, targetRound, targetMessageID := parseGambleMenuCustomID(i.MessageComponentData().CustomID)
		if action == "" || targetRound <= 0 || targetMessageID == "" {
			gamble.Mu.Unlock()
			err := discord.UpdateResponse(s, i, "Invalid bet menu state.")
			if err != nil {
				log.Println(err)
			}
			return
		}
		if !gambleRoundIsCurrentLocked(targetRound) {
			gamble.Mu.Unlock()
			err := discord.UpdateResponse(s, i, "Only the current round can be changed from this embed.")
			if err != nil {
				log.Println(err)
			}
			return
		}
		if len(gamble.GameState.Rounds) == 0 {
			gamble.Mu.Unlock()
			discord.UpdateResponse(s, i, "No active rounds!")
			return
		}

		selectedUser := i.MessageComponentData().Values
		if len(selectedUser) == 0 {
			gamble.Mu.Unlock()
			err := discord.UpdateResponse(s, i, "No user selected.")
			if err != nil {
				log.Println(err)
			}
			return
		}
		round := targetRound - 1

		if action == "remove" {
			member, err := s.GuildMember(i.GuildID, selectedUser[0])
			if err != nil || member == nil {
				gamble.Mu.Unlock()
				discord.UpdateResponse(s, i, "User is not in the server!")
				return
			}
			by := gamble.Player{
				User: i.Interaction.Member.User,
			}
			on := gamble.Player{
				User: member.User,
			}
			gamble.GameState.Rounds[round].RemoveBet(by, on)
			edit := buildGambleStatusMessageEditLocked(i.ChannelID, targetMessageID, targetRound)
			gamble.Mu.Unlock()
			err = discord.UpdateResponse(s, i, "Removed bet!")
			if err != nil {
				log.Println(err)
			}
			if err := updateGambleStatusMessage(s, edit); err != nil {
				log.Println(err)
			}
			return
		}
		if action == "winner" {
			member, err := s.GuildMember(i.GuildID, selectedUser[0])
			if err != nil || member == nil {
				gamble.Mu.Unlock()
				discord.UpdateResponse(s, i, "User is not in the server!")
				return
			}
			player := gamble.Player{
				User: member.User,
			}

			if len(gamble.GameState.Rounds[round].Bets) == 0 {
				gamble.Mu.Unlock()
				err := discord.UpdateResponse(s, i, "No bets!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			if !gamble.GameState.Rounds[round].HasWinner() {
				gamble.GameState.AddRound()
			}

			gamble.GameState.Rounds[round].SetWinner(player)
			edit := buildGambleStatusMessageEditLocked(i.ChannelID, targetMessageID, targetRound)
			gamble.Mu.Unlock()
			err = discord.UpdateResponse(s, i, "Set winner!")
			if err != nil {
				log.Println(err)
			}
			if err := updateGambleStatusMessage(s, edit); err != nil {
				log.Println(err)
			}
			return
		}

		gamble.GameState.SendModal(s, i, selectedUser[0], targetRound, targetMessageID)
		gamble.Mu.Unlock()
	},
	"reminder": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

		values := i.MessageComponentData().Values
		if len(values) == 0 {
			discord.UpdateResponse(s, i, "No reminder selected.")
			return
		}

		id, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			discord.UpdateResponse(s, i, "Invalid reminder ID.")
			return
		}

		if reminder.Delete(id) {
			discord.UpdateResponse(s, i, "✅ Reminder deleted!")
		} else {
			discord.UpdateResponse(s, i, "Reminder not found (it may have already fired).")
		}
	},
	"memorydigest": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			discord.DeferEphemeralResponse(s, i)
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		if err := updateMemoryDigestPage(s, i, parseMemoryDigestPage(i.MessageComponentData().CustomID)); err != nil {
			log.Println(err)
		}
	},
}
