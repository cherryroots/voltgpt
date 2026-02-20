package handler

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"

	"github.com/bwmarrin/discordgo"
)

var Modals = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"modal_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ModalSubmitData().CustomID, i.Interaction.Member.User.Username)

		userID := strings.Split(i.ModalSubmitData().CustomID, "-")[1]
		user, _ := s.GuildMember(i.GuildID, userID)
		if user == nil {
			err := discord.UpdateResponse(s, i, "User is not in the server!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		byPlayer := gamble.Player{
			User: i.Interaction.Member.User,
		}
		onPlayer := gamble.Player{
			User: user.User,
		}

		components := i.ModalSubmitData().Components
		if len(components) == 0 {
			discord.UpdateResponse(s, i, "Invalid modal submission")
			return
		}
		row, ok := components[0].(*discordgo.ActionsRow)
		if !ok || len(row.Components) == 0 {
			discord.UpdateResponse(s, i, "Invalid modal submission")
			return
		}
		textInput, ok := row.Components[0].(*discordgo.TextInput)
		if !ok {
			discord.UpdateResponse(s, i, "Invalid modal submission")
			return
		}
		modalInput := textInput.Value

		amount, err := strconv.Atoi(modalInput)
		if err != nil {
			err := discord.UpdateResponse(s, i, "Invalid amount")
			if err != nil {
				log.Println(err)
			}
			return
		}

		bet := gamble.Bet{
			By:     byPlayer,
			On:     onPlayer,
			Amount: amount,
		}

		gamble.Mu.Lock()
		defer gamble.Mu.Unlock()

		if len(gamble.GameState.Rounds) == 0 {
			discord.UpdateResponse(s, i, "No active rounds!")
			return
		}

		currentRoundID := gamble.GameState.CurrentRound().ID
		existingBet, hasBet := gamble.GameState.Rounds[currentRoundID].HasBet(bet)
		existingAmount := 0
		if hasBet {
			existingAmount = existingBet.Amount
		}

		options := len(gamble.GameState.CurrentWheelOptions())
		PlayerBets, _ := gamble.GameState.PlayerBets(byPlayer, gamble.GameState.CurrentRound())
		if options%2 == 1 {
			options++
		}
		if PlayerBets >= (options/2) && !hasBet {
			err := discord.UpdateResponse(s, i, "You can only bet on half of the players")
			if err != nil {
				log.Println(err)
			}
			return

		}

		if amount > gamble.GameState.PlayerUsableMoney(byPlayer)+existingAmount {
			err := discord.UpdateResponse(s, i, "You don't have that much money")
			if err != nil {
				log.Println(err)
			}
			return
		}

		if amount <= 0 {
			err := discord.UpdateResponse(s, i, "You can't place a bet of 0 or lower")
			if err != nil {
				log.Println(err)
			}
			return
		}

		gamble.GameState.Rounds[currentRoundID].AddBet(bet)
		message := fmt.Sprintf("Bet on %s for %d", onPlayer.User.DisplayName(), amount)

		err = discord.UpdateResponse(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
}
