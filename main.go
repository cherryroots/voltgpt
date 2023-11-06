package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	discordgo "github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func init() {
	// godotenv get environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Print("No .env file found")
	}
}

func main() {
	discordToken := os.Getenv("DISCORD_TOKEN")
	if discordToken == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}

	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Panic("error creating Discord session,", err)
		return
	}

	dg.Identify.Intents = discordgo.IntentGuildMessages
	dg.ShouldReconnectOnError = true
	dg.ShouldRetryOnRateLimit = true

	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
				go h(s, i)
			}
		case discordgo.InteractionModalSubmit:
			prefix := strings.Split(i.ModalSubmitData().CustomID, "-")
			if h, ok := commandHandlers[prefix[0]]; ok {
				go h(s, i)
			}
		}
	})

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		go handleMessage(s, m)
	})

	dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})

	err = dg.Open()
	if err != nil {
		log.Panic("error opening connection,", err)
		return
	}

	for _, guild := range dg.State.Guilds {
		registerCommands := make([]*discordgo.ApplicationCommand, len(commands))
		for i, command := range commands {
			cmd, err := dg.ApplicationCommandCreate(dg.State.User.ID, guild.ID, command)
			if err != nil {
				log.Printf("could not create '%s' command: %v", command.Name, err)
			}
			registerCommands[i] = cmd
		}

		// delete commands that are not registered in commands.go
		commands, err := dg.ApplicationCommands(dg.State.User.ID, guild.ID)
		if err != nil {
			log.Printf("could not get commands for guild %s: %v", guild.ID, err)
		}
		for _, command := range commands {
			if _, ok := commandHandlers[command.Name]; !ok {
				err := dg.ApplicationCommandDelete(dg.State.User.ID, guild.ID, command.ID)
				if err != nil {
					log.Printf("could not delete '%s' command: %v", command.Name, err)
				}
			}
		}
	}

	log.Println("Bot is now running. Press CTRL-C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	defer dg.Close()
	defer log.Print("Bot is shutting down.")
}
