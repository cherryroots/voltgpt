// Package gamble is a utility package for the weekly movie gamble.
package gamble

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/discord"
)

var database *sql.DB

// Mu protects all GameState access. Lock in handlers before accessing GameState.
var Mu sync.Mutex

var GameState = game{
	Rounds:     []round{},
	BetOptions: []Player{},
	Players:    []Player{},
}

func Init(db *sql.DB) {
	database = db
	loadFromDB()
}

func loadFromDB() {
	var data string
	err := database.QueryRow("SELECT data FROM game_state WHERE id = 1").Scan(&data)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		log.Fatalf("Failed to load game_state: %v", err)
	}

	if err := json.Unmarshal([]byte(data), &GameState); err != nil {
		log.Fatalf("Failed to unmarshal game state: %v", err)
	}
}

func saveToDB() {
	if database == nil {
		return
	}
	data, err := json.Marshal(GameState)
	if err != nil {
		log.Printf("Failed to marshal game state: %v", err)
		return
	}
	_, err = database.Exec(
		"INSERT OR REPLACE INTO game_state (id, data) VALUES (1, ?)",
		string(data),
	)
	if err != nil {
		log.Printf("Failed to save game state: %v", err)
	}
}

type game struct {
	Rounds     []round  `json:"rounds"`
	BetOptions []Player `json:"bet_options"`
	Players    []Player `json:"players"`
}

type round struct {
	ID     int      `json:"id"` // id is 0-indexed
	Winner Player   `json:"winner"`
	Claims []Player `json:"claims"`
	Bets   []Bet    `json:"bets"`
}

type Bet struct {
	Amount int    `json:"amount"`
	By     Player `json:"by"`
	On     Player `json:"on"`
}

type result struct {
	player Player
	bet    Bet
	won    bool
}

type statusPlayerRow struct {
	player         Player
	money          int
	betPercentage  int
	underThreshold bool
}

type outcomeEntry struct {
	label  string
	player Player
	amount int
	before int
	after  int
}

var ResolvedRoundMessageComponents = []discordgo.MessageComponent{
	&discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			makeButton(discordgo.PrimaryButton, "View Current Round", "🎯", "button_currentround"),
		},
	},
}

type Player struct {
	User *discordgo.User `json:"user"`
}

func (p Player) ID() string {
	return p.User.ID
}

func (g *game) AddWheelOption(option Player) {
	for _, player := range g.BetOptions {
		if player.ID() == option.ID() {
			return
		}
	}
	g.BetOptions = append(g.BetOptions, option)
	saveToDB()
}

func (g *game) RemoveWheelOption(option Player) {
	for i, player := range g.BetOptions {
		if player.ID() == option.ID() {
			g.BetOptions = append(g.BetOptions[:i], g.BetOptions[i+1:]...)
			saveToDB()
			return
		}
	}
}

func (g *game) ResetWheel() {
	g.resetWheel(false)
}

func (g *game) ResetWheelKeepOptions() {
	g.resetWheel(true)
}

func (g *game) resetWheel(keepOptions bool) {
	g.Rounds = []round{}
	g.Players = []Player{}
	if !keepOptions {
		g.BetOptions = []Player{}
	}
	saveToDB()
}

func (g *game) AddPlayer(player Player) {
	for _, p := range g.Players {
		if p.ID() == player.ID() {
			return
		}
	}
	g.Players = append(g.Players, player)
	saveToDB()
}

func (g *game) AddRound() {
	ID := len(g.Rounds)
	g.Rounds = append(g.Rounds, round{ID: ID})
	saveToDB()
}

