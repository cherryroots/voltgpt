package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

type requestContent struct {
	text string
	url  []string
}

func isAdmin(id string) bool {
	for _, admin := range admins {
		if admin == id {
			return true
		}
	}
	return false
}

func linkFromIMessage(i *discordgo.InteractionCreate, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, m.ChannelID, m.ID)
}

func splitTTS(message string, hd bool) []*discordgo.File {
	// Chunk up message into maxLength character chunks separated by newlines
	separator := "\n\n"
	maxLength := 4000
	var files []*discordgo.File
	var messageChunks []string

	// Split message into chunks of up to maxLength characters
	for len(message) > 0 {
		var chunk string
		if len(message) > maxLength {
			// Find the last separator before the maxLength character limit
			end := strings.LastIndex(message[:maxLength], separator)
			if end == -1 {
				// No separator found, so just cut at maxLength characters
				end = maxLength
			}
			chunk = message[:end]
			message = message[end:]
		} else {
			chunk = message
			message = ""
		}
		// Add chunk to messageChunks
		messageChunks = append(messageChunks, chunk)
	}

	var wg sync.WaitGroup
	filesChan := make(chan *discordgo.File, len(messageChunks))

	for count, chunk := range messageChunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			files := getTTSFile(chunk, fmt.Sprintf("%d", count+1), hd)
			for _, file := range files {
				filesChan <- file
			}
		}()
	}

	wg.Wait()
	close(filesChan)

	for file := range filesChan {
		files = append(files, file)
	}

	return files
}

func splitParagraph(message string) (string, string) {
	primarySeparator := "\n\n"
	secondarySeparator := "\n"
	var firstPart string
	var lastPart string

	// split the message into two parts based on the separator
	lastPrimaryIndex := strings.LastIndex(message, primarySeparator)
	lastSecondaryIndex := strings.LastIndex(message, secondarySeparator)
	// if there's a separator in the message
	if lastPrimaryIndex != -1 {
		// split the message into two parts
		firstPart = message[:lastPrimaryIndex]
		lastPart = message[lastPrimaryIndex+len(primarySeparator):]
	} else if lastSecondaryIndex != -1 {
		// split the message into two parts
		firstPart = message[:lastSecondaryIndex]
		lastPart = message[lastSecondaryIndex+len(secondarySeparator):]

	} else {
		// if there's no separator, return the whole message, and start the next one
		firstPart = message
		lastPart = ""
	}

	// if there's a code block in the first part that's not closed
	if strings.Count(firstPart, "```")%2 != 0 {
		lastCodeBlockIndex := strings.LastIndex(firstPart, "```")
		lastCodeBlock := firstPart[lastCodeBlockIndex:]
		// returns ```lang of the last code block in the first part
		languageCode := lastCodeBlock[:strings.Index(lastCodeBlock, "\n")]

		// ends the code block in the first message and starts a new code block in the next one
		firstPart = firstPart + "```"
		lastPart = languageCode + "\n" + lastPart
	}

	return firstPart, lastPart
}

func getMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) []*discordgo.Message {
	messages, err := s.ChannelMessages(channelID, count, messageID, "", "")
	if err != nil {
		return nil
	}
	return messages
}

func getChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	// if the count is over 100 split into multiple runs with the last message id being the before id argument
	var messages []*discordgo.Message
	var lastMessageID string

	// Calculate the number of full iterations and the remainder, dividing an int floors the result
	iterations := count / 100
	remainder := count % 100

	// Fetch full iterations of 100 messages
	for range iterations {
		batch := getMessagesBefore(s, channelID, 100, lastMessageID)
		lastMessageID = batch[len(batch)-1].ID
		messages = append(messages, batch...)
	}

	// Fetch the remainder of messages if there are any
	if remainder > 0 {
		batch := getMessagesBefore(s, channelID, remainder, lastMessageID)
		messages = append(messages, batch...)
	}
	return messages
}

