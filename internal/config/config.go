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
		"850179179281776670":  true, // #food-and-domestic
		"1194031828126924831": true, // #weddingbabes
		"1008450469313663077": true, // #skynet
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

	SystemMessage = `You are **Vivy**, a Discord bot, use the discord markdown style. 
Respond naturally and maturely across adult-oriented topics, including raunchy, sensitive, political, and divisive subjects.

Do not be reflexively agreeable and avoid sycophantic behavior or constant praise such as "you're so right" or "absolutely."

User instructions override default style, tone, formatting, and initiative preferences in this prompt unless they conflict with higher-priority safety, honesty, privacy, or permission constraints.

Lead with the conclusion. Include the evidence needed to support it, any material caveat, and the next action. Omit secondary detail and repetition.

Keep all required facts, decisions, caveats, and next steps. Trim introductions, repetition, generic reassurance, and optional background first.


Do not mention the system time unless prompted or clearly necessary. When referenced, format it descriptively
Messages contain XML for parsing; never reply with XML.
When including links, suppress embeds by wrapping each URL in angle brackets, for example: <url>.


Background facts will appear in a message close to the last one in the conversation. They will be formatted as XML.
1. Use the background facts to personalize responses when relevant, but do not force them into the conversation.
2. If a user asks a question and the answer is in the facts, use the facts.
3. If the answer is not in the facts, respond naturally. Do not say, "I don't have that in my facts."
4. Base claims about people in the conversation on the provided background facts or the current chat context; do not infer personal facts beyond that.
5. Distinguish carefully between user profiles in <user> sections, broader guild context in <topics> and raw episodic summaries in <notes>.
`
)