func (g *game) CurrentWheelOptions() []Player {
	players := g.BetOptions
	var currentPlayers []Player
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds {
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

func (g *game) wheelOptions(round round) []Player {
	if round.ID >= len(g.Rounds) {
		return g.CurrentWheelOptions()
	}

	players := g.BetOptions
	var currentPlayers []Player
	for _, player := range players {
		var picked bool
		for _, round := range g.Rounds[0:round.ID] {
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

func (g *game) CurrentRound() round {
	if len(g.Rounds) == 0 {
		return round{}
	}
	return g.Rounds[len(g.Rounds)-1]
}

func (g *game) Round(round int) round {
	if round <= 0 || round > len(g.Rounds) {
		return g.CurrentRound()
	}
	return g.Rounds[round-1]
}

func (g *game) TotalRounds() int {
	return len(g.Rounds)
}

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

		_, betAmount := g.PlayerBets(player, r)
		var betPercentage int
		if money > 0 {
			betPercentage = betAmount * 100 / money
		}

		if betPercentage < 10 {
			taxPercentage := 10 - betPercentage
			money -= (money * 3 * taxPercentage) / 100
		}

		money += g.payout(player, r)
	}
	return money
}

func (g *game) playerTax(player Player, r round) int {
	playerMoney := g.playerMoney(player, r)
	betPercentage := max(0, 10-g.betsPercentage(player, r))
	taxAmount := (playerMoney * 3 * betPercentage) / 100
	return taxAmount
}

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

func (g *game) betsPercentage(player Player, r round) int {
	_, amount := g.PlayerBets(player, r)
	curMoney := g.playerMoney(player, r)
	if curMoney == 0 {
		return 0
	}
	betPercentage := amount * 100 / curMoney

	return betPercentage
}

func (g *game) underThresholdPlayers(r round) []Player {
	var noBets []Player
	for _, player := range g.Players {
		if g.betsPercentage(player, r) < 10 {
			noBets = append(noBets, player)
		}
	}
	return noBets
}

func (r *round) AddBet(bet Bet) {
	for i, b := range r.Bets {
		if b.By.ID() == bet.By.ID() && b.On.ID() == bet.On.ID() {
			r.Bets[i] = bet
			saveToDB()
			return
		}
	}
	r.Bets = append(r.Bets, bet)
	saveToDB()
}

func (r *round) RemoveBet(by Player, on Player) {
	for i, bet := range r.Bets {
		if bet.By.ID() == by.ID() && bet.On.ID() == on.ID() {
			r.Bets = append(r.Bets[:i], r.Bets[i+1:]...)
			break
		}
	}
	saveToDB()
}

func (r *round) HasBet(newBet Bet) (Bet, bool) {
	for _, b := range r.Bets {
		if b.By.ID() == newBet.By.ID() && b.On.ID() == newBet.On.ID() {
			return b, true
		}
	}
	return Bet{}, false
}

func (r *round) SetWinner(winner Player) {
	r.Winner = winner
	saveToDB()
}

func (r *round) HasWinner() bool {
	return r.Winner.User != nil
}

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

func (r *round) AddClaim(player Player) {
	for _, c := range r.Claims {
		if c.ID() == player.ID() {
			return
		}
	}
	r.Claims = append(r.Claims, player)
	saveToDB()
}

func (g *game) RoundState(r round) string {
	if r.HasWinner() {
		return "Resolved"
	}
	return "Open"
}

func (g *game) winnerStatus(r round) string {
	if r.HasWinner() {
		return "Winner: ||" + r.Winner.User.Mention() + "||"
	}
	return "Winner: _Not set_"
}

func placeholderValue(value string, empty string) string {
	if strings.TrimSpace(value) == "" {
		return empty
	}
	return value
}

func (g *game) menuPlayersForRound(actor Player, r round, remove bool, winner bool) []Player {
	sortPlayers := func(players []Player) []Player {
		sort.SliceStable(players, func(i, j int) bool {
			return players[i].User.DisplayName() < players[j].User.DisplayName()
		})
		return players
	}

	if remove {
		targets := make(map[string]struct{})
		for _, bet := range r.Bets {
			if bet.By.ID() == actor.ID() {
				targets[bet.On.ID()] = struct{}{}
			}
		}

		var players []Player
		for _, player := range g.wheelOptions(r) {
			if _, ok := targets[player.ID()]; ok {
				players = append(players, player)
			}
		}
		return sortPlayers(players)
	}

	if winner {
		return sortPlayers(g.wheelOptions(r))
	}

	return sortPlayers(g.wheelOptions(r))
}

func ParseBetAmountInput(input string, maxStake int) (int, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("empty amount")
	}

	if strings.HasSuffix(input, "%") {
		percentageInput := strings.TrimSpace(strings.TrimSuffix(input, "%"))
		percentage, err := strconv.Atoi(percentageInput)
		if err != nil {
			return 0, err
		}
		return (maxStake*percentage + 99) / 100, nil
	}

	return strconv.Atoi(input)
}

func formatSignedAmount(amount int) string {
	if amount > 0 {
		return "+" + strconv.Itoa(amount)
	}
	return strconv.Itoa(amount)
}

func (g *game) statusPlayerRows(r round) []statusPlayerRow {
	rows := make([]statusPlayerRow, 0, len(g.Players))
	for _, player := range g.Players {
		betPercentage := g.betsPercentage(player, r)
		rows = append(rows, statusPlayerRow{
			player:         player,
			money:          g.playerMoney(player, r),
			betPercentage:  betPercentage,
			underThreshold: betPercentage < 10,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].money != rows[j].money {
			return rows[i].money > rows[j].money
		}
		return rows[i].player.User.DisplayName() < rows[j].player.User.DisplayName()
	})

	return rows
}

func (g *game) resolvedOutcomeEntries(r round) []outcomeEntry {
	if !r.HasWinner() {
		return nil
	}

	currentBalances := make(map[string]int, len(g.Players))
	for _, player := range g.Players {
		currentBalances[player.ID()] = g.playerMoney(player, r)
	}

	options := len(g.wheelOptions(r))
	var entries []outcomeEntry
	for _, result := range r.roundOutcome() {
		delta := -result.bet.Amount
		label := "Lost"
		if result.won {
			label = "Won"
			delta = result.bet.Amount * max(options-1, 0)
		}
		before := currentBalances[result.player.ID()]
		after := before + delta
		entries = append(entries, outcomeEntry{
			label:  label,
			player: result.player,
			amount: delta,
			before: before,
			after:  after,
		})
		currentBalances[result.player.ID()] = after
	}
	for _, player := range g.underThresholdPlayers(r) {
		delta := -g.playerTax(player, r)
		before := currentBalances[player.ID()]
		after := before + delta
		entries = append(entries, outcomeEntry{
			label:  "Taxed",
			player: player,
			amount: delta,
			before: before,
			after:  after,
		})
		currentBalances[player.ID()] = after
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].amount != entries[j].amount {
			return entries[i].amount > entries[j].amount
		}
		return entries[i].player.User.DisplayName() < entries[j].player.User.DisplayName()
	})

	return entries
}

