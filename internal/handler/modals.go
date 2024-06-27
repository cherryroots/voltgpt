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

		byPlayer := gamble.Player{
			User: i.Interaction.Member.User,
		}
		onPlayer := gamble.Player{
			User: user.User,
		}

		modalInput := i.ModalSubmitData().Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
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

		existingBet, err := gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].HasBet(bet)
		existingAmount := 0
		if err == nil {
			existingAmount = existingBet.Amount
		}

		options := len(gamble.Wheel.CurrentWheelOptions())
		PlayerBets, _ := gamble.Wheel.PlayerBets(byPlayer, gamble.Wheel.CurrentRound())
		if options%2 == 1 {
			options++
		}
		if PlayerBets >= (options / 2) {
			err := discord.UpdateResponse(s, i, "You can only bet on half of the players")
			if err != nil {
				log.Println(err)
			}
			return

		}

		if amount > gamble.Wheel.PlayerUsableMoney(byPlayer)+existingAmount {
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

		gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].AddBet(bet)
		message := fmt.Sprintf("Bet on %s for %d", onPlayer.User.Username, amount)

		err = discord.UpdateResponse(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
}
