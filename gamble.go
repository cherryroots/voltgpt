package main

import (
	"bytes"
	"encoding/gob"
	"log"
	"os"
	"strconv"

	"github.com/bwmarrin/discordgo"
)

var (
	wheel = game{
		Rounds:       []round{},
		WheelOptions: []player{},
		Players:      []player{},
	}
)

func writeWheelToFile() {

	if _, err := os.Stat("wheel.gob"); os.IsNotExist(err) {
		file, err := os.Create("wheel.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(wheel); err != nil {
			log.Fatal(err)
		}

		readHashFromFile()

		return
	}

	buf := new(bytes.Buffer)

	if err := gob.NewEncoder(buf).Encode(wheel); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("wheel.gob", os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("OpenFile error: %v\n", err)
		return
	}
	defer file.Close()

	if _, err := buf.WriteTo(file); err != nil {
		log.Printf("WriteTo error: %v\n", err)
		return
	}
}

func readWheelFromFile() {
	dataFile, err := os.Open("wheel.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&wheel); err != nil {
		log.Fatal(err)
	}
}

type game struct {
	Rounds       []round
	WheelOptions []player
	Players      []player
}
type round struct {
	ID     int
	Winner player
	Claims []player
	Bets   []bet
}

type bet struct {
	Amount int
	By     player
	On     player
}

type player struct {
	User *discordgo.User
}

func (p player) ID() string {
	return p.User.ID
}

func (g *game) addWheelOption(option player) {
	for _, player := range g.WheelOptions {
		if player.User.ID == option.User.ID {
			return
		}
	}
	g.WheelOptions = append(g.WheelOptions, option)
}

func (g *game) addPlayer(player player) {
	for _, p := range g.Players {
		if p.User.ID == player.User.ID {
			return
		}
	}
	g.Players = append(g.Players, player)
}

func (g *game) addRound() {
	id := len(g.Rounds)
	g.Rounds = append(g.Rounds, round{ID: id})
}

func (g *game) currentWheelOptions() []player {
	players := g.WheelOptions
	var currentPlayers []player
	// add everyone who's not been picked
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds {
			// if no round winner
			if !round.hasWinner() {
				continue
			}
			if round.Winner.ID() == player.ID() {
				picked = true
				break
			}
		}
		if !picked {
			currentPlayers = append(currentPlayers, player)
		}
	}

	return currentPlayers
}

func (g *game) currentRound() *round {
	return &g.Rounds[len(g.Rounds)-1]
}

func (g *game) getRound(round int) *round {
	if round >= len(g.Rounds) {
		return g.currentRound()
	}
	return &g.Rounds[round-1]
}

func (g *game) getPlayerMoney(player player) int {
	var money int
	for _, round := range g.Rounds {
		for _, claim := range round.Claims {
			if claim.ID() == player.ID() {
				money += 100
			}
		}
		for _, bet := range round.Bets {
			if !round.hasWinner() {
				continue
			}
			if bet.On.ID() == round.Winner.ID() {
				money += bet.Amount
			} else {
				money -= bet.Amount
			}
		}
	}
	return money
}

func (g *game) checkPlayerAvailableMoney(player player) int {
	money := g.getPlayerMoney(player)
	var usedMoney int
	for _, bet := range g.currentRound().Bets {
		if bet.On.ID() == player.ID() {
			usedMoney += bet.Amount
		}
	}

	return money - usedMoney
}

func (r *round) addBet(bet bet) {
	for i, b := range r.Bets {
		if b.By.ID() == bet.By.ID() && b.On.ID() == bet.On.ID() {
			r.Bets[i] = bet
			return
		}
	}
	r.Bets = append(r.Bets, bet)
}

func (r *round) removeBetOnPlayer(by player, on player) {
	for i, bet := range r.Bets {
		if bet.By.ID() == by.ID() && bet.On.ID() == on.ID() {
			r.Bets = append(r.Bets[:i], r.Bets[i+1:]...)
		}
	}
}

func (r *round) setWinner(picked player) {
	r.Winner = picked
}

func (r *round) hasWinner() bool {
	return r.Winner.User != nil
}

func (r *round) getWinners() []player {
	var winners []player
	if !r.hasWinner() {
		return winners
	}
	for _, bet := range r.Bets {
		if bet.On.ID() == r.Winner.ID() {
			winners = append(winners, bet.By)
		}
	}
	return winners
}

