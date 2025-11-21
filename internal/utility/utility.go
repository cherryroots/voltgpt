// Package utility is a utility package for utility functions.
package utility

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	ffmpeg "github.com/u2takey/ffmpeg-go"

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
	if currentMessage == "" {
		return "", msg, fmt.Errorf("empty message")
	}

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

func GetAspectRatio(url string) (float64, error) {
	data, err := DownloadURL(url)
	if err != nil {
		return 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	return float64(img.Bounds().Dx()) / float64(img.Bounds().Dy()), nil
}

func Base64Image(url string) ([]string, error) {
	data, err := DownloadURL(url)
	if err != nil {
		return nil, err
	}
	return []string{base64.StdEncoding.EncodeToString(data)}, nil
}

func GifToBase64Images(url string) ([]string, error) {
	data, err := DownloadURL(url)
	if err != nil {
		return nil, err
	}

	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	targetInterval := 100.0 / 3.0

	var selectedFrames []int
	currentTime := 0.0
	for i := range len(g.Image) {
		if currentTime >= float64(len(selectedFrames))*targetInterval {
			selectedFrames = append(selectedFrames, i)
		}
		delay := g.Delay[i]
		if delay == 0 {
			delay = 10
		}
		currentTime += float64(delay)
	}

	if len(selectedFrames) == 0 {
		selectedFrames = []int{0}
	}

	b := [][9]*bytes.Buffer{}
	for i, frameIndex := range selectedFrames {
		if i%9 == 0 {
			b = append(b, [9]*bytes.Buffer{})
		}
		chunkIndex := len(b) - 1
		positionInChunk := i % 9

		b[chunkIndex][positionInChunk] = &bytes.Buffer{}
		png.Encode(b[chunkIndex][positionInChunk], g.Image[frameIndex])
	}

	var base64s []string
	for _, chunk := range b {
		var validBuffers []*bytes.Buffer
		for i := range 9 {
			if chunk[i] != nil && chunk[i].Len() > 0 {
				validBuffers = append(validBuffers, chunk[i])
			}
		}

		if len(validBuffers) > 0 {
			gridBuffer, err := CombinePNGsToGridSimple(validBuffers)
			if err != nil {
				return nil, err
			}
			base64s = append(base64s, base64.StdEncoding.EncodeToString(gridBuffer.Bytes()))
		}
	}

	return base64s, nil
}

func getVideoDuration(videoPath string) (float64, error) {
	stderrBuf := bytes.NewBuffer(nil)
	_ = ffmpeg.Input(videoPath).
		Output("/dev/null", ffmpeg.KwArgs{"f": "null"}).
		GlobalArgs("-hide_banner").
		WithErrorOutput(stderrBuf).
		Silent(true).
		Run()

	stderrOutput := stderrBuf.String()
	re := regexp.MustCompile(`Duration: (\d{1,2}):(\d{2}):(\d{2}\.\d{2})`)
	matches := re.FindStringSubmatch(stderrOutput)
	if len(matches) >= 4 {
		hours, _ := strconv.ParseFloat(matches[1], 64)
		minutes, _ := strconv.ParseFloat(matches[2], 64)
		seconds, _ := strconv.ParseFloat(matches[3], 64)
		return hours*3600 + minutes*60 + seconds, nil
	}

	return 0, fmt.Errorf("could not parse duration from ffmpeg output: %s", stderrOutput)
}

func extractVideoFrameAtTime(videoPath string, timestamp float64) (io.Reader, error) {
	outBuf := bytes.NewBuffer(nil)

	err := ffmpeg.Input(videoPath).
		Filter("select", ffmpeg.Args{fmt.Sprintf("gte(t,%.2f)", timestamp)}).
		Output("pipe:", ffmpeg.KwArgs{
			"vframes": 1,
			"format":  "image2",
			"vcodec":  "mjpeg",
			"q:v":     "2",
		}).
		GlobalArgs("-hide_banner", "-loglevel", "error").
		WithOutput(outBuf).
		Silent(true).
		Run()
	if err != nil {
		return nil, err
	}

	if outBuf.Len() == 0 {
		return nil, fmt.Errorf("no frame data extracted")
	}

	return outBuf, nil
}

func VideoToBase64Images(urlStr string) ([]string, error) {
	data, err := DownloadURL(urlStr)
	if err != nil {
		return nil, err
	}

	tempFile, err := os.CreateTemp("", "video_*.mp4")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = tempFile.Write(data)
	if err != nil {
		return nil, fmt.Errorf("failed to write video data: %w", err)
	}
	tempFile.Close()

	duration, err := getVideoDuration(tempFile.Name())
	if err != nil {
		log.Printf("Failed to get video duration, using fallback: %v", err)
		duration = 60.0
	}

	// Calculate dynamic frame count based on 3fps
	endPercentage := 0.98
	usableDuration := duration * endPercentage
	totalFrames := int(usableDuration * 3.0) // 3 frames per second

	// Ensure we have at least 1 frame and cap at reasonable maximum
	if totalFrames < 1 {
		totalFrames = 1
	} else if totalFrames > 910 { // Cap at 910 frames for very long videos, 10 images
		totalFrames = 910
	}

	var timestamps []float64
	for i := range totalFrames {
		timestamp := (float64(i) / float64(totalFrames-1)) * usableDuration
		timestamps = append(timestamps, timestamp)
	}

	log.Printf("Video duration: %.2f seconds, extracting %d frames at ~3fps", duration, totalFrames)

	type frameResult struct {
		index  int
		buffer *bytes.Buffer
		err    error
	}

	frameChan := make(chan frameResult, totalFrames)
	var wg sync.WaitGroup

	maxConcurrent := 10
	semaphore := make(chan struct{}, maxConcurrent)

	for i, timestamp := range timestamps {
		wg.Add(1)
		go func(index int, ts float64) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			frameReader, err := extractVideoFrameAtTime(tempFile.Name(), ts)
			if err != nil {
				frameChan <- frameResult{index: index, err: err}
				return
			}

			img, err := jpeg.Decode(frameReader)
			if err != nil {
				frameChan <- frameResult{index: index, err: err}
				return
			}

			buffer := &bytes.Buffer{}
			err = png.Encode(buffer, img)
			if err != nil {
				frameChan <- frameResult{index: index, err: err}
				return
			}

			frameChan <- frameResult{index: index, buffer: buffer}
		}(i, timestamp)
	}

	go func() {
		wg.Wait()
		close(frameChan)
	}()

	frames := make([]*bytes.Buffer, totalFrames)
	successCount := 0
	for result := range frameChan {
		if result.err != nil {
			log.Printf("Failed to extract frame %d: %v", result.index, result.err)
			continue
		}
		frames[result.index] = result.buffer
		successCount++
	}

	log.Printf("Successfully extracted %d out of %d frames", successCount, totalFrames)

	// Group frames into 9x9 grids (81 frames per grid)
	b := [][81]*bytes.Buffer{}
	for i, frame := range frames {
		if frame == nil {
			continue
		}

		if i%81 == 0 {
			b = append(b, [81]*bytes.Buffer{})
		}
		chunkIndex := len(b) - 1
		positionInChunk := i % 81
		b[chunkIndex][positionInChunk] = frame
	}

	var base64s []string
	for _, chunk := range b {
		var validBuffers []*bytes.Buffer
		for i := range 81 {
			if chunk[i] != nil && chunk[i].Len() > 0 {
				validBuffers = append(validBuffers, chunk[i])
			}
		}

		if len(validBuffers) > 0 {
			// Use CombinePNGsToGrid with 0.5 scaling (assuming typical frame size ~200px, scaled to ~100px)
			cellSize := 100 // 0.5 scaling from typical video frame size
			gridBuffer, err := CombinePNGsToGrid(validBuffers, cellSize)
			if err != nil {
				log.Printf("Failed to create grid: %v", err)
				continue
			}
			base64s = append(base64s, base64.StdEncoding.EncodeToString(gridBuffer.Bytes()))
		}
	}

	if len(base64s) == 0 {
		return nil, fmt.Errorf("failed to extract any frames from video")
	}

	return base64s, nil
}

