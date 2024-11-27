package transcription

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

var TranscriptCache = struct {
	sync.RWMutex
	t map[string]Transcript
}{
	t: make(map[string]Transcript),
}

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
	return fmt.Sprintf("<URL>%s</URL> <response>%s</response>", t.String(), t.Response.Text)
}

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

func ReadFromFile() {
	dataFile, err := os.Open("transcripts.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&TranscriptCache.t); err != nil {
		log.Fatalf("Decode error transcripts.gob: %v\n", err)
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

func TotalTranscripts() int {
	TranscriptCache.RLock()
	defer TranscriptCache.RUnlock()
	return len(TranscriptCache.t)
}

func GetTranscript(s *discordgo.Session, message *discordgo.Message) (text string) {
	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(message.Content, -1)
	_, videos, _ := utility.GetMessageMediaURL(message)

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
		return ""
	}

	for count, videoURL := range videoURLs {
		if checkTranscriptCache(videoURL.contentURL, false) {
			transcript := readTranscriptCache(videoURL.contentURL, false)
			transcripts = append(transcripts, transcript)
			continue
		}

		msg, err := discord.SendMessage(s, message, fmt.Sprintf("Gettings transcript for video %d...", count+1))
		if err != nil {
			log.Println(err)
			return ""
		}

		transcript, err := GetTranscriptFromVideo(videoURL.contentURL, videoURL.downloadMethod)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to get transcript for video %d: %s", count+1, err)
			errURL, err := url.Parse(videoURL.contentURL)
			if err != nil {
				log.Println(err)
				continue
			}
			transcript = Transcript{ContentURL: errURL, Response: openai.AudioResponse{Text: errMsg}}
		}
		transcripts = append(transcripts, transcript)

		s.ChannelMessageDelete(message.ChannelID, msg.ID)
	}

	for id, transcript := range transcripts {
		if transcript.ContentURL == nil {
			continue
		}
		text += fmt.Sprintf("<transcript %d>%s</transcript %d>\n", id+1, transcript.formatString(), id+1)
	}
	return fmt.Sprintf("<transcripts>%s</transcripts>", text)
}

func GetTranscriptFromVideo(videoURL string, downloadType string) (Transcript, error) {
	if checkTranscriptCache(videoURL, false) {
		transcript := readTranscriptCache(videoURL, false)
		return transcript, nil
	}

	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	dir, err := os.MkdirTemp("", "video-*")
	if err != nil {
		return Transcript{}, err
	}
	defer os.RemoveAll(dir)

	if downloadType == "ytdlp" {
		cmd := exec.Command("/home/bot/.pyenv/versions/3.12.2/bin/yt-dlp", "--username", "oauth2", "--password", "''", "-f", "bestaudio[ext=m4a]", "-x", "-o", fmt.Sprintf("%s/audio.%%(ext)s", dir), videoURL)
		if out, err := cmd.CombinedOutput(); err != nil {
			errMsg := fmt.Errorf("Out: %s\nErr: %s", out, err)
			return Transcript{}, errMsg
		}
	} else {
		cmd := exec.Command("ffmpeg", "-i", videoURL, "-vn", "-acodec", "aac", "-b:a", "128k", fmt.Sprintf("%s/audio.m4a", dir))
		if out, err := cmd.CombinedOutput(); err != nil {
			errMsg := fmt.Errorf("Out: %s\nErr: %s", out, err)
			return Transcript{}, errMsg
		}
	}

	videoPath := fmt.Sprintf("%s/audio.m4a", dir)

	client := openai.NewClient(openaiToken)

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