func (r *round) getLosers() []player {
	var losers []player
	if !r.hasWinner() {
		return losers
	}
	for _, bet := range r.Bets {
		if bet.On.ID() == r.Winner.ID() {
			losers = append(losers, bet.By)
		}
	}
	return losers
}

func (r *round) addClaim(player player) {
	for _, c := range r.Claims {
		if c.ID() == player.ID() {
			return
		}
	}
	r.Claims = append(r.Claims, player)
}

func (g *game) statusEmbed(r *round) discordgo.MessageEmbed {
	embed := discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{},
		Color:  0x00ff00,
		Title:  "Round " + strconv.Itoa(r.ID+1),
		Fields: []*discordgo.MessageEmbedField{},
	}
	var winner string
	if r.hasWinner() {
		winner = r.Winner.User.Username
	}
	var playerNames, playerMoney string
	for _, player := range g.Players {
		playerNames += player.User.Username + "\n"
		playerMoney += strconv.Itoa(g.getPlayerMoney(player)) + "\n"
	}
	var playerBetsBy, playerBetsOn, playerBetsAmount string
	for _, bet := range r.Bets {
		playerBetsBy += bet.By.User.Username + "\n"
		playerBetsOn += bet.On.User.Username + "\n"
		playerBetsAmount += strconv.Itoa(bet.Amount) + "\n"
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "Player Statuses",
		Value: "Winner: " + winner,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Players",
		Value:  playerNames,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Money",
		Value:  playerMoney,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "Round bets",
		Value: "",
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "By",
		Value:  playerBetsBy,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "On",
		Value:  playerBetsOn,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Amount",
		Value:  playerBetsAmount,
		Inline: true,
	})
	return embed
}

func (g *game) sendMenu(s *discordgo.Session, i *discordgo.InteractionCreate, remove bool, winner bool) {
	var options []discordgo.SelectMenuOption
	for _, player := range g.currentWheelOptions() {
		options = append(options, discordgo.SelectMenuOption{
			Label: player.User.Username,
			Value: player.ID(),
		})
	}

	var customID = "menu_bet"
	var content = "Place a Bet"
	if remove {
		customID = "menu_bet-remove"
		content = "Remove a Bet"
	}
	if winner {
		customID = "menu_bet-winner"
		content = "Pick a Winner"
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							MenuType:    discordgo.StringSelectMenu,
							CustomID:    customID,
							Placeholder: "Select a player",
							Options:     options,
						},
					},
				},
			},
		},
	})
}

func (g *game) sendModal(s *discordgo.Session, i *discordgo.InteractionCreate, userid string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_bet-" + userid,
			Title:    "Place a Bet",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "amount",
							Label:       "Amount",
							Style:       discordgo.TextInputShort,
							Placeholder: "Amount",
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Println(err)
	}
}

var retryButton = &discordgo.Button{
	Style:    discordgo.PrimaryButton,
	Emoji:    discordgo.ComponentEmoji{Name: "🔄"},
	CustomID: "button_refresh",
}

var claimButton = &discordgo.Button{
	Style:    discordgo.PrimaryButton,
	Label:    "Claim!",
	Emoji:    discordgo.ComponentEmoji{Name: "📈"},
	CustomID: "button_claim",
}

var betButton = &discordgo.Button{
	Style:    discordgo.SecondaryButton,
	Label:    "Place Bet!",
	Emoji:    discordgo.ComponentEmoji{Name: "💸"},
	CustomID: "button_bet",
}

var removeBetButton = &discordgo.Button{
	Style:    discordgo.SecondaryButton,
	Label:    "Remove Bet!",
	Emoji:    discordgo.ComponentEmoji{Name: "💰"},
	CustomID: "button_bet-remove",
}

var pickWinnerButton = &discordgo.Button{
	Style:    discordgo.SuccessButton,
	Emoji:    discordgo.ComponentEmoji{Name: "✨"},
	Label:    "Set Winner!",
	CustomID: "button_winner",
}

var roundMessageComponents = []discordgo.MessageComponent{
	&discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			retryButton,
			claimButton,
			betButton,
			removeBetButton,
			pickWinnerButton,
		},
	},
}