func Base64ImageDownload(urlStr string) ([]string, error) {
	fileExt, err := UrlToExt(urlStr)
	if err != nil {
		return nil, err
	}

	var imageStrings []string

	switch fileExt {
	case ".jpg", ".jpeg":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/jpeg", b))
		}
	case ".png":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	case ".gif":
		b64, err := GifToBase64Images(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	case ".webp":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/webp", b))
		}
	case ".mp4", ".webm", ".mov":
		b64, err := VideoToBase64Images(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	default:
		return nil, fmt.Errorf("unknown file extension: %s", fileExt)
	}

	return imageStrings, nil
}

func MatchYTDLPWebsites(urlStr string) bool {
	if urlStr == "" {
		return false
	}
	// vimeo
	urlRegexes := []*regexp.Regexp{
		regexp.MustCompile(`^((?:https?:)?\/\/)?((?:www|m)\.)?((?:vimeo\.com))(\/)([\w\-]+)(\S+)?$`),
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

func BytesToPNG(data []byte) (*bytes.Buffer, error) {
	// 4 bytes for length header + data
	totalLen := len(data) + 4
	numPixels := int(math.Ceil(float64(totalLen) / 4.0))

	// Calculate dimensions for square-ish image
	width := int(math.Ceil(math.Sqrt(float64(numPixels))))
	height := int(math.Ceil(float64(numPixels) / float64(width)))

	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	// Write length header
	binary.BigEndian.PutUint32(img.Pix[0:4], uint32(len(data)))

	// Write data
	copy(img.Pix[4:], data)

	var buf bytes.Buffer
	// Use default compression
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return &buf, nil
}

func PNGToBytes(pngData []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}

	var pix []uint8
	switch i := img.(type) {
	case *image.NRGBA:
		pix = i.Pix
	case *image.RGBA:
		// Warning: this might have altered data due to premultiplication if it wasn't NRGBA
		pix = i.Pix
	default:
		// Convert to NRGBA
		bounds := img.Bounds()
		nrgba := image.NewNRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				nrgba.Set(x, y, img.At(x, y))
			}
		}
		pix = nrgba.Pix
	}

	if len(pix) < 4 {
		return nil, fmt.Errorf("image too small")
	}

	// Read length header
	dataLen := binary.BigEndian.Uint32(pix[0:4])

	if uint64(dataLen) > uint64(len(pix)-4) {
		return nil, fmt.Errorf("data length %d exceeds available pixel data", dataLen)
	}

	// Create a copy of the data to return
	data := make([]byte, dataLen)
	copy(data, pix[4:4+dataLen])

	return data, nil
}
