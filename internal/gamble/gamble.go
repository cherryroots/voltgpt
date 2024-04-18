// Package gamble is a utility package for the weekly movie gamble.
package gamble

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/discord"
)

// Wheel is a data structure that stores data related to the Wheel game.
var Wheel = game{
	Rounds:     []round{},
	BetOptions: []Player{},
	Players:    []Player{},
}

// WriteToFile writes the data related to the Wheel to a file named "wheel.gob".
//
// If the file doesn't exist, it creates a new one and encodes the Wheel data using gob.
//
// If the file already exists, it truncates it and encodes the Wheel data again.
//
// If an error occurs during file operations, it logs the error.
func WriteToFile() {
	if _, err := os.Stat("wheel.gob"); os.IsNotExist(err) {
		file, err := os.Create("wheel.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(Wheel); err != nil {
			log.Fatal(err)
		}

		ReadFromFile()

		return
	}

	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(Wheel); err != nil {
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

// ReadFromFile reads data from a file named "wheel.gob" and decodes it into the global variable Wheel.
//
// If an error occurs during decoding or opening the file, it returns without doing anything.
func ReadFromFile() {
	dataFile, err := os.Open("wheel.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&Wheel); err != nil {
		log.Fatal(err)
	}
}

// game is a struct representing the game.
type game struct {
	Rounds     []round
	BetOptions []Player
	Players    []Player
}

// round is a struct representing a round in the wheel.
type round struct {
	ID     int // id is 0-indexed
	Winner Player
	Claims []Player
	Bets   []Bet
}

// Bet is a struct representing a bet on a player.
type Bet struct {
	Amount int
	By     Player
	On     Player
}

// result is a struct representing the result of a bet.
type result struct {
	player Player
	bet    Bet
	won    bool
}

// Player is a struct representing a player.
type Player struct {
	User *discordgo.User
}

// ID returns the ID of the player.
func (p Player) ID() string {
	return p.User.ID
}

// AddWheelOption adds a new player option to the wheel if it doesn't already exist.
func (g *game) AddWheelOption(option Player) {
	for _, player := range g.BetOptions {
		if player.ID() == option.ID() {
			return
		}
	}
	g.BetOptions = append(g.BetOptions, option)
}

// RemoveWheelOption removes the specified player option from the game's BetOptions slice.
func (g *game) RemoveWheelOption(option Player) {
	for i, player := range g.BetOptions {
		if player.ID() == option.ID() {
			g.BetOptions = append(g.BetOptions[:i], g.BetOptions[i+1:]...)
			return
		}
	}
}

// AddPlayer adds a new player to the game if the player doesn't already exist.
func (g *game) AddPlayer(player Player) {
	for _, p := range g.Players {
		if p.ID() == player.ID() {
			return
		}
	}
	g.Players = append(g.Players, player)
}

// AddRound adds a new round to the game.
func (g *game) AddRound() {
	ID := len(g.Rounds)
	g.Rounds = append(g.Rounds, round{ID: ID})
}

// CurrentWheelOptions returns the current available players for the wheel based on the rounds won.
func (g *game) CurrentWheelOptions() []Player {
	players := g.BetOptions
	var currentPlayers []Player
	// add everyone who's not been picked
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds {
			// if no round winner
			if !round.HasWinner() {
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

// wheelOptions returns the available players for the wheel based on the rounds won.
func (g *game) wheelOptions(round round) []Player {
	if round.ID >= len(g.Rounds) {
		return g.CurrentWheelOptions()
	}

	players := g.BetOptions
	var currentPlayers []Player
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds[0:round.ID] {
			// if no round winner
			if !round.HasWinner() {
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

// CurrentRound returns the current round in the game.
func (g *game) CurrentRound() round {
	if len(g.Rounds) == 0 {
		return round{}
	}
	return g.Rounds[len(g.Rounds)-1]
}

// Round returns the specified round in the game.
func (g *game) Round(round int) round {
	if round >= len(g.Rounds) {
		return g.CurrentRound()
	}
	return g.Rounds[round-1]
}

// TotalRounds returns the total number of rounds in the game.
func (g *game) TotalRounds() int {
	return len(g.Rounds)
}

// playerMoney calculates the total money of a player up to a specified round in the game.
func (g *game) playerMoney(player Player, toRound round) int {
	var money int
	for _, r := range g.Rounds[0 : toRound.ID+1] {
		for _, claim := range r.Claims {
			if claim.ID() == player.ID() {
				money += 100
			}
		}
		if r.ID == toRound.ID {
			return money
		}

		if g.betsPercentage(player, r) < 10 {
			money -= g.playerTax(player, r)
		}

		money += g.payout(player, r)
	}
	return money
}

// playerTax calculates the tax amount for a player based on their player money, bet percentage, and a fixed multiplier.
func (g *game) playerTax(player Player, r round) int {
	playerMoney := g.playerMoney(player, r)
	betPercentage := 10 - g.betsPercentage(player, r)
	taxAmount := (playerMoney * 3 * betPercentage) / 100
	return taxAmount
}

// payout calculates the money to be paid out to a player based on the round's outcome.
func (g *game) payout(player Player, r round) int {
	if !r.HasWinner() {
		return 0
	}

	var money int
	options := len(g.wheelOptions(r))
	for _, bet := range r.Bets {
		if bet.By.ID() != player.ID() {
			continue
		}
		if bet.On.ID() == r.Winner.ID() {
			money += bet.Amount * max(options-1, 0)
		} else {
			money -= bet.Amount
		}
	}
	return money
}

// PlayerUsableMoney calculates the usable money of a player for the current round in the game.
func (g *game) PlayerUsableMoney(player Player) int {
	money := g.playerMoney(player, g.CurrentRound())
	var usedMoney int
	for _, bet := range g.CurrentRound().Bets {
		if bet.By.ID() == player.ID() {
			usedMoney += bet.Amount
		}
	}
	return money - usedMoney
}

// PlayerBets calculates the number of bets and the total bet amount placed by a specific player in a round.
func (g *game) PlayerBets(player Player, round round) (int, int) {
	var bets, betAmount int
	for _, bet := range round.Bets {
		if bet.By.ID() == player.ID() {
			bets++
			betAmount += bet.Amount
		}
	}
	return bets, betAmount
}

// betsPercentage calculates the percentage of the total bet amount placed by a player compared to the player's current money in the round.
func (g *game) betsPercentage(player Player, r round) int {
	_, amount := g.PlayerBets(player, r)
	curMoney := g.playerMoney(player, r)
	if curMoney == 0 {
		return 0
	}
	betPercentage := amount * 100 / curMoney

	return betPercentage
}

// underThresholdPlayers filters players based on their bet percentage in the round being under 10%.
func (g *game) underThresholdPlayers(r round) []Player {
	var noBets []Player
	for _, player := range g.Players {
		if g.betsPercentage(player, r) < 10 {
			noBets = append(noBets, player)
		}
	}
	return noBets
}

// AddBet adds a bet to the round, replacing an existing bet if the same bettor and choice are found.
func (r *round) AddBet(bet Bet) {
	for i, b := range r.Bets {
		if b.By.ID() == bet.By.ID() && b.On.ID() == bet.On.ID() {
			r.Bets[i] = bet
			return
		}
	}
	r.Bets = append(r.Bets, bet)
}

// RemoveBet removes a bet from the round if the bettor and choice match the specified players.
func (r *round) RemoveBet(by Player, on Player) {
	for i, bet := range r.Bets {
		if bet.By.ID() == by.ID() && bet.On.ID() == on.ID() {
			r.Bets = append(r.Bets[:i], r.Bets[i+1:]...)
		}
	}
}

// HasBet checks if a specific bet exists in the round.
func (r *round) HasBet(newBet Bet) (Bet, error) {
	for _, b := range r.Bets {
		if b.By.ID() == newBet.By.ID() && b.On.ID() == newBet.On.ID() {
			return b, nil
		}
	}
	return Bet{}, errors.New("no bet found")
}

// SetWinner sets the winner of the round.
func (r *round) SetWinner(winner Player) {
	r.Winner = winner
}

// HasWinner checks if the round has a winner.
func (r *round) HasWinner() bool {
	return r.Winner.User != nil
}

// roundOutcome calculates the outcome of the round based on the bets placed.
func (r *round) roundOutcome() []result {
	var results []result
	if !r.HasWinner() {
		return results
	}
	for _, bet := range r.Bets {
		var outcome result
		if bet.On.ID() == r.Winner.ID() {
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

// AddClaim adds a claim to the round if the player has not already claimed.
func (r *round) AddClaim(player Player) {
	for _, c := range r.Claims {
		if c.ID() == player.ID() {
			return
		}
	}
	r.Claims = append(r.Claims, player)
}

// StatusEmbed generates a discordgo.MessageEmbed with the status information for the given round.
func (g *game) StatusEmbed(r round) discordgo.MessageEmbed {
	embed := discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{},
		Color:  0x00ff00,
		Title:  "Round " + strconv.Itoa(r.ID+1),
		Fields: []*discordgo.MessageEmbedField{},
	}
	var winner string
	if r.HasWinner() {
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
	var PlayerBetsBy, PlayerBetsOn, PlayerBetsAmount string
	for _, bet := range r.Bets {
		PlayerBetsBy += bet.By.User.Username + "\n"
		PlayerBetsOn += bet.On.User.Username + "\n"
		PlayerBetsAmount += strconv.Itoa(bet.Amount) + "\n"
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
		Value:  PlayerBetsBy,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "On",
		Value:  PlayerBetsOn,
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Amount",
		Value:  PlayerBetsAmount,
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

// SendMenu sends a menu to a Discord user with options based on the game state.
func (g *game) SendMenu(s *discordgo.Session, i *discordgo.InteractionCreate, remove bool, winner bool) {
	var options []discordgo.SelectMenuOption
	for _, player := range g.CurrentWheelOptions() {
		options = append(options, discordgo.SelectMenuOption{
			Label: player.User.Username,
			Value: player.ID(),
		})
	}
	if len(options) == 0 {
		discord.DeferEphemeralResponse(s, i)
		_, err := discord.SendFollowup(s, i, "No players available")
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

// SendModal sends a modal to the user for placing a bet.
func (g *game) SendModal(s *discordgo.Session, i *discordgo.InteractionCreate, userID string) {
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

// makeButton creates a new Discord button with the specified style, label, emoji, and custom ID.
func makeButton(style discordgo.ButtonStyle, label string, emoji string, customID string) *discordgo.Button {
	return &discordgo.Button{
		Style:    style,
		Label:    label,
		Emoji:    &discordgo.ComponentEmoji{Name: emoji},
		CustomID: customID,
	}
}

// RoundMessageComponents are the components for the round message.
var RoundMessageComponents = []discordgo.MessageComponent{
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