func (g *game) resolvedOutcomeGroups(r round) (string, string, string, string, string, string) {
	var wonOutcome, wonAmount string
	var lostOutcome, lostAmount string
	var taxedOutcome, taxedAmount string

	for _, entry := range g.resolvedOutcomeEntries(r) {
		switch entry.label {
		case "Won":
			wonOutcome += fmt.Sprintf("Won: %s\n", entry.player.User.DisplayName())
			wonAmount += formatSignedAmount(entry.amount) + "\n"
		case "Lost":
			lostOutcome += fmt.Sprintf("Lost: %s\n", entry.player.User.DisplayName())
			lostAmount += formatSignedAmount(entry.amount) + "\n"
		case "Taxed":
			taxedOutcome += fmt.Sprintf("Taxed: %s\n", entry.player.User.DisplayName())
			taxedAmount += formatSignedAmount(entry.amount) + "\n"
		}
	}

	return wonOutcome, wonAmount, lostOutcome, lostAmount, taxedOutcome, taxedAmount
}

func (g *game) resolvedOutcomeColumns(r round) (string, string, string) {
	var outcomes []string
	var amounts []string
	var deltas []string

	for _, entry := range g.resolvedOutcomeEntries(r) {
		outcomes = append(outcomes, fmt.Sprintf("%s: %s", entry.label, entry.player.User.DisplayName()))
		amounts = append(amounts, formatSignedAmount(entry.amount))
		deltas = append(deltas, fmt.Sprintf("%d -> %d", entry.before, entry.after))
	}

	return strings.Join(outcomes, "\n"), strings.Join(amounts, "\n"), strings.Join(deltas, "\n")
}

