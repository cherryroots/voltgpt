package main

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/bwmarrin/discordgo"
)

var wheel = game{
	Rounds:     []round{},
	BetOptions: []player{},
	Players:    []player{},
}

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
		readWheelFromFile()

		return
	}

	buf := new(bytes.Buffer)

	if err := gob.NewEncoder(buf).Encode(wheel); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("wheel.gob", os.O_WRONLY|os.O_TRUNC, 0o644)
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
	Rounds     []round
	BetOptions []player
	Players    []player
}
type round struct {
	ID     int // id is 0-indexed
	Winner player
	Claims []player
	Bets   []bet
}

type bet struct {
	Amount int
	By     player
	On     player
}

type result struct {
	player player
	bet    bet
	won    bool
}

type player struct {
	User *discordgo.User
}

func (p player) id() string {
	return p.User.ID
}

func (g *game) addWheelOption(option player) {
	for _, player := range g.BetOptions {
		if player.id() == option.id() {
			return
		}
	}
	g.BetOptions = append(g.BetOptions, option)
}

func (g *game) removeWheelOption(option player) {
	for i, player := range g.BetOptions {
		if player.id() == option.id() {
			g.BetOptions = append(g.BetOptions[:i], g.BetOptions[i+1:]...)
			return
		}
	}
}

func (g *game) addPlayer(player player) {
	for _, p := range g.Players {
		if p.id() == player.id() {
			return
		}
	}
	g.Players = append(g.Players, player)
}

func (g *game) addRound() {
	ID := len(g.Rounds)
	g.Rounds = append(g.Rounds, round{ID: ID})
}

