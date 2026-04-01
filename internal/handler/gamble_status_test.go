package handler

import (
	"testing"

	"voltgpt/internal/gamble"

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

func TestGambleStatusComponentsLockedResolvedRound(t *testing.T) {
	gamble.GameState.ResetWheel()
	gamble.GameState.AddRound()
	gamble.GameState.AddRound()
	gamble.GameState.Rounds[0].SetWinner(gamble.Player{User: &discordgo.User{ID: "1", Username: "winner", GlobalName: "Winner"}})

	components := gambleStatusComponentsLocked(1)
	if len(components) != 1 {
		t.Fatalf("len(components) = %d, want 1", len(components))
	}

	row, ok := components[0].(*discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("resolved components = %#v, want single button row", components[0])
	}
	button, ok := row.Components[0].(*discordgo.Button)
	if !ok || button.CustomID != "button_currentround" {
		t.Fatalf("resolved button = %#v, want button_currentround", row.Components[0])
	}
}

func TestGambleStatusComponentsLockedCurrentRound(t *testing.T) {
	gamble.GameState.ResetWheel()
	gamble.GameState.AddRound()

	components := gambleStatusComponentsLocked(1)
	row, ok := components[0].(*discordgo.ActionsRow)
	if !ok {
		t.Fatalf("components[0] = %#v, want action row", components[0])
	}
	if len(row.Components) != 4 {
		t.Fatalf("current round buttons = %d, want 4", len(row.Components))
	}
}