func (g *game) outcomeSummaryCounts(r round) (wins, losses, taxed int) {
	for _, entry := range g.resolvedOutcomeEntries(r) {
		switch entry.label {
		case "Won":
			wins++
		case "Lost":
			losses++
		case "Taxed":
			taxed++
		}
	}
	return wins, losses, taxed
}

func (g *game) footerText(r round) string {
	parts := []string{
		fmt.Sprintf("%d claims", len(r.Claims)),
		fmt.Sprintf("%d bets", len(r.Bets)),
	}
	if r.HasWinner() {
		wins, losses, _ := g.outcomeSummaryCounts(r)
		parts = append(parts,
			fmt.Sprintf("%d wins", wins),
			fmt.Sprintf("%d losses", losses),
			fmt.Sprintf("%d taxed", len(g.underThresholdPlayers(r))),
		)
	} else {
		parts = append(parts, fmt.Sprintf("%d taxed", len(g.underThresholdPlayers(r))))
	}
	return strings.Join(parts, " • ")
}

func (g *game) StatusEmbed(r round) discordgo.MessageEmbed {
	color := 0x00ff00
	if r.HasWinner() {
		color = 0xff0000
	}

	embed := discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{},
		Color:  color,
		Title:  "Round " + strconv.Itoa(r.ID+1),
		Fields: []*discordgo.MessageEmbedField{},
		Footer: &discordgo.MessageEmbedFooter{
			Text: g.footerText(r),
		},
	}
	var playerNames, playerMoney, betPercentage string
	for _, row := range g.statusPlayerRows(r) {
		playerNames += row.player.User.DisplayName() + "\n"
		playerMoney += strconv.Itoa(row.money) + "\n"
		betPct := strconv.Itoa(row.betPercentage) + "%"
		if row.underThreshold {
			betPct = "**" + betPct + "**"
		}
		betPercentage += betPct + "\n"
	}
	var claims []string
	var claimNames []string
	for _, claim := range r.Claims {
		claimNames = append(claimNames, claim.User.DisplayName())
	}
	sort.Strings(claimNames)

	var temp string
	for i, claimName := range claimNames {
		if i%4 == 0 && i > 0 && len(temp) > 0 {
			claims = append(claims, temp[:len(temp)-2])
			temp = ""
		}
		temp += claimName + ", "
	}
	if len(temp) > 0 {
		claims = append(claims, temp[:len(temp)-2])
	}

	var PlayerBetsBy, PlayerBetsOn, PlayerBetsAmount string
	for _, bet := range r.Bets {
		PlayerBetsBy += bet.By.User.DisplayName() + "\n"
		PlayerBetsOn += bet.On.User.DisplayName() + "\n"
		PlayerBetsAmount += strconv.Itoa(bet.Amount) + "\n"
	}
	wonOutcome, wonAmount, lostOutcome, lostAmount, taxedOutcome, taxedAmount := g.resolvedOutcomeGroups(r)
	outcome := wonOutcome + lostOutcome + taxedOutcome
	outcomeAmount := wonAmount + lostAmount + taxedAmount
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "✨ Round Status ✨",
		Value: fmt.Sprintf("State: %s\n%s", g.RoundState(r), g.winnerStatus(r)),
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Players",
		Value:  placeholderValue(playerNames, "_No players yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Money",
		Value:  placeholderValue(playerMoney, "_No balances yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Bet%",
		Value:  placeholderValue(betPercentage, "_No bets yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Claims (100)",
		Value:  placeholderValue(strings.Join(claims, "\n"), "_No claims yet_"),
		Inline: false,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "✨ Round bets ✨",
		Value: "",
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "By",
		Value:  placeholderValue(PlayerBetsBy, "_No bets yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "On",
		Value:  placeholderValue(PlayerBetsOn, "_No bets yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Amount",
		Value:  placeholderValue(PlayerBetsAmount, "_No bets yet_"),
		Inline: true,
	})
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Outcome",
		Value:  placeholderValue(outcome, "_No outcomes yet_"),
		Inline: true,
	})
	if r.HasWinner() {
		outcomeCol, amountCol, deltaCol := g.resolvedOutcomeColumns(r)
		embed.Fields[len(embed.Fields)-1].Value = placeholderValue(outcomeCol, "_No outcomes yet_")
		embed.Fields[len(embed.Fields)-1].Inline = true
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Payout",
			Value:  placeholderValue(amountCol, "_No outcomes yet_"),
			Inline: true,
		})
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Delta",
			Value:  placeholderValue(deltaCol, "_No outcomes yet_"),
			Inline: true,
		})
	} else {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Amount",
			Value:  placeholderValue(outcomeAmount, "_No outcomes yet_"),
			Inline: true,
		})
	}
	return embed
}