func (g *game) currentWheelOptions() []player {
	players := g.BetOptions
	var currentPlayers []player
	// add everyone who's not been picked
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds {
			// if no round winner
			if !round.hasWinner() {
				continue
			}
			if round.Winner.id() == player.id() {
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

func (g *game) wheelOptions(round round) []player {
	if round.ID >= len(g.Rounds) {
		return g.currentWheelOptions()
	}

	players := g.BetOptions
	var currentPlayers []player
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds[0:round.ID] {
			// if no round winner
			if !round.hasWinner() {
				continue
			}
			if round.Winner.id() == player.id() {
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

func (g *game) currentRound() round {
	if len(g.Rounds) == 0 {
		return round{}
	}
	return g.Rounds[len(g.Rounds)-1]
}

func (g *game) round(round int) round {
	if round >= len(g.Rounds) {
		return g.currentRound()
	}
	return g.Rounds[round-1]
}

func (g *game) playerMoney(player player, toRound round) int {
	var money int
	for _, r := range g.Rounds[0 : toRound.ID+1] {
		for _, claim := range r.Claims {
			if claim.id() == player.id() {
				money += 100
			}
		}
		if r.ID == toRound.ID {
			continue
		}

		if g.betsPercentage(player, r) < 10 {
			money -= g.playerTax(player, r)
		}

		money += g.payout(player, r)
	}
	return money
}

func (g *game) playerTax(player player, r round) int {
	playerMoney := g.playerMoney(player, r)
	betPercentage := 10 - g.betsPercentage(player, r)
	taxAmount := (playerMoney * 3 * betPercentage) / 100
	return taxAmount
}

func (g *game) payout(player player, r round) int {
	if !r.hasWinner() {
		return 0
	}

	var money int
	options := len(g.wheelOptions(r))
	for _, bet := range r.Bets {
		if bet.By.id() != player.id() {
			continue
		}
		if bet.On.id() == r.Winner.id() {
			money += bet.Amount * max(options-1, 0)
		} else {
			money -= bet.Amount
		}
	}
	return money
}

func (g *game) playerUsableMoney(player player) int {
	money := g.playerMoney(player, g.currentRound())
	var usedMoney int
	for _, bet := range g.currentRound().Bets {
		if bet.By.id() == player.id() {
			usedMoney += bet.Amount
		}
	}
	return money - usedMoney
}

func (g *game) playerBets(player player, round round) (int, int) {
	var bets, betAmount int
	for _, bet := range round.Bets {
		if bet.By.id() == player.id() {
			bets++
			betAmount += bet.Amount
		}
	}
	return bets, betAmount
}

func (g *game) betsPercentage(player player, r round) int {
	_, amount := g.playerBets(player, r)
	curMoney := g.playerMoney(player, r)
	if curMoney == 0 {
		return 0
	}
	betPercentage := amount * 100 / curMoney

	return betPercentage
}

func (g *game) underThresholdPlayers(r round) []player {
	var noBets []player
	for _, player := range g.Players {
		if g.betsPercentage(player, r) < 10 {
			noBets = append(noBets, player)
		}
	}
	return noBets
}

func (r *round) addBet(bet bet) {
	for i, b := range r.Bets {
		if b.By.id() == bet.By.id() && b.On.id() == bet.On.id() {
			r.Bets[i] = bet
			return
		}
	}
	r.Bets = append(r.Bets, bet)
}

func (r *round) removeBet(by player, on player) {
	for i, bet := range r.Bets {
		if bet.By.id() == by.id() && bet.On.id() == on.id() {
			r.Bets = append(r.Bets[:i], r.Bets[i+1:]...)
		}
	}
}

func (r *round) hasBet(newBet bet) (bet, error) {
	for _, b := range r.Bets {
		if b.By.id() == newBet.By.id() && b.On.id() == newBet.On.id() {
			return b, nil
		}
	}
	return bet{}, errors.New("no bet found")
}

func (r *round) setWinner(winner player) {
	r.Winner = winner
}

func (r *round) hasWinner() bool {
	return r.Winner.User != nil
}

func (r *round) roundOutcome() []result {
	var results []result
	if !r.hasWinner() {
		return results
	}
	for _, bet := range r.Bets {
		var outcome result
		if bet.On.id() == r.Winner.id() {
			outcome = result{
				player: bet.By,
				bet:    bet,
				won:    true,
			}
		} else {
			outcome = result{
				player: bet.By,
				bet:    bet,
				won:    false,
			}
		}
		results = append(results, outcome)
	}
	return results
}

func (r *round) addClaim(player player) {
	for _, c := range r.Claims {
		if c.id() == player.id() {
			return
		}
	}
	r.Claims = append(r.Claims, player)
}

func (g *game) statusEmbed(r round) discordgo.MessageEmbed {
	embed := discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{},
		Color:  0x00ff00,
		Title:  "Round " + strconv.Itoa(r.ID+1),
		Fields: []*discordgo.MessageEmbedField{},
	}
	var winner string
	if r.hasWinner() {
		winner = "Winner: " + r.Winner.User.Mention()
	}
	var playerNames, playerMoney, betPercentage string
	for _, player := range g.Players {
		playerNames += player.User.Username + "\n"
		playerMoney += strconv.Itoa(g.playerMoney(player, r)) + "\n"
		betPercentage += strconv.Itoa(g.betsPercentage(player, r)) + "%" + "\n"
	}
	var claims, claimAmount string
	for _, claim := range r.Claims {
		claims += claim.User.Username + "\n"
		claimAmount += strconv.Itoa(100) + "\n"
	}
	var playerBetsBy, playerBetsOn, playerBetsAmount string
	for _, bet := range r.Bets {
		playerBetsBy += bet.By.User.Username + "\n"
		playerBetsOn += bet.On.User.Username + "\n"
		playerBetsAmount += strconv.Itoa(bet.Amount) + "\n"
	}
	var outcome, outcomeAmount string
	options := len(g.wheelOptions(r))
	for _, result := range r.roundOutcome() {
		if result.won {
			outcome += fmt.Sprintf("Won: %s\n", result.player.User.Username)
			outcomeAmount += strconv.Itoa(result.bet.Amount*max(options-1, 0)) + "\n"
		} else {
			outcome += fmt.Sprintf("Lost: %s\n", result.player.User.Username)
			outcomeAmount += strconv.Itoa(-result.bet.Amount) + "\n"
		}
	}
	for _, player := range g.underThresholdPlayers(r) {
		outcome += fmt.Sprintf("Taxed: %s\n", player.User.Username)
		outcomeAmount += "-" + strconv.Itoa(g.playerTax(player, r)) + "\n"
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "✨ Player Statuses ✨",
		Value: winner,
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
		Name:   "Bet%",
		Value:  betPercentage,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Claims",
		Value:  claims,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Amount",
		Value:  claimAmount,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "✨ Round bets ✨",
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
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Outcome",
		Value:  outcome,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Amount",
		Value:  outcomeAmount,
		Inline: true,
	})
	return embed
}

func (g *game) sendMenu(s *discordgo.Session, i *discordgo.InteractionCreate, remove bool, winner bool) {
	var options []discordgo.SelectMenuOption
	for _, player := range g.currentWheelOptions() {
		options = append(options, discordgo.SelectMenuOption{
			Label: player.User.Username,
			Value: player.id(),
		})
	}
	if len(options) == 0 {
		deferEphemeralResponse(s, i)
		_, err := sendFollowup(s, i, "No players available")
		if err != nil {
			log.Println(err)
		}
		return
	}

	customID := "menu_bet"
	content := "Place a Bet"
	if remove {
		customID = "menu_bet-remove"
		content = "Remove a Bet"
	}
	if winner {
		customID = "menu_bet-winner"
		content = "Pick a Winner"
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
	if err != nil {
		log.Println(err)
	}
}

func (g *game) sendModal(s *discordgo.Session, i *discordgo.InteractionCreate, userID string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_bet-" + userID,
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

func makeButton(style discordgo.ButtonStyle, label string, emoji string, customID string) *discordgo.Button {
	return &discordgo.Button{
		Style:    style,
		Label:    label,
		Emoji:    &discordgo.ComponentEmoji{Name: emoji},
		CustomID: customID,
	}
}

var roundMessageComponents = []discordgo.MessageComponent{
	&discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			makeButton(discordgo.PrimaryButton, "", "🔄", "button_refresh"),
			makeButton(discordgo.PrimaryButton, "Claim!", "📈", "button_claim"),
			makeButton(discordgo.SecondaryButton, "Place Bet!", "💸", "button_bet"),
			makeButton(discordgo.SecondaryButton, "Remove Bet!", "💰", "button_bet-remove"),
			makeButton(discordgo.SuccessButton, "Set Winner!", "✨", "button_winner"),
		},
	},
}
