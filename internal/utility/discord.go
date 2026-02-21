package utility

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/config"
)

func IsAdmin(id string) bool {
	for _, admin := range config.Admins {
		if admin == id {
			return true
		}
	}
	return false
}

func LinkFromIMessage(guildID string, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, m.ChannelID, m.ID)
}

func MessageToEmbeds(guildID string, m *discordgo.Message, distance int) []*discordgo.MessageEmbed {
	var embeds []*discordgo.MessageEmbed

	embeds = append(embeds, &discordgo.MessageEmbed{
		Title:       "Message link",
		Description: m.Content,
		URL:         LinkFromIMessage(guildID, m),
		Color:       0x2b2d31,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    m.Author.Username,
			IconURL: m.Author.AvatarURL(""),
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%dbit distance | %d attachments | %d embeds", distance, len(m.Attachments), len(m.Embeds)),
		},
		Timestamp: m.Timestamp.Format("2006-01-02T15:04:05Z"),
	})

	if len(m.Embeds) > 0 {
		embeds = append(embeds, m.Embeds...)
	}

	return embeds
}

func CleanMessage(s *discordgo.Session, message *discordgo.Message) *discordgo.Message {
	botPing := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botPing)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

// ResolveMentions replaces raw Discord mentions (<@ID>) with human-readable names.
// Uses GlobalName (display name) if available, falls back to Username.
func ResolveMentions(content string, mentions []*discordgo.User) string {
	for _, mention := range mentions {
		name := mention.GlobalName
		if name == "" {
			name = mention.Username
		}
		content = strings.ReplaceAll(content, "<@"+mention.ID+">", name)
	}
	return content
}

func CleanName(name string) string {
	if len(name) > 64 {
		name = name[:64]
	}
	name = regexp.MustCompile("[^a-zA-Z0-9_-]").ReplaceAllString(name, "")
	return name
}

func GetMessageMediaURL(m *discordgo.Message) (images []string, videos []string, pdfs []string, ytURLs []string) {
	seen := make(map[string]bool)
	providerBlacklist := []string{"tenor"}

	// Helper function to add URL if not seen
	addIfNotSeen := func(url string, collection *[]string) {
		checkURL := cleanURL(url)
		if !seen[checkURL] {
			seen[checkURL] = true
			*collection = append(*collection, url)
		}
	}

	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if IsImageURL(attachment.URL) {
				addIfNotSeen(attachment.URL, &images)
			}
			if IsVideoURL(attachment.URL) {
				addIfNotSeen(attachment.URL, &videos)
			}
		}
		if IsPDFURL(attachment.URL) {
			addIfNotSeen(attachment.URL, &pdfs)
		}
	}

	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			provider := ""
			if embed.Provider != nil {
				provider = embed.Provider.Name
			}
			if IsImageURL(embed.Thumbnail.URL) && !MatchMultiple(provider, providerBlacklist) {
				addIfNotSeen(embed.Thumbnail.URL, &images)
			}
			if IsImageURL(embed.Thumbnail.ProxyURL) {
				addIfNotSeen(embed.Thumbnail.ProxyURL, &images)
			}
		}
		if embed.Image != nil {
			if IsImageURL(embed.Image.URL) {
				addIfNotSeen(embed.Image.URL, &images)
			}
			if IsImageURL(embed.Image.ProxyURL) {
				addIfNotSeen(embed.Image.ProxyURL, &images)
			}
		}
		if embed.Video != nil {
			if IsVideoURL(embed.Video.URL) {
				addIfNotSeen(embed.Video.URL, &videos)
			}
			if IsImageURL(embed.Video.URL) {
				addIfNotSeen(embed.Video.URL, &images)
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		u := match[1]
		if IsImageURL(u) {
			addIfNotSeen(u, &images)
		}
		if IsVideoURL(u) {
			addIfNotSeen(u, &videos)
		}
		if IsPDFURL(u) {
			addIfNotSeen(u, &pdfs)
		}
		if IsYTURL(u) {
			addIfNotSeen(u, &ytURLs)
		}
	}

	return images, videos, pdfs, ytURLs
}

func HasImageURL(m *discordgo.Message) bool {
	for _, attachment := range m.Attachments {
		if IsImageURL(attachment.URL) {
			return true
		}
	}
	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			if IsImageURL(embed.Thumbnail.URL) {
				return true
			}
			if IsImageURL(embed.Thumbnail.ProxyURL) {
				return true
			}
		}
		if embed.Image != nil {
			if IsImageURL(embed.Image.URL) {
				return true
			}
			if IsImageURL(embed.Image.ProxyURL) {
				return true
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if IsImageURL(match[1]) {
			return true
		}
	}
	return false
}

func HasVideoURL(m *discordgo.Message) bool {
	for _, attachment := range m.Attachments {
		if IsVideoURL(attachment.URL) {
			return true
		}
	}
	for _, embed := range m.Embeds {
		if embed.Video != nil {
			if IsVideoURL(embed.Video.URL) {
				return true
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if IsVideoURL(match[1]) {
			return true
		}
	}
	return false
}

func AttachmentText(m *discordgo.Message) (text string) {
	var urls []string
	if len(m.Attachments) == 0 {
		return ""
	}
	for _, attachment := range m.Attachments {
		if strings.HasPrefix(attachment.ContentType, "text/") {
			urls = append(urls, attachment.URL)
		}
	}
	for i, u := range urls {
		byteData, err := DownloadURL(u)
		if err != nil {
			log.Printf("Error downloading attachment: %v", err)
			continue
		}

		ext, err := UrlToExt(u)
		if err != nil {
			log.Printf("Error getting extension: %v", err)
			continue
		}
		idText := fmt.Sprintf("<attachmentID>%d</attachmentID>", i+1)
		typeText := fmt.Sprintf("<attachmentType>%s</attachmentType>", ext)
		textText := fmt.Sprintf("<attachmentText>\n%s\n</attachmentText>", string(byteData))
		text += fmt.Sprintf("<attachment>\n%s\n%s\n%s\n</attachment>", idText, typeText, textText)
	}
	if text == "" {
		return ""
	}
	return fmt.Sprintf("<attachments>\n%s\n</attachments>\n", text)
}

func EmbedText(m *discordgo.Message) (text string) {
	var embedStrings []string
	if len(m.Embeds) == 0 {
		return ""
	}
	for i, embed := range m.Embeds {
		embedStrings = append(embedStrings, fmt.Sprintf("<embed id>%d</embed id>", i+1))
		if embed.Title != "" {
			embedStrings = append(embedStrings, fmt.Sprintf("<title>%s</title>", embed.Title))
		}
		if embed.Description != "" {
			embedStrings = append(embedStrings, fmt.Sprintf("<description>%s</description>", embed.Description))
		}
		for j, field := range embed.Fields {
			embedStrings = append(embedStrings, fmt.Sprintf("<field id>%d</field id><field name>%s</field name><field value>%s</field value>", j+1, field.Name, field.Value))
		}
	}

	text = "<embed>" + strings.Join(embedStrings, "\n") + "</embed>\n"

	return fmt.Sprintf("<embeds>%s</embeds>\n", text)
}
