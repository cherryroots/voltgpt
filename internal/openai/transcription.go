package openai

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sync"

	"voltgpt/internal/discord"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

// TranscriptCache is a map of transcriptions.
var TranscriptCache = struct {
	sync.RWMutex
	t map[string]transcript
}{
	t: make(map[string]transcript),
}

// game is a struct representing the game.
type transcript struct {
	ContentURL *url.URL
	Response   openai.AudioResponse
}

func (t transcript) String() string {
	return t.ContentURL.String()
}

// WriteToFile writes the global variable TranscriptCache to a file named "transcripts.gob".
func WriteToFile() {
	if _, err := os.Stat("transcripts.gob"); os.IsNotExist(err) {
		file, err := os.Create("transcripts.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(TranscriptCache.t); err != nil {
			log.Fatal(err)
		}

		ReadFromFile()

		return
	}

	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(TranscriptCache.t); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("transcripts.gob", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Printf("OpenFile error: %v\n", err)
		return
	}
	defer file.Close()

	if _, err := buf.WriteTo(file); err != nil {
		log.Printf("WriteTo error: %v\n", err)
		return
	}
}

// ReadFromFile reads data from a file named "transcripts.gob" and decodes it into the global variable TranscriptCache.
func ReadFromFile() {
	dataFile, err := os.Open("transcripts.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&TranscriptCache.t); err != nil {
		log.Fatal(err)
	}
}

func readTranscriptCache(contentURL string, locking bool) transcript {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	return TranscriptCache.t[contentURL]
}

func writeTranscriptCache(transcript transcript, locking bool) {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	TranscriptCache.t[transcript.String()] = transcript
}

func checkTranscriptCache(contentURL string, locking bool) bool {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	_, ok := TranscriptCache.t[contentURL]

	return ok
}

// TotalTranscripts returns the total number of transcripts
func TotalTranscripts() int {
	TranscriptCache.RLock()
	defer TranscriptCache.RUnlock()
	return len(TranscriptCache.t)
}

// GetTranscriptFromMessage returns the transcript of the message
func GetTranscriptFromMessage(s *discordgo.Session, message *discordgo.Message) (string, error) {
	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(message.Content, -1)
	var transcript string
	for _, match := range result {
		if utility.MatchVideo(match[1]) {
			msg, err := discord.SendMessage(s, message, fmt.Sprintf("Gettings transcript for <%s>...", match[1]))
			if err != nil {
				return "", err
			}
			transcript, err = GetTranscriptFromVideo(match[1])
			if err != nil {
				log.Println(err)
			}

			s.ChannelMessageDelete(message.ChannelID, msg.ID)
		}
	}

	return transcript, nil
}

// GetTranscriptFromVideo returns the transcript of the video
func GetTranscriptFromVideo(videoURL string) (string, error) {
	// Check if the video is already in the cache
	if checkTranscriptCache(videoURL, false) {
		transcript := readTranscriptCache(videoURL, false)
		return transcript.Response.Text, nil
	}

	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a temp dir to store the video
	dir, err := os.MkdirTemp("", "video-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	// Download the audio using ytdlp
	cmd := exec.Command("yt-dlp", "-f", "bestaudio[ext=m4a]", "-x", "-o", fmt.Sprintf("%s/audio.%%(ext)s", dir), videoURL)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Get the video path
	videoPath := fmt.Sprintf("%s/audio.m4a", dir)

	// Create the whisper client
	client := openai.NewClient(openaiToken)

	// Upload the video to whisper
	resp, err := client.CreateTranscription(
		context.Background(),
		openai.AudioRequest{
			Model:    openai.Whisper1,
			FilePath: videoPath,
			Format:   openai.AudioResponseFormatSRT,
		},
	)
	if err != nil {
		return "", err
	}

	parsedURL, err := url.Parse(videoURL)
	if err != nil {
		log.Printf("Parse error, not writing to cache: %v\n", err)
	} else {
		writeTranscriptCache(transcript{ContentURL: parsedURL, Response: resp}, true)
	}

	text := fmt.Sprintf("Transcript for %s: %s", videoURL, resp.Text)

	return text, nil
}
