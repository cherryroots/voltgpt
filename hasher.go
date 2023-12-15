package main

import (
	"bytes"
	"crypto/tls"
	"encoding/gob"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"sort"
	"sync"

	_ "golang.org/x/image/webp"

	"io"
	"net/http"
	"os"

	"github.com/bwmarrin/discordgo"
	"github.com/corona10/goimagehash"
)

var (
	hashStore = struct {
		sync.RWMutex
		m map[string]*discordgo.Message
	}{
		m: make(map[string]*discordgo.Message),
	}
)

type hashResult struct {
	distance int
	message  *discordgo.Message
}

func writeHashToFile() {
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

		readHashFromFile()

		return
	}

	buf := new(bytes.Buffer)

	gob.Register(&discordgo.ActionsRow{})
	gob.Register(&discordgo.Button{})
	gob.Register(&discordgo.SelectMenu{})
	gob.Register(&discordgo.TextInput{})

	if err := gob.NewEncoder(buf).Encode(hashStore.m); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("imagehashes.gob", os.O_WRONLY|os.O_TRUNC, 0644)
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

func readHashFromFile() {
	dataFile, err := os.Open("imagehashes.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	gob.Register(&discordgo.ActionsRow{})
	gob.Register(&discordgo.Button{})
	gob.Register(&discordgo.SelectMenu{})
	gob.Register(&discordgo.TextInput{})

	if err := gob.NewDecoder(dataFile).Decode(&hashStore.m); err != nil {
		log.Fatal(err)
	}
}

func hashAttachments(s *discordgo.Session, m *discordgo.Message, store bool) ([]string, int) {
	// Get the data
	images := getMessageImages(s, m)
	var hashes []string
	var count int

	for _, attachment := range images {

		buf, err := getFile(attachment)
		if err != nil {
			continue
		}

		img, _, err := image.Decode(&buf)
		if err != nil {
			continue
		}

		width, height := 16, 16
		hash, err := goimagehash.ExtAverageHash(img, width, height)
		if err != nil {
			continue
		}

		if store && (!checkHash(hash.ToString(), true) || olderHash(hash.ToString(), m)) {
			writeHash(hash.ToString(), m, true)
			count++
			log.Printf("Stored hash: %s", hash.ToString())
		}
		hashes = append(hashes, hash.ToString())
	}

	return hashes, count
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
		// get the stored message for our hash
		oldMessage := readHash(hash, true)
		// if the message we're parsing is older than the stored message
		if message.Timestamp.Before(oldMessage.Timestamp) {
			return true
		} else {
			return false
		}
	}
	// return true if the hash doesn't exist
	return true
}

func checkInHashes(s *discordgo.Session, m *discordgo.Message) (bool, []hashResult) {
	var matchedMessages []hashResult
	messageHashes, _ := hashAttachments(s, m, false)
	hashStore.RLock()
	defer hashStore.RUnlock()
	// copy the map
	for hashes := range hashStore.m {
		hash1 := stringToHash(hashes)
		for _, messageHash := range messageHashes {
			hash2 := stringToHash(messageHash)
			distance, _ := hash1.Distance(hash2)
			if distance <= 10 {
				matchedMessages = append(matchedMessages, hashResult{distance, readHash(hashes, false)})
			}
		}
	}
	if len(matchedMessages) > 0 {
		sort.SliceStable(matchedMessages, func(i, j int) bool {
			return matchedMessages[i].distance < matchedMessages[j].distance
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
	defer resp.Body.Close()

	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return buf, err
	}

	return buf, nil
}
