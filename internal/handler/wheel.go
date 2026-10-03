package handler

import (
	"context"
	"fmt"
	"log"

	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

func wheelStatusCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferResponse(s, i)

	gamble.Mu.Lock()
	defer gamble.Mu.Unlock()

	if len(gamble.GameState.Rounds) == 0 {
		gamble.GameState.AddRound()
	}

	var round int
	for _, option := range i.ApplicationCommandData().Options {
		if option.Name == "round" {
			round = int(option.IntValue())
		}
	}

	if round == 0 {
		round = gamble.GameState.CurrentRound().ID + 1
	}

	statusRound := gamble.GameState.Round(round)
	embed := gamble.GameState.StatusEmbed(statusRound)
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds:     []*discordgo.MessageEmbed{&embed},
		Components: gambleStatusComponentsLocked(round),
		Flags:      1 << 12,
	})
	if err != nil {
		log.Println(err)
	}
}

func wheelAddCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	gamble.Mu.Lock()
	defer gamble.Mu.Unlock()

	var user *discordgo.User
	var remove bool
	for _, option := range i.ApplicationCommandData().Options {
		switch option.Name {
		case "user":
			user = option.UserValue(s)
		case "remove":
			remove = option.BoolValue()
		}
	}

	if !utility.IsAdmin(i.Interaction.Member.User.ID) {
		respond(s, i, "Only admins can add players to the wheel!")
		return
	}

	var message string
	player := gamble.Player{
		User: user,
	}
	if remove {
		gamble.GameState.RemoveWheelOption(player)
		message = fmt.Sprintf("Removed %s from the wheel!", player.User.DisplayName())
	} else {
		gamble.GameState.AddWheelOption(player)
		message = fmt.Sprintf("Added %s to the wheel!", player.User.DisplayName())
	}

	respond(s, i, message)
}

func insertBetCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	var on, by *discordgo.User
	var amount, round int
	for _, option := range i.ApplicationCommandData().Options {
		switch option.Name {
		case "on":
			on = option.UserValue(s)
		case "by":
			by = option.UserValue(s)
		case "amount":
			amount = int(option.IntValue())
		case "round":
			round = int(option.IntValue())
		}
	}

	if !requireAdmin(s, i) {
		return
	}

	gamble.Mu.Lock()
	defer gamble.Mu.Unlock()

	if round <= 0 || round > len(gamble.GameState.Rounds) {
		respond(s, i, fmt.Sprintf("Invalid round number! Must be between 1 and %d", len(gamble.GameState.Rounds)))
		return
	}

	onPlayer := gamble.Player{
		User: on,
	}
	byPlayer := gamble.Player{
		User: by,
	}

	bet := gamble.Bet{
		By:     byPlayer,
		On:     onPlayer,
		Amount: amount,
	}

	var message string
	if bet.Amount == 0 {
		gamble.GameState.Rounds[round-1].RemoveBet(byPlayer, onPlayer)
		message = fmt.Sprintf("Removed bet on %s, by %s on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), round)
	} else {
		gamble.GameState.Rounds[round-1].AddBet(bet)
		message = fmt.Sprintf("Added bet on %s, by %s for %d on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), amount, round)
	}

	respond(s, i, message)
}

func resetWheelCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	if !requireAdmin(s, i) {
		return
	}

	gamble.Mu.Lock()
	defer gamble.Mu.Unlock()

	var keepOptions bool
	for _, option := range i.ApplicationCommandData().Options {
		if option.Name == "keep_options" {
			keepOptions = option.BoolValue()
		}
	}

	message := "Wheel reset!"
	if keepOptions {
		gamble.GameState.ResetWheelKeepOptions()
		message = "Wheel rounds reset. Bet options kept."
	} else {
		gamble.GameState.ResetWheel()
	}

	respond(s, i, message)
}
