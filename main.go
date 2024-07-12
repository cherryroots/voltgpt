// Package main is the entry point for the application.
package main

import (
	"encoding/gob"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"

	"voltgpt/internal/config"
	"voltgpt/internal/gamble"
	"voltgpt/internal/handler"
	"voltgpt/internal/hasher"
	"voltgpt/internal/openai"
)

func init() {
	if err := godotenv.Load(); err != nil {
		log.Print("No .env file found")
	}

	gob.Register(&discordgo.ActionsRow{})
	gob.Register(&discordgo.Button{})
	gob.Register(&discordgo.SelectMenu{})
	gob.Register(&discordgo.TextInput{})

	hasher.ReadFromFile()
	gamble.ReadFromFile()
	openai.ReadFromFile()

	go func() {
		for {
			hasher.WriteToFile()
			gamble.WriteToFile()
			openai.WriteToFile()
			time.Sleep(1 * time.Minute)
		}
	}()
}

func main() {
	discordToken := os.Getenv("DISCORD_TOKEN")
	if discordToken == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}

	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatal("error creating Discord session,", err)
		return
	}

	dg.Identify.Intents = discordgo.IntentGuildMessages
	dg.ShouldReconnectOnError = true
	dg.ShouldRetryOnRateLimit = true

	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if h, ok := handler.Commands[i.ApplicationCommandData().Name]; ok {
				go h(s, i)
			}
		case discordgo.InteractionMessageComponent:
			split := strings.Split(i.MessageComponentData().CustomID, "-")
			if h, ok := handler.Components[split[0]]; ok {
				go h(s, i)
			}
		case discordgo.InteractionModalSubmit:
			split := strings.Split(i.ModalSubmitData().CustomID, "-")
			if h, ok := handler.Modals[split[0]]; ok {
				go h(s, i)
			}
		}
	})

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		go handler.HandleMessage(s, m)
	})

	dg.AddHandler(func(s *discordgo.Session, _ *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
		log.Printf("Hashes: %d", hasher.TotalHashes())
		log.Printf("Rounds: %d", gamble.Wheel.TotalRounds())
		log.Printf("Transcripts in cache: %d", openai.TotalTranscripts())
	})

	err = dg.Open()
	if err != nil {
		log.Fatal("error opening connection,", err)
		return
	}

	for _, guild := range dg.State.Guilds {
		log.Printf("Loading commands for %s", guild.ID)
		registerCommands := make([]*discordgo.ApplicationCommand, len(config.Commands))
		for i, command := range config.Commands {
			cmd, err := dg.ApplicationCommandCreate(dg.State.User.ID, guild.ID, command)
			if err != nil {
				log.Printf("could not create '%s' command: %v", command.Name, err)
			}
			registerCommands[i] = cmd
		}

		commands, err := dg.ApplicationCommands(dg.State.User.ID, guild.ID)
		if err != nil {
			log.Printf("could not get commands for guild %s: %v", guild.ID, err)
		}
		for _, command := range commands {
			if _, ok := handler.Commands[command.Name]; !ok {
				err := dg.ApplicationCommandDelete(dg.State.User.ID, guild.ID, command.ID)
				if err != nil {
					log.Printf("could not delete '%s' command: %v", command.Name, err)
				}
			}
		}
		log.Printf("Loaded %d commands for %s", len(registerCommands), guild.ID)
	}

	log.Println("Bot is now running. Press CTRL-C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	defer dg.Close()
	defer log.Print("Bot is shutting down.")
}
