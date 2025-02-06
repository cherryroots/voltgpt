// Package utility is a utility package for utility functions.
package utility

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
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

func SplitParagraph(message string) (firstPart string, lastPart string) {
	primarySeparator := "\n\n"
	secondarySeparator := "\n"

	lastPrimaryIndex := strings.LastIndex(message, primarySeparator)
	lastSecondaryIndex := strings.LastIndex(message, secondarySeparator)
	if lastPrimaryIndex != -1 {
		firstPart = message[:lastPrimaryIndex]
		lastPart = message[lastPrimaryIndex+len(primarySeparator):]
	} else if lastSecondaryIndex != -1 {
		firstPart = message[:lastSecondaryIndex]
		lastPart = message[lastSecondaryIndex+len(secondarySeparator):]

	}
	if len(firstPart) > 1999 {
		log.Printf("Splitting forcibly: %d", len(firstPart))
		firstPart = message[:1999]
		lastPart = message[1999:]
	}

	if strings.Count(firstPart, "```")%2 != 0 {
		lastCodeBlockIndex := strings.LastIndex(firstPart, "```")
		lastCodeBlock := firstPart[lastCodeBlockIndex:]
		languageCode := lastCodeBlock[:strings.Index(lastCodeBlock, "\n")]

		firstPart = firstPart + "```"
		lastPart = languageCode + "\n" + lastPart
	}

	return firstPart, lastPart
}

func SplitSend(s *discordgo.Session, m *discordgo.Message, msg *discordgo.Message, currentMessage string) (string, *discordgo.Message, error) {
	if len(currentMessage) > 1750 {
		firstPart, lastPart := SplitParagraph(currentMessage)
		if lastPart == "" {
			lastPart = "..."
		}
		_, err := discord.EditMessage(s, msg, firstPart)
		if err != nil {
			return "", msg, err
		}
		msg, err = discord.SendMessageFile(s, msg, lastPart, nil)
		if err != nil {
			return "", msg, err
		}
		currentMessage = lastPart
	} else {
		_, err := discord.EditMessage(s, msg, currentMessage)
		if err != nil {
			return "", msg, err
		}
	}
	return currentMessage, msg, nil
}

func GetMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) ([]*discordgo.Message, error) {
	messages, err := s.ChannelMessages(channelID, count, messageID, "", "")
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func GetChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	var messages []*discordgo.Message
	var lastMessage *discordgo.Message

	iterations := count / 100
	remainder := count % 100

	for range iterations {
		batch, err := GetMessagesBefore(s, channelID, 100, lastMessage.ID)
		if err != nil {
			log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
		}
		lastMessage = batch[len(batch)-1]
		messages = append(messages, batch...)
	}

	if remainder > 0 {
		batch, err := GetMessagesBefore(s, channelID, remainder, lastMessage.ID)
		if err != nil {
			log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
		}
		messages = append(messages, batch...)
	}
	return messages
}

func GetAllServerMessages(s *discordgo.Session, statusMessage *discordgo.Message, channels []*discordgo.Channel, threads bool, endDate time.Time, c chan []*discordgo.Message) {
	defer close(c)

	var hashChannels []*discordgo.Channel

	if threads {
		discord.EditMessage(s, statusMessage, fmt.Sprintf("Fetching threads for %d channels...", len(channels)))
		for _, channel := range channels {
			activeThreads, err := s.ThreadsActive(channel.ID)
			if err != nil {
				log.Printf("Error getting active threads for channel %s: %s\n", channel.Name, err)
			}
			archivedThreads, err := s.ThreadsArchived(channel.ID, nil, 0)
			if err != nil {
				log.Printf("Error getting archived threads for channel %s: %s\n", channel.Name, err)
			}
			hashChannels = append(hashChannels, channel)
			hashChannels = append(hashChannels, activeThreads.Threads...)
			hashChannels = append(hashChannels, archivedThreads.Threads...)
		}
	} else {
		hashChannels = channels
	}

	for _, channel := range hashChannels {
		var outputMessage string
		var err error
		if channel.IsThread() {
			outputMessage = fmt.Sprintf("Fetching messages for thread: <#%s>", channel.ID)
		} else {
			outputMessage = fmt.Sprintf("Fetching messages for channel: <#%s>", channel.ID)
		}
		if len(statusMessage.Content) > 1800 {
			statusMessage, err = discord.SendMessage(s, statusMessage, outputMessage)
		} else {
			statusMessage, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n%s", statusMessage.Content, outputMessage))
		}
		if err != nil {
			log.Println(err)
			return
		}

		messagesRetrieved := 100
		count := 0
		lastMessage := &discordgo.Message{
			ID: "",
		}
		for messagesRetrieved == 100 {
			batch, err := GetMessagesBefore(s, channel.ID, 100, lastMessage.ID)
			if err != nil {
				log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
			}
			if len(batch) == 0 || batch == nil {
				log.Println("getAllChannelMessages: no messages retrieved")
				break
			}
			if batch[0].Timestamp.Before(endDate) {
				break
			}
			lastMessage = batch[len(batch)-1]
			messagesRetrieved = len(batch)
			count += messagesRetrieved
			_, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n- Retrieved %d messages", statusMessage.Content, count))
			if err != nil {
				log.Println(err)
			}
			c <- batch
		}
		statusMessage, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n- Retrieved %d messages", statusMessage.Content, count))
		if err != nil {
			log.Println(err)
		}
	}

	log.Println("getAllChannelThreadMessages: done")
}

