package handler

import (
	"context"
	"log"

	"voltgpt/internal/discord"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

type commandHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate)

// Commands is a map of command names and their corresponding functions.
var Commands = map[string]commandHandler{
	// generate.go
	"draw":  drawCommand,
	"video": videoCommand,
	// hash.go
	"hash_server": hashServerCommand,
	"CheckSnail":  checkSnailCommand,
	"Hash":        hashCommand,
	// wheel.go
	"wheel_status": wheelStatusCommand,
	"wheel_add":    wheelAddCommand,
	"insert_bet":   insertBetCommand,
	"reset_wheel":  resetWheelCommand,
	// memory.go
	"memory_admin_view":   memoryAdminViewCommand,
	"memory_admin_delete": memoryAdminDeleteCommand,
	"memory_admin_dirty":  memoryAdminDirtyCommand,
	"memory_admin_digest": memoryAdminDigestCommand,
	"memory_self":         memorySelfCommand,
	"memory_setname":      memorySetNameCommand,
	// reminders.go
	"reminders": remindersCommand,
}

// logCommand logs which command was invoked and by whom.
func logCommand(i *discordgo.InteractionCreate) {
	log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
}

// respond sends content as a followup message, logging any send failure.
func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := discord.SendFollowup(s, i, content); err != nil {
		log.Println(err)
	}
}

// respondErr logs err and reports it to the user as a followup message.
func respondErr(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	log.Println(err)
	respond(s, i, err.Error())
}

// requireAdmin reports whether the invoking user is an admin, telling them
// otherwise. Callers should return when it returns false.
func requireAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if utility.IsAdmin(i.Interaction.Member.User.ID) {
		return true
	}
	respond(s, i, "Only admins can use this command!")
	return false
}

// truncateMessage shortens content to fit Discord's 2000 character limit.
func truncateMessage(content string) string {
	if len(content) > 2000 {
		return content[:1997] + "..."
	}
	return content
}
