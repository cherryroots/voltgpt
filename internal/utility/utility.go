// Package utility is a utility package for utility functions.
package utility

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"log"
	"math"
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

	lastPrimaryIndex := strings.LastIndex(message[:min(1990, len(message))], primarySeparator)
	lastSecondaryIndex := strings.LastIndex(message[:min(1990, len(message))], secondarySeparator)
	if lastPrimaryIndex != -1 {
		firstPart = message[:lastPrimaryIndex]
		lastPart = message[lastPrimaryIndex+len(primarySeparator):]
	} else if lastSecondaryIndex != -1 {
		firstPart = message[:lastSecondaryIndex]
		lastPart = message[lastSecondaryIndex+len(secondarySeparator):]

	}
	if len(firstPart) > 1990 {
		log.Printf("Splitting forcibly: %d", len(firstPart))
		firstPart = message[:1990]
		lastPart = message[1990:]
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

// SplitMessageSlices splits a message into slices based on Discord's character limits
// Returns a slice of message parts that can be sent sequentially
func SplitMessageSlices(message string) []string {
	if len(message) <= 1800 {
		return []string{message}
	}

	var parts []string
	remaining := message

	for len(remaining) > 1800 {
		primarySeparator := "\n\n"
		secondarySeparator := "\n"
		maxLength := min(1900, len(remaining)) // Safety buffer

		// Find the best split point
		lastPrimaryIndex := strings.LastIndex(remaining[:maxLength], primarySeparator)
		lastSecondaryIndex := strings.LastIndex(remaining[:maxLength], secondarySeparator)

		var splitIndex int
		var separatorLen int

		if lastPrimaryIndex != -1 && lastPrimaryIndex > len(remaining)/4 {
			// Use primary separator if it's not too close to the beginning
			splitIndex = lastPrimaryIndex
			separatorLen = len(primarySeparator)
		} else if lastSecondaryIndex != -1 && lastSecondaryIndex > len(remaining)/4 {
			// Use secondary separator if it's not too close to the beginning
			splitIndex = lastSecondaryIndex
			separatorLen = len(secondarySeparator)
		} else {
			// Force split at safe length
			splitIndex = 1900
			separatorLen = 0
			log.Printf("Force splitting message at %d characters", splitIndex)
		}

		part := remaining[:splitIndex]

		// Handle code blocks to prevent breaking them
		if strings.Count(part, "```")%2 != 0 {
			lastCodeBlockIndex := strings.LastIndex(part, "```")
			if lastCodeBlockIndex != -1 {
				lastCodeBlock := part[lastCodeBlockIndex:]
				languageCode := ""
				if newlineIndex := strings.Index(lastCodeBlock, "\n"); newlineIndex != -1 {
					languageCode = lastCodeBlock[:newlineIndex]
				}
				part = part + "```"
				remaining = languageCode + "\n" + remaining[splitIndex+separatorLen:]
			} else {
				remaining = remaining[splitIndex+separatorLen:]
			}
		} else {
			remaining = remaining[splitIndex+separatorLen:]
		}

		parts = append(parts, strings.TrimSpace(part))
	}

	if len(remaining) > 0 {
		parts = append(parts, strings.TrimSpace(remaining))
	}

	return parts
}

func SplitSend(s *discordgo.Session, msg *discordgo.Message, currentMessage string) (string, *discordgo.Message, error) {
	if len(currentMessage) > 1800 {
		firstPart, lastPart := SplitParagraph(currentMessage)
		if lastPart == "" {
			lastPart = "..."
		}
		_, err := discord.EditMessage(s, msg, firstPart)
		if err != nil {
			return "", msg, err
		}
		msg, err = discord.SendMessage(s, msg, lastPart)
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

// SliceSend sends message slices sequentially, editing the first message and sending new ones for subsequent parts
func SliceSend(s *discordgo.Session, msg *discordgo.Message, messageSlices []string, currentSliceIndex int) (*discordgo.Message, error) {
	if len(messageSlices) == 0 {
		return msg, nil
	}

	// Edit the first message with the current slice
	if currentSliceIndex < len(messageSlices) {
		_, err := discord.EditMessage(s, msg, messageSlices[currentSliceIndex])
		if err != nil {
			return msg, err
		}
	}

	// Send additional messages for remaining slices
	for i := currentSliceIndex + 1; i < len(messageSlices); i++ {
		newMsg, err := discord.SendMessage(s, msg, messageSlices[i])
		if err != nil {
			return msg, err
		}
		msg = newMsg // Update to the latest message for potential further sends
	}

	return msg, nil
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

		ext, err := UrlToExt(u)
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

func UrlToExt(urlStr string) (string, error) {
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
	fileExt, err := UrlToExt(urlStr)
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
	fileExt, err := UrlToExt(urlStr)
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
	fileExt, err := UrlToExt(urlStr)
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
	fileExt, err := UrlToExt(urlStr)
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

func Base64Image(url string) (string, error) {
	data, err := DownloadURL(url)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func GifToBase64Image(url string) (string, error) {
	data, err := DownloadURL(url)
	if err != nil {
		return "", err
	}

	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	b := []*bytes.Buffer{}
	for _, frame := range g.Image {
		b = append(b, &bytes.Buffer{})
		png.Encode(b[len(b)-1], frame)
	}

	gridBuffer, err := CombinePNGsToGridSimple(b)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(gridBuffer.Bytes()), nil
}

func Base64ImageDownload(urlStr string) (string, error) {
	fileExt, err := UrlToExt(urlStr)
	if err != nil {
		return "", err
	}

	switch fileExt {
	case ".jpg", ".jpeg":
		base64, err := Base64Image(urlStr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("data:%s;base64,%s", "image/jpeg", base64), nil
	case ".png":
		base64, err := Base64Image(urlStr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("data:%s;base64,%s", "image/png", base64), nil
	case ".gif":
		base64, err := GifToBase64Image(urlStr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("data:%s;base64,%s", "image/png", base64), nil
	case ".webp":
		base64, err := Base64Image(urlStr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("data:%s;base64,%s", "image/webp", base64), nil
	default:
		return "", fmt.Errorf("unknown file extension: %s", fileExt)
	}
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
		urlExt, _ := UrlToExt(URL)
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

func CombinePNGsToGrid(pngBuffers []*bytes.Buffer, cellSize int) (*bytes.Buffer, error) {
	if len(pngBuffers) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	// Calculate grid dimensions (square grid)
	gridSize := int(math.Ceil(math.Sqrt(float64(len(pngBuffers)))))

	// Decode all PNG images
	images := make([]image.Image, len(pngBuffers))
	for i, buf := range pngBuffers {
		img, err := png.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PNG %d: %w", i, err)
		}
		images[i] = img
	}

	// Create output image
	outputWidth := gridSize * cellSize
	outputHeight := gridSize * cellSize
	outputImg := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))

	// Fill with white background
	for y := 0; y < outputHeight; y++ {
		for x := 0; x < outputWidth; x++ {
			outputImg.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}

	// Draw images into grid
	for i, img := range images {
		row := i / gridSize
		col := i % gridSize

		// Calculate position in grid
		startX := col * cellSize
		startY := row * cellSize

		// Get source image bounds
		srcBounds := img.Bounds()
		srcWidth := srcBounds.Dx()
		srcHeight := srcBounds.Dy()

		// Calculate scaling to fit in cell while maintaining aspect ratio
		scaleX := float64(cellSize) / float64(srcWidth)
		scaleY := float64(cellSize) / float64(srcHeight)
		scale := math.Min(scaleX, scaleY)

		newWidth := int(float64(srcWidth) * scale)
		newHeight := int(float64(srcHeight) * scale)

		// Center the image in the cell
		offsetX := (cellSize - newWidth) / 2
		offsetY := (cellSize - newHeight) / 2

		// Draw the scaled image
		for y := 0; y < newHeight; y++ {
			for x := 0; x < newWidth; x++ {
				// Calculate source pixel
				srcX := int(float64(x) / scale)
				srcY := int(float64(y) / scale)

				if srcX < srcWidth && srcY < srcHeight {
					srcColor := img.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY)
					outputImg.Set(startX+offsetX+x, startY+offsetY+y, srcColor)
				}
			}
		}
	}

	// Encode to PNG
	var outputBuffer bytes.Buffer
	err := png.Encode(&outputBuffer, outputImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode output PNG: %w", err)
	}

	return &outputBuffer, nil
}

func CombinePNGsToGridSimple(pngBuffers []*bytes.Buffer) (*bytes.Buffer, error) {
	if len(pngBuffers) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	// Decode first image to get dimensions
	firstImg, err := png.Decode(bytes.NewReader(pngBuffers[0].Bytes()))
	if err != nil {
		return nil, fmt.Errorf("failed to decode first PNG: %w", err)
	}

	imgBounds := firstImg.Bounds()
	imgWidth := imgBounds.Dx()
	imgHeight := imgBounds.Dy()

	// Calculate grid dimensions
	gridSize := int(math.Ceil(math.Sqrt(float64(len(pngBuffers)))))

	// Create output image
	outputWidth := gridSize * imgWidth
	outputHeight := gridSize * imgHeight
	outputImg := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))

	// Process all images
	for i, buf := range pngBuffers {
		img, err := png.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PNG %d: %w", i, err)
		}

		row := i / gridSize
		col := i % gridSize

		startX := col * imgWidth
		startY := row * imgHeight

		// Copy image pixels
		bounds := img.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				outputImg.Set(startX+x-bounds.Min.X, startY+y-bounds.Min.Y, img.At(x, y))
			}
		}
	}

	// Encode to PNG
	var outputBuffer bytes.Buffer
	err = png.Encode(&outputBuffer, outputImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode output PNG: %w", err)
	}

	return &outputBuffer, nil
}
