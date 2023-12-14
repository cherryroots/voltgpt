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
	"sync"

	_ "golang.org/x/image/webp"

	"io"
	"net/http"
	"os"

	"github.com/bwmarrin/discordgo"
	"github.com/corona10/goimagehash"
)

var (
	iHashes = struct {
		sync.RWMutex
		m map[string]*discordgo.Message
	}{
		m: make(map[string]*discordgo.Message),
	}
)

func writeHashToFile() {
	iHashes.Lock()
	defer iHashes.Unlock()

	if _, err := os.Stat("imagehashes.gob"); os.IsNotExist(err) {
		file, err := os.Create("imagehashes.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(iHashes.m); err != nil {
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

	if err := gob.NewEncoder(buf).Encode(iHashes.m); err != nil {
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

	if err := gob.NewDecoder(dataFile).Decode(&iHashes.m); err != nil {
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

		width, height := 8, 8
		hash, _ := goimagehash.ExtAverageHash(img, width, height)

		if store && (!checkHash(hash.ToString()) || olderHash(hash.ToString(), m)) {
			writeHash(hash.ToString(), m)
			count++
			log.Printf("Stored hash: %s", hash.ToString())
		}
		hashes = append(hashes, hash.ToString())
	}

	return hashes, count
}

func readHash(hashString string) *discordgo.Message {
	iHashes.RLock()
	defer iHashes.RUnlock()

	return iHashes.m[hashString]
}

func readAllHashes() map[string]*discordgo.Message {
	iHashes.RLock()
	defer iHashes.RUnlock()

	return iHashes.m
}

func writeHash(hash string, message *discordgo.Message) {
	iHashes.Lock()
	defer iHashes.Unlock()

	iHashes.m[hash] = message
}

func checkHash(hash string) bool {
	iHashes.RLock()
	defer iHashes.RUnlock()

	_, ok := iHashes.m[hash]

	return ok
}

func olderHash(hash string, message *discordgo.Message) bool {
	iHashes.RLock()
	defer iHashes.RUnlock()

	if checkHash(hash) {
		// get the stored message for our hash
		oldMessage := readHash(hash)
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

func checkInHashes(s *discordgo.Session, m *discordgo.Message) (bool, []*discordgo.Message) {
	var oldMessages []*discordgo.Message
	newHashes, _ := hashAttachments(s, m, false)
	for oldHash := range readAllHashes() {
		hash1 := stringToHash(oldHash)
		for _, newHash := range newHashes {
			hash2 := stringToHash(newHash)
			distance, _ := hash1.Distance(hash2)
			if distance <= 6 {
				oldMessages = append(oldMessages, readHash(oldHash))
			}
		}
	}
	if len(oldMessages) > 0 {
		return true, oldMessages
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
			TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
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
