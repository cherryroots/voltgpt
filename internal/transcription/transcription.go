package transcription

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"voltgpt/internal/discord"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var database *sql.DB

var TranscriptCache = struct {
	sync.RWMutex
	t map[string]Transcript
}{
	t: make(map[string]Transcript),
}

type Transcript struct {
	ContentURL *url.URL             `json:"content_url"`
	Response   openai.AudioResponse `json:"response"`
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

func Init(db *sql.DB) {
	database = db
	loadFromDB()
}

func loadFromDB() {
	rows, err := database.Query("SELECT content_url, response_json FROM transcriptions")
	if err != nil {
		log.Fatalf("Failed to load transcriptions: %v", err)
	}
	defer rows.Close()

	TranscriptCache.Lock()
	defer TranscriptCache.Unlock()

	for rows.Next() {
		var contentURL, respJSON string
		if err := rows.Scan(&contentURL, &respJSON); err != nil {
			log.Printf("Failed to scan row: %v", err)
			continue
		}
		var resp openai.AudioResponse
		if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
			log.Printf("Failed to unmarshal transcript for %s: %v", contentURL, err)
			continue
		}
		parsedURL, err := url.Parse(contentURL)
		if err != nil {
			log.Printf("Failed to parse URL %s: %v", contentURL, err)
			continue
		}
		TranscriptCache.t[contentURL] = Transcript{ContentURL: parsedURL, Response: resp}
	}
}

func writeTranscriptToDB(contentURL string, resp openai.AudioResponse) {
	respJSON, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Failed to marshal transcript for DB write: %v", err)
		return
	}
	_, err = database.Exec(
		"INSERT OR REPLACE INTO transcriptions (content_url, response_json) VALUES (?, ?)",
		contentURL, string(respJSON),
	)
	if err != nil {
		log.Printf("Failed to write transcript to DB: %v", err)
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
	writeTranscriptToDB(transcript.String(), transcript.Response)
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
	_, videos, _, _ := utility.GetMessageMediaURL(message)

	var videoURLs []videoType
	var transcripts []Transcript

	for _, match := range result {
		if utility.MatchYTDLPWebsites(match[1]) {
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
			if strings.Contains(err.Error(), "Output file #0 does not contain any stream") {
				return Transcript{}, nil
			}
			errMsg := fmt.Errorf("Out: %s\nErr: %s", out, err)
			return Transcript{}, errMsg
		}
	} else {
		cmd := exec.Command("ffmpeg", "-i", videoURL, "-vn", "-acodec", "aac", "-b:a", "128k", fmt.Sprintf("%s/audio.m4a", dir))
		if out, err := cmd.CombinedOutput(); err != nil {
			if strings.Contains(err.Error(), "Output file #0 does not contain any stream") {
				return Transcript{}, nil
			}
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
