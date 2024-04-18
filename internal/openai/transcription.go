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
	t map[string]Transcript
}{
	t: make(map[string]Transcript),
}

// Transcript is a struct that contains the content URL and the response from the OpenAI API.
type Transcript struct {
	ContentURL *url.URL
	Response   openai.AudioResponse
}

type videoType struct {
	contentURL     string
	downloadMethod string
}

func (t Transcript) String() string {
	return t.ContentURL.String()
}

func (t Transcript) formatString() string {
	return fmt.Sprintf("Transcription for %s: %s", t.String(), t.Response.Text)
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

func readTranscriptCache(contentURL string, locking bool) Transcript {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	return TranscriptCache.t[contentURL]
}

func writeTranscriptCache(transcript Transcript, locking bool) {
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
func GetTranscriptFromMessage(s *discordgo.Session, message *discordgo.Message) (text string, err error) {
	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(message.Content, -1)
	_, videos := utility.GetMessageMediaURL(message)

	var videoURLs []videoType
	var transcripts []Transcript

	for _, match := range result {
		if utility.MatchVideoWebsites(match[1]) {
			videoURLs = append(videoURLs, videoType{contentURL: match[1], downloadMethod: "ytdlp"})
		}
	}

	for _, video := range videos {
		if utility.IsVideoURL(video) {
			videoURLs = append(videoURLs, videoType{contentURL: video, downloadMethod: "ffmpeg"})
		}
	}

	if len(videoURLs) == 0 {
		return "", nil
	}

	for count, videoURL := range videoURLs {
		if checkTranscriptCache(videoURL.contentURL, false) {
			transcript := readTranscriptCache(videoURL.contentURL, false)
			transcripts = append(transcripts, transcript)
			continue
		}

		msg, err := discord.SendMessage(s, message, fmt.Sprintf("Gettings transcript for video %d...", count+1))
		if err != nil {
			return "", err
		}

		transcript, err := GetTranscriptFromVideo(videoURL.contentURL, videoURL.downloadMethod)
		if err != nil {
			log.Println(err)
			continue
		}
		transcripts = append(transcripts, transcript)

		s.ChannelMessageDelete(message.ChannelID, msg.ID)
	}

	for _, transcript := range transcripts {
		if transcript.ContentURL == nil {
			continue
		}
		text += transcript.formatString() + "\n"
	}
	return text, nil
}

// GetTranscriptFromVideo returns the transcript of the video
func GetTranscriptFromVideo(videoURL string, downloadType string) (Transcript, error) {
	// Check if the video is already in the cache
	if checkTranscriptCache(videoURL, false) {
		transcript := readTranscriptCache(videoURL, false)
		return transcript, nil
	}

	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a temp dir to store the video
	dir, err := os.MkdirTemp("", "video-*")
	if err != nil {
		return Transcript{}, err
	}
	defer os.RemoveAll(dir)

	if downloadType == "ytdlp" {
		// Download the audio using ytdlp
		cmd := exec.Command("yt-dlp", "-f", "bestaudio[ext=m4a]", "-x", "-o", fmt.Sprintf("%s/audio.%%(ext)s", dir), videoURL)
		if err := cmd.Run(); err != nil {
			return Transcript{}, err
		}
	} else {
		// Download the audio using ffmpeg to .m4a
		cmd := exec.Command("ffmpeg", "-i", videoURL, "-vn", "-acodec", "copy", fmt.Sprintf("%s/audio.m4a", dir))
		if err := cmd.Run(); err != nil {
			return Transcript{}, err
		}
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
		return Transcript{}, err
	}

	parsedURL, err := url.Parse(videoURL)
	if err != nil {
		log.Printf("Parse error, not writing to cache: %v\n", err)
		return Transcript{}, err
	}

	writeTranscriptCache(Transcript{ContentURL: parsedURL, Response: resp}, true)

	return Transcript{ContentURL: parsedURL, Response: resp}, nil
}