func getAllChannelMessages(s *discordgo.Session, m *discordgo.Message, channelID string, c chan []*discordgo.Message) {
	defer close(c)
	var lastMessageID string
	messagesRetrieved := 100
	count := 0

	for messagesRetrieved == 100 {
		batch := getMessagesBefore(s, channelID, 100, lastMessageID)
		if len(batch) == 0 || batch == nil {
			log.Println("getAllChannelMessages: no messages retrieved")
			break
		}
		lastMessageID = batch[len(batch)-1].ID
		messagesRetrieved = len(batch)
		count += messagesRetrieved
		_, err := editMessage(s, m, fmt.Sprintf("Retrieved %d messages", count))
		if err != nil {
			log.Println(err)
		}
		c <- batch
	}

	log.Println("getAllChannelMessages: done")
}

func getMessageMediaURL(m *discordgo.Message) ([]string, []string) {
	seen := make(map[string]bool)
	var imgageURLs []string
	var videoURLs []string
	var images []string
	var videos []string

	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				imgageURLs = append(imgageURLs, attachment.URL)
			}
			if isVideoURL(attachment.URL) {
				videoURLs = append(videoURLs, attachment.URL)
			}
		}
	}
	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			if isImageURL(embed.Thumbnail.URL) {
				imgageURLs = append(imgageURLs, embed.Thumbnail.URL)
			}
		}
		if embed.Image != nil {
			if isImageURL(embed.Image.URL) {
				imgageURLs = append(imgageURLs, embed.Image.URL)
			}
		}
		if embed.Video != nil {
			if isVideoURL(embed.Video.URL) {
				videoURLs = append(videoURLs, embed.Video.URL)
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
			imgageURLs = append(imgageURLs, match[1])
		}
		if isVideoURL(match[1]) {
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

func checkCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
	}
	return nil
}

func cleanMessage(s *discordgo.Session, message *discordgo.Message) *discordgo.Message {
	botID := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botID)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func cleanName(name string) string {
	if len(name) > 64 {
		name = name[:64]
	}
	name = regexp.MustCompile("[^a-zA-Z0-9_-]").ReplaceAllString(name, "")
	return name
}

func cleanMessages(s *discordgo.Session, messages []*discordgo.Message) []*discordgo.Message {
	for i, message := range messages {
		messages[i] = cleanMessage(s, message)
	}
	return messages
}

func hasImageURL(m *discordgo.Message) bool {
	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				return true
			}
		}
	}
	for _, embed := range m.Embeds {
		// check if embed has image
		if embed.Thumbnail != nil {
			if isImageURL(embed.Thumbnail.URL) {
				return true
			}
		}
		if embed.Image != nil {
			if isImageURL(embed.Image.URL) {
				return true
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
			return true
		}
	}
	return false
}

func hasVideoURL(m *discordgo.Message) bool {
	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isVideoURL(attachment.URL) {
				return true
			}
		}
	}
	for _, embed := range m.Embeds {
		// check if embed has image
		if embed.Video != nil {
			if isVideoURL(embed.Video.URL) {
				return true
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
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
	fileExt = strings.TrimPrefix(fileExt, ".")
	fileExt = strings.ToLower(fileExt)
	return fileExt, nil
}

func isImageURL(urlStr string) bool {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case "jpg", "jpeg", "png", "gif", "webp":
		return true
	default:
		return false
	}
}

func isVideoURL(urlStr string) bool {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case "mp4", "webm", "mov":
		return true
	default:
		return false
	}
}

func mediaType(urlStr string) string {
	fileExt, err := urlToExt(urlStr)
	if err != nil {
		return ""
	}

	switch fileExt {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
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

func downloadURL(url string) ([]byte, error) {
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

func replaceMultiple(str string, oldStrings []string, newString string) string {
	for _, oldStr := range oldStrings {
		str = strings.ReplaceAll(str, oldStr, newString)
	}
	return str
}

func extractPairText(text string, lookup string) string {
	if !containsPair(text, lookup) {
		return ""
	}
	firstIndex := strings.Index(text, lookup)
	lastIndex := strings.LastIndex(text, lookup)
	return text[firstIndex : lastIndex+len(lookup)]
}

func containsPair(text string, lookup string) bool {
	return strings.Contains(text, lookup) && strings.Count(text, lookup)%2 == 0
}
