// Package hasher is a utility package for hashing images.
package hasher

import (
	"bytes"
	"crypto/tls"
	"encoding/gob"
	"fmt"
	"image"

	// Import image decoder packages for their side effects: registering decoders for gif, jpeg, and png formats.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"

	"voltgpt/internal/utility"

	// Import image decoder packages for their side effects: registering decoder for webp formats.
	_ "golang.org/x/image/webp"

	ffmpeg "github.com/u2takey/ffmpeg-go"

	"github.com/bwmarrin/discordgo"
	"github.com/corona10/goimagehash"
)

var hashStore = struct {
	sync.RWMutex
	m map[string]*discordgo.Message
}{
	m: make(map[string]*discordgo.Message),
}

type hashResult struct {
	distance int
	message  *discordgo.Message
}

type HashOptions struct {
	Store            bool
	Threshold        int
	IgnoreExtensions []string
}

func WriteToFile() {
	hashStore.RLock()
	defer hashStore.RUnlock()

	if _, err := os.Stat("imagehashes.gob"); os.IsNotExist(err) {
		file, err := os.Create("imagehashes.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(hashStore.m); err != nil {
			log.Fatal(err)
		}

		ReadFromFile()

		return
	}

	buf := new(bytes.Buffer)

	if err := gob.NewEncoder(buf).Encode(hashStore.m); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("imagehashes.gob", os.O_WRONLY|os.O_TRUNC, 0o644)
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
	dataFile, err := os.Open("imagehashes.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&hashStore.m); err != nil {
		log.Fatal(err)
	}
}

func TotalHashes() int {
	hashStore.RLock()
	defer hashStore.RUnlock()
	return len(hashStore.m)
}

func HashAttachments(m *discordgo.Message, options HashOptions) ([]string, int) {
	images, videos := utility.GetMessageMediaURL(m)
	allAttachments := append(images, videos...)
	var hashes []string
	var count int

	for _, attachment := range allAttachments {
		if utility.HasExtension(attachment, options.IgnoreExtensions) {
			continue
		}
		var img image.Image

		if utility.IsVideoURL(attachment) {
			reader, err := readFrameAsJpeg(attachment, 10)
			if err != nil {
				log.Printf("ffmpegSS error: %v, url: %s\n", err, attachment)
				continue
			}
			img, _, err = image.Decode(reader)
			if err != nil {
				continue
			}
		} else if utility.IsImageURL(attachment) {
			buf, err := getFile(attachment)
			if err != nil {
				log.Printf("getFile error: %v, url: %s\n", err, attachment)
				continue
			}
			img, _, err = image.Decode(&buf)
			if err != nil {
				continue
			}
		} else {
			continue
		}

		width, height := 16, 16
		hash, err := goimagehash.ExtAverageHash(img, width, height)
		if err != nil {
			continue
		}

		hashString := hash.ToString()

		if options.Store && (!checkHash(hashString, true) || olderHash(hashString, m)) {
			writeHash(hashString, m, true)
			count++
			log.Printf("Stored hash: %s", hashString)
		}
		hashes = append(hashes, hashString)
	}

	return hashes, count
}

func readFrameAsJpeg(url string, frameNum int) (io.Reader, error) {
	outBuf := bytes.NewBuffer(nil)
	err := ffmpeg.Input(url).
		Filter("select", ffmpeg.Args{fmt.Sprintf("gte(n,%d)", frameNum)}).
		Output("pipe:", ffmpeg.KwArgs{"vframes": 1, "format": "image2", "vcodec": "mjpeg"}).
		WithOutput(outBuf).
		Silent(true).
		Run()
	if err != nil {
		return nil, err
	}
	return outBuf, nil
}

func readHash(hashString string, locking bool) *discordgo.Message {
	if locking {
		hashStore.Lock()
		defer hashStore.Unlock()
	}

	return hashStore.m[hashString]
}

func writeHash(hash string, message *discordgo.Message, locking bool) {
	if locking {
		hashStore.Lock()
		defer hashStore.Unlock()
	}

	hashStore.m[hash] = message
}

func checkHash(hash string, locking bool) bool {
	if locking {
		hashStore.Lock()
		defer hashStore.Unlock()
	}

	_, ok := hashStore.m[hash]

	return ok
}

func olderHash(hash string, message *discordgo.Message) bool {
	if checkHash(hash, true) {
		oldMessage := readHash(hash, true)
		if message.Timestamp.Before(oldMessage.Timestamp) {
			return true
		}
		return false
	}
	return true
}

func checkInHashes(m *discordgo.Message, options HashOptions) (bool, []hashResult) {
	var matchedMessages []hashResult
	messageHashes, _ := HashAttachments(m, options)
	hashStore.RLock()
	defer hashStore.RUnlock()
	for hashes := range hashStore.m {
		hash1 := stringToHash(hashes)
		for _, messageHash := range messageHashes {
			hash2 := stringToHash(messageHash)
			distance, _ := hash1.Distance(hash2)
			if distance <= options.Threshold {
				matchedMessages = append(matchedMessages, hashResult{distance, readHash(hashes, false)})
			}
		}
	}
	if len(matchedMessages) > 0 {
		sort.SliceStable(matchedMessages, func(i, j int) bool {
			return matchedMessages[i].message.Timestamp.Before(matchedMessages[j].message.Timestamp)
		})
		return true, matchedMessages
	}

	return false, nil
}

func stringToHash(s string) *goimagehash.ExtImageHash {
	extHash, err := goimagehash.ExtImageHashFromString(s)
	if err != nil {
		log.Printf("stringToHash error: %v\n", err)
		return new(goimagehash.ExtImageHash)
	}
	return extHash
}

func uniqueHashResults(results []hashResult) []hashResult {
	uniqueMap := make(map[string]hashResult)

	for _, result := range results {
		key := fmt.Sprintf("%d:%s", result.distance, result.message.ID)
		uniqueMap[key] = result
	}

	uniqueResults := make([]hashResult, 0, len(uniqueMap))
	for _, result := range uniqueMap {
		uniqueResults = append(uniqueResults, result)
	}

	return uniqueResults
}

func FindSnails(guildID string, message *discordgo.Message, options HashOptions) (string, []*discordgo.MessageEmbed) {
	isSnail, results := checkInHashes(message, options)
	var messageContent string
	var embeds []*discordgo.MessageEmbed
	if isSnail {
		for _, result := range uniqueHashResults(results) {
			if result.message.ID == message.ID {
				continue
			}
			if result.message.Timestamp.After(message.Timestamp) {
				continue
			}
			timestamp := result.message.Timestamp.UTC().Format("2006-01-02")
			embeds = append(embeds, utility.MessageToEmbeds(guildID, result.message, result.distance)...)
			messageContent += fmt.Sprintf("%dd: %s: Snail of %s! %s\n", result.distance, timestamp, result.message.Author.Username, utility.LinkFromIMessage(guildID, result.message))
		}
	}

	return messageContent, embeds
}

func getFile(url string) (bytes.Buffer, error) {
	var buf bytes.Buffer

	client := &http.Client{
		Transport: &http.Transport{
			TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper),
			DisableKeepAlives: true,
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return buf, err
	}
	if resp.StatusCode != http.StatusOK {
		return buf, fmt.Errorf("bad status: %s", resp.Status)
	}
	defer resp.Body.Close()

	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return buf, err
	}

	return buf, nil
}