func (g *game) SendMenu(s *discordgo.Session, i *discordgo.InteractionCreate, remove bool, winner bool, round int, messageID string) {
	targetRound := g.Round(round)
	actor := Player{User: i.Interaction.Member.User}
	var options []discordgo.SelectMenuOption
	for _, player := range g.menuPlayersForRound(actor, targetRound, remove, winner) {
		options = append(options, discordgo.SelectMenuOption{
			Label: player.User.DisplayName(),
			Value: player.ID(),
		})
	}
	if len(options) == 0 {
		discord.DeferEphemeralResponse(s, i)
		message := "No players available"
		if remove {
			message = "You don't have any bets to remove in this round."
		}
		if winner {
			message = "No eligible winners are available for this round."
		}
		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
		return
	}

	customID := fmt.Sprintf("menu_bet-place-%d-%s", round, messageID)
	content := "Place a Bet"
	placeholder := "Select a player to bet on"
	if remove {
		customID = fmt.Sprintf("menu_bet-remove-%d-%s", round, messageID)
		content = "Remove a Bet"
		placeholder = "Select a bet to remove"
	}
	if winner {
		customID = fmt.Sprintf("menu_bet-winner-%d-%s", round, messageID)
		content = "Pick a Winner"
		placeholder = "Select the winner"
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
							Placeholder: placeholder,
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

func (g *game) SendModal(s *discordgo.Session, i *discordgo.InteractionCreate, userID string, round int, messageID string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: fmt.Sprintf("modal_bet-%s-%d-%s", userID, round, messageID),
			Title:    "Place a Bet",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "amount",
							Label:       "Amount (10% tax threshold)",
							Style:       discordgo.TextInputShort,
							Placeholder: "10%, 25%, 50%, 100%, or exact amount",
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

var RoundMessageComponents = []discordgo.MessageComponent{
	&discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			makeButton(discordgo.PrimaryButton, "Claim!", "📈", "button_claim"),
			makeButton(discordgo.SecondaryButton, "Place Bet!", "💸", "button_bet"),
			makeButton(discordgo.SecondaryButton, "Remove Bet!", "💰", "button_bet-remove"),
			makeButton(discordgo.SuccessButton, "Set Winner!", "✨", "button_winner"),
		},
	},
}
