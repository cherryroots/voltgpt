package handler

import (
	"context"
	"fmt"
	"log"
	"strings"

	"voltgpt/internal/discord"
	"voltgpt/internal/memory"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

// userOption returns the value of the command's "user" option, or nil.
func userOption(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.User {
	for _, option := range i.ApplicationCommandData().Options {
		if option.Name == "user" {
			return option.UserValue(s)
		}
	}
	return nil
}

// userMemoryMessage renders a user's cached guild profile, falling back to
// their recent conversation notes. self selects second-person wording for
// the user viewing their own memory.
func userMemoryMessage(guildID string, user *discordgo.User, self bool) (string, error) {
	profile, err := memory.GetGuildUserProfile(guildID, user.ID)
	if err != nil {
		return "", err
	}
	if profile != nil {
		return truncateMessage(memory.RenderProfileMarkdown(profile, user.Username)), nil
	}

	notes, err := memory.GetRecentConversationNotesForUser(guildID, user.ID, 3)
	if err != nil {
		return "", err
	}

	var message string
	switch {
	case len(notes) == 0 && self:
		message = "I don't have any guild-scoped memory stored about you yet."
	case len(notes) == 0:
		message = fmt.Sprintf("No guild-scoped memory stored for %s.", user.Username)
	case self:
		message = "I don't have a cached profile for you yet.\n\n" + memory.RenderNotesMarkdown(notes)
	default:
		message = fmt.Sprintf("No cached profile for %s.\n\n%s", user.Username, memory.RenderNotesMarkdown(notes))
	}
	return truncateMessage(message), nil
}

func memoryAdminViewCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	if !requireAdmin(s, i) {
		return
	}

	user := userOption(s, i)
	if user == nil {
		respond(s, i, "Please select a user.")
		return
	}

	message, err := userMemoryMessage(i.GuildID, user, false)
	if err != nil {
		respond(s, i, fmt.Sprintf("Error: %v", err))
		return
	}
	respond(s, i, message)
}

func memoryAdminDeleteCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	if !requireAdmin(s, i) {
		return
	}

	var err error
	var message string
	if user := userOption(s, i); user != nil {
		err = memory.DeleteUserMemory(i.GuildID, user.ID)
		message = fmt.Sprintf("Deleted guild-scoped memory for %s.", user.Username)
	} else {
		count := memory.CountGuildNotes(i.GuildID)
		err = memory.DeleteAllGuildMemory(i.GuildID)
		message = fmt.Sprintf("Deleted %d note(s) of guild-scoped memory.", count)
	}
	if err != nil {
		respond(s, i, fmt.Sprintf("Error: %v", err))
		return
	}

	respond(s, i, message)
}

func memoryAdminDirtyCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	if !requireAdmin(s, i) {
		return
	}

	var err error
	var message string
	if user := userOption(s, i); user != nil {
		err = memory.MarkGuildUserProfileDirty(i.GuildID, user.ID, user.Username, user.GlobalName)
		message = fmt.Sprintf("Marked the cached guild-scoped profile dirty for %s.", user.Username)
	} else {
		var count int64
		count, err = memory.MarkAllGuildProfilesDirty(i.GuildID)
		message = fmt.Sprintf("Marked %d cached guild-scoped profile(s) dirty.", count)
	}
	if err != nil {
		respond(s, i, fmt.Sprintf("Error: %v", err))
		return
	}

	respond(s, i, message)
}

func memoryAdminDigestCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferResponse(s, i)

	if !requireAdmin(s, i) {
		return
	}

	if err := sendMemoryDigestPage(s, i, 1); err != nil {
		log.Println(err)
	}
}

func memorySelfCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	message, err := userMemoryMessage(i.GuildID, i.Interaction.Member.User, true)
	if err != nil {
		respond(s, i, fmt.Sprintf("Error: %v", err))
		return
	}
	respond(s, i, message)
}

func memorySetNameCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	var name string
	var targetUser *discordgo.User
	for _, option := range i.ApplicationCommandData().Options {
		switch option.Name {
		case "name":
			name = strings.TrimSpace(option.StringValue())
		case "user":
			targetUser = option.UserValue(s)
		}
	}

	// Non-admins cannot target other users
	if targetUser != nil && targetUser.ID != i.Interaction.Member.User.ID && !utility.IsAdmin(i.Interaction.Member.User.ID) {
		respond(s, i, "Only admins can set names for other users!")
		return
	}

	// Default to the invoking user
	if targetUser == nil {
		targetUser = i.Interaction.Member.User
	}

	// An empty name clears the preferred name
	if err := memory.SetPreferredName(targetUser.ID, targetUser.Username, name); err != nil {
		respond(s, i, fmt.Sprintf("Error: %v", err))
		return
	}

	if name == "" {
		respond(s, i, fmt.Sprintf("Cleared preferred name for %s.", targetUser.Username))
		return
	}
	respond(s, i, fmt.Sprintf("Set preferred name for %s to **%s**.", targetUser.Username, name))
}
