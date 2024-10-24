// Package utility is a utility package for utility functions.
package utility

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

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

func GetMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) []*discordgo.Message {
	messages, err := s.ChannelMessages(channelID, count, messageID, "", "")
	if err != nil {
		return nil
	}
	return messages
}

func GetChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	var messages []*discordgo.Message
	var lastMessageID string

	iterations := count / 100
	remainder := count % 100

	for range iterations {
		batch := GetMessagesBefore(s, channelID, 100, lastMessageID)
		lastMessageID = batch[len(batch)-1].ID
		messages = append(messages, batch...)
	}

	if remainder > 0 {
		batch := GetMessagesBefore(s, channelID, remainder, lastMessageID)
		messages = append(messages, batch...)
	}
	return messages
}

func GetAllChannelMessages(s *discordgo.Session, refMsg *discordgo.Message, channelID string, c chan []*discordgo.Message) {
	defer close(c)
	var lastMessageID string
	messagesRetrieved := 100
	count := 0

	for messagesRetrieved == 100 {
		batch := GetMessagesBefore(s, channelID, 100, lastMessageID)
		if len(batch) == 0 || batch == nil {
			log.Println("getAllChannelMessages: no messages retrieved")
			break
		}
		lastMessageID = batch[len(batch)-1].ID
		messagesRetrieved = len(batch)
		count += messagesRetrieved
		_, err := discord.EditMessage(s, refMsg, fmt.Sprintf("%s\n- Retrieved %d messages", refMsg.Content, count))
		if err != nil {
			log.Println(err)
		}
		c <- batch
	}

	log.Println("getAllChannelMessages: done")
}

func GetAllChannelThreadMessages(s *discordgo.Session, refMsg *discordgo.Message, channelID string, c chan []*discordgo.Message) {
	defer close(c)

	activeThreads, err := s.ThreadsActive(channelID)
	if err != nil {
		log.Println(err)
	}
	archivedThreads, err := s.ThreadsArchived(channelID, nil, 0)
	if err != nil {
		log.Println(err)
	}
	var threadChannels []*discordgo.Channel
	threadChannels = append(threadChannels, activeThreads.Threads...)
	threadChannels = append(threadChannels, archivedThreads.Threads...)

	for _, thread := range threadChannels {
		outputMessage := fmt.Sprintf("Fetching messages for thread: <#%s>", thread.ID)
		if len(refMsg.Content) > 1800 {
			refMsg, err = discord.SendMessage(s, refMsg, outputMessage)
		} else {
			refMsg, err = discord.EditMessage(s, refMsg, fmt.Sprintf("%s\n%s", refMsg.Content, outputMessage))
		}
		if err != nil {
			log.Println(err)
			return
		}

		var lastMessageID string
		messagesRetrieved := 100
		count := 0
		for messagesRetrieved == 100 {
			batch := GetMessagesBefore(s, thread.ID, 100, lastMessageID)
			if len(batch) == 0 || batch == nil {
				log.Println("getAllChannelMessages: no messages retrieved")
				break
			}
			lastMessageID = batch[len(batch)-1].ID
			messagesRetrieved = len(batch)
			count += messagesRetrieved
			_, err = discord.EditMessage(s, refMsg, fmt.Sprintf("%s\n- Retrieved %d messages", refMsg.Content, count))
			if err != nil {
				log.Println(err)
			}
			c <- batch
		}
		refMsg, err = discord.EditMessage(s, refMsg, fmt.Sprintf("%s\n- Retrieved %d messages", refMsg.Content, count))
		if err != nil {
			log.Println(err)
		}
	}

	log.Println("getAllChannelThreadMessages: done")
}

func GetMessageMediaURL(m *discordgo.Message) (images []string, videos []string) {
	seen := make(map[string]bool)
	var imgageURLs []string
	var videoURLs []string
	providerBlacklist := []string{"tenor"}

	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if IsImageURL(attachment.URL) {
				imgageURLs = append(imgageURLs, attachment.URL)
			}
			if IsVideoURL(attachment.URL) {
				videoURLs = append(videoURLs, attachment.URL)
			}
		}
	}
	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			if IsImageURL(embed.Thumbnail.URL) && MatchMultiple(embed.Provider.Name, providerBlacklist) {
				imgageURLs = append(imgageURLs, embed.Thumbnail.URL)
			}
			if IsImageURL(embed.Thumbnail.ProxyURL) {
				imgageURLs = append(imgageURLs, embed.Thumbnail.ProxyURL)
			}
		}
		if embed.Image != nil {
			if IsImageURL(embed.Image.URL) {
				imgageURLs = append(imgageURLs, embed.Image.URL)
			}
			if IsImageURL(embed.Image.ProxyURL) {
				imgageURLs = append(imgageURLs, embed.Image.ProxyURL)
			}
		}
		if embed.Video != nil {
			if IsVideoURL(embed.Video.URL) {
				videoURLs = append(videoURLs, embed.Video.URL)
			}
			if IsImageURL(embed.Video.URL) {
				imgageURLs = append(imgageURLs, embed.Video.URL)
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if IsImageURL(match[1]) {
			imgageURLs = append(imgageURLs, match[1])
		}
		if IsVideoURL(match[1]) {
			videoURLs = append(videoURLs, match[1])
		}
	}

	for _, u := range imgageURLs {
		checkURL := cleanURL(u)
		if !seen[checkURL] {
			seen[checkURL] = true
			images = append(images, u)
		}
	}

	for _, v := range videoURLs {
		checkURL := cleanURL(v)
		if !seen[checkURL] {
			seen[checkURL] = true
			videos = append(videos, v)
		}
	}

	return images, videos
}

func CheckCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
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
		embedStrings = append(embedStrings, fmt.Sprintf("Embed %d", i+1))
		if embed.Title != "" {
			embedStrings = append(embedStrings, fmt.Sprintf("Title: %s", embed.Title))
		}
		if embed.Description != "" {
			embedStrings = append(embedStrings, fmt.Sprintf("Description: %s", embed.Description))
		}
		for j, field := range embed.Fields {
			embedStrings = append(embedStrings, fmt.Sprintf("Field %d Name: %s", j+1, field.Name))
			embedStrings = append(embedStrings, fmt.Sprintf("Field %d Value: %s", j+1, field.Value))
		}
	}

	text = strings.Join(embedStrings, "\n") + "\n"

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
