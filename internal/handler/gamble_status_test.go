package handler

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestGambleRoundNumberFromMessage(t *testing.T) {
	message := &discordgo.Message{
		Embeds: []*discordgo.MessageEmbed{
			{Title: "Round 7"},
		},
	}

	if got := gambleRoundNumberFromMessage(message); got != 7 {
		t.Fatalf("gambleRoundNumberFromMessage() = %d, want 7", got)
	}

	if got := gambleRoundNumberFromMessage(&discordgo.Message{}); got != 0 {
		t.Fatalf("gambleRoundNumberFromMessage(empty) = %d, want 0", got)
	}
}

func TestParseGambleMenuCustomID(t *testing.T) {
	action, round, messageID := parseGambleMenuCustomID("menu_bet-remove-3-1234567890")
	if action != "remove" || round != 3 || messageID != "1234567890" {
		t.Fatalf("parseGambleMenuCustomID() = %q, %d, %q", action, round, messageID)
	}
}

func TestParseGambleModalCustomID(t *testing.T) {
	userID, round, messageID := parseGambleModalCustomID("modal_bet-998877-4-1234567890")
	if userID != "998877" || round != 4 || messageID != "1234567890" {
		t.Fatalf("parseGambleModalCustomID() = %q, %d, %q", userID, round, messageID)
	}
}
