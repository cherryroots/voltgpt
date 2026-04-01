package handler

import (
	"fmt"
	"strconv"
	"strings"

	"voltgpt/internal/gamble"

	"github.com/bwmarrin/discordgo"
)

func gambleRoundNumberFromMessage(message *discordgo.Message) int {
	if message == nil || len(message.Embeds) == 0 {
		return 0
	}

	title := strings.TrimSpace(message.Embeds[0].Title)
	if !strings.HasPrefix(title, "Round ") {
		return 0
	}

	round, err := strconv.Atoi(strings.TrimPrefix(title, "Round "))
	if err != nil || round <= 0 {
		return 0
	}
	return round
}

func currentGambleRoundNumberLocked() int {
	if len(gamble.GameState.Rounds) == 0 {
		gamble.GameState.AddRound()
	}
	return gamble.GameState.CurrentRound().ID + 1
}

func gambleRoundIsCurrentLocked(round int) bool {
	if round <= 0 {
		return true
	}
	return round == currentGambleRoundNumberLocked()
}

func buildGambleStatusMessageEditLocked(channelID, messageID string, round int) *discordgo.MessageEdit {
	if round <= 0 {
		round = currentGambleRoundNumberLocked()
	}

	embed := gamble.GameState.StatusEmbed(gamble.GameState.Round(round))
	embeds := []*discordgo.MessageEmbed{&embed}
	components := gamble.RoundMessageComponents

	return &discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &embeds,
		Components: &components,
	}
}

func updateGambleStatusMessage(s *discordgo.Session, edit *discordgo.MessageEdit) error {
	if edit == nil {
		return nil
	}

	_, err := s.ChannelMessageEditComplex(edit)
	return err
}

func buildGambleMenuCustomID(action string, round int, messageID string) string {
	return fmt.Sprintf("menu_bet-%s-%d-%s", action, round, messageID)
}

func parseGambleMenuCustomID(customID string) (string, int, string) {
	parts := strings.Split(customID, "-")
	if len(parts) < 4 {
		return "", 0, ""
	}

	round, err := strconv.Atoi(parts[2])
	if err != nil {
		return parts[1], 0, parts[3]
	}
	return parts[1], round, parts[3]
}

func buildGambleModalCustomID(userID string, round int, messageID string) string {
	return fmt.Sprintf("modal_bet-%s-%d-%s", userID, round, messageID)
}

func parseGambleModalCustomID(customID string) (string, int, string) {
	parts := strings.Split(customID, "-")
	if len(parts) < 4 {
		return "", 0, ""
	}

	round, err := strconv.Atoi(parts[2])
	if err != nil {
		return parts[1], 0, parts[3]
	}
	return parts[1], round, parts[3]
}
