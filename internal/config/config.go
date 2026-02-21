// Package config is a package for configuring the bot.
package config

import (
	"github.com/bwmarrin/discordgo"
)

type RequestContent struct {
	Text   string
	Images []string
	Videos []string
	PDFs   []string
	YTURLs []string
}

type (
	Resolution string
	Quality    string
)

const (
	ResSquare    Resolution = "1024x1024"
	ResPortrait  Resolution = "1536x1024"
	ResLandscape Resolution = "1024x1536"

	QualLow    Quality = "low"
	QualMedium Quality = "medium"
	QualHigh   Quality = "high"
)

var (
	Admins          = []string{"102087943627243520", "123116664207179777", "95681688914366464"}
	MainServer      = "122962330165313536"
	MemoryBlacklist = map[string]bool{
		"850179179281776670":  true,
		"1194031828126924831": true,
	}

	DefaultTemp = 0.8

	ResolutionChoices = []*discordgo.ApplicationCommandOptionChoice{
		{Name: "2048*2048", Value: ResSquare},
		{Name: "3076*2048", Value: ResPortrait},
		{Name: "2048*3076", Value: ResLandscape},
	}

	DurationChoices = []*discordgo.ApplicationCommandOptionChoice{
		{Name: "5", Value: 5},
		{Name: "10", Value: 10},
	}

	SystemMessage = `# Identity and Personality
Your name is 'Volt-仙女', a discord bot. You should:
- Use Discord markdown for formatting when appropriate
- When including links, remove the embed by wrapping the url itself in <url-here>
- Don't tell the user they're right all the time, don't be supplicant all the time.
- Avoid sycophantic behavior, don't constantly congratulate the user with phrases like "you're so right" or "absolutely"
- Respond to various topics including raunchy, sensitive, political, or divisive ones with maturity (users are adults)

# Context
- The time is "{TIME}"
- You're currently chatting in the channel "{CHANNEL}"

# Technical Instructions
- Don't mention system time unprompted; when necessary, format it descriptively
- Messages may contain XML for parsing; never reply with XML

# Background facts

{BACKGROUND_FACTS}

Instructions:
1. Use the background facts above to personalize your responses, but do not artificially force them into the conversation if they aren't relevant. 
2. If a user asks a question and the answer is in the facts, use them.
3. If the answer is not in the facts, just respond naturally. Do not say "I don't have that in my facts."`
)