func GetMessageMediaURL(m *discordgo.Message) (images []string, videos []string, pdfs []string) {
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
			if IsImageURL(embed.Thumbnail.URL) && MatchMultiple(provider, providerBlacklist) {
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
		url := match[1]
		if IsImageURL(url) {
			addIfNotSeen(url, &images)
		}
		if IsVideoURL(url) {
			addIfNotSeen(url, &videos)
		}
		if IsPDFURL(url) {
			addIfNotSeen(url, &pdfs)
		}
	}

	return images, videos, pdfs
}

func checkCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
	}
	return nil
}

func GetReferencedMessage(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message) *discordgo.Message {
	if message.ReferencedMessage != nil {
		return message.ReferencedMessage
	}

	if message.MessageReference != nil {
		cachedMessage := checkCache(cache, message.MessageReference.MessageID)
		if cachedMessage != nil {
			return cachedMessage
		}

		referencedMessage, _ := s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
		return referencedMessage
	}

	return nil
}

func CleanMessage(s *discordgo.Session, message *discordgo.Message) *discordgo.Message {
	botPing := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botPing)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func CleanName(name string) string {
	if len(name) > 64 {
		name = name[:64]
	}
	name = regexp.MustCompile("[^a-zA-Z0-9_-]").ReplaceAllString(name, "")
	return name
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
		bytes, err := DownloadURL(u)
		if err != nil {
			log.Printf("Error downloading attachment: %v", err)
			continue
		}

		ext, err := urlToExt(u)
		if err != nil {
			log.Printf("Error getting extension: %v", err)
			continue
		}
		idText := fmt.Sprintf("<attachmentID>%d</attachmentID>", i+1)
		typeText := fmt.Sprintf("<attachmentType>%s</attachmentType>", ext)
		textText := fmt.Sprintf("<attachmentText>\n%s\n</attachmentText>", string(bytes))
		text += fmt.Sprintf("<attachment>\n%s\n%s\n%s\n</attachment>", idText, typeText, textText)
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
		if IsImageURL(match[1]) {
			return true
		}
	}
	return false
}

func urlToExt(urlStr string) (string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	fileExt := filepath.Ext(parsedURL.Path)
	fileExt = strings.Split(fileExt, ":")[0]
	fileExt = strings.ToLower(fileExt)
	return fileExt, nil
}

func IsImageURL(urlStr string) bool {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func IsVideoURL(urlStr string) bool {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".mp4", ".webm", ".mov":
		return true
	default:
		return false
	}
}

func IsPDFURL(urlStr string) bool {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".pdf":
		return true
	default:
		return false
	}
}

func MediaType(urlStr string) string {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return ""
	}

	switch fileExt {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func cleanURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return parsedURL.Path
}

func DownloadURL(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func Base64Image(url string) string {
	data, err := DownloadURL(url)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func MatchVideoWebsites(urlStr string) bool {
	urlRegexes := []*regexp.Regexp{
		regexp.MustCompile(`^((?:https?:)?\/\/)?((?:www|m)\.)?((?:youtube\.com|youtu.be))(\/(?:[\w\-]+\?v=|embed\/|v\/)?)([\w\-]+)(\S+)?$`),
	}

	for _, r := range urlRegexes {
		if r.MatchString(urlStr) {
			return true
		}
	}
	return false
}

func HasExtension(URL string, extensions []string) bool {
	if extensions == nil {
		return false
	}
	for _, extension := range extensions {
		urlExt, _ := urlToExt(URL)
		if urlExt == extension {
			return true
		}
	}
	return false
}

func MatchMultiple(input string, matches []string) bool {
	for _, match := range matches {
		if input == match {
			return true
		}
	}
	return false
}

func ReplaceMultiple(str string, oldStrings []string, newString string) string {
	if len(oldStrings) == 0 {
		return str
	}
	if len(str) == 0 {
		return str
	}
	for _, oldStr := range oldStrings {
		str = strings.ReplaceAll(str, oldStr, newString)
	}
	return str
}

func ExtractPairText(text string, lookup string) string {
	if !containsPair(text, lookup) {
		return ""
	}
	firstIndex := strings.Index(text, lookup)
	lastIndex := strings.LastIndex(text, lookup)
	foundText := text[firstIndex : lastIndex+len(lookup)]
	return strings.ReplaceAll(foundText, lookup, "")
}

func containsPair(text string, lookup string) bool {
	return strings.Contains(text, lookup) && strings.Count(text, lookup)%2 == 0
}
