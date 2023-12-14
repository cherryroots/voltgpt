package main

import (
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

		return
	}

	file, err := os.OpenFile("imagehashes.gob", os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(iHashes.m); err != nil {
		log.Fatal(err)
	}

	if err := file.Sync(); err != nil {
		log.Fatal(err)
	}
}

func readHashFromFile() {
	dataFile, err := os.Open("imagehashes.gob")
	if err != nil {
		log.Fatal(err)
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&iHashes.m); err != nil {
		log.Fatal(err)
	}
}

func hashAttachments(s *discordgo.Session, m *discordgo.Message, store bool) []string {
	// Get the data
	attachments := getAttachments(s, m)
	var hashes []string

	for _, attachment := range attachments {

		file, err := downloadOpenFile(attachment)
		if err != nil {
			continue
		}
		defer file.Close()

		img, _, err := image.Decode(file)
		if err != nil {
			continue
		}

		width, height := 8, 8
		hash, _ := goimagehash.ExtAverageHash(img, width, height)

		if store {
			writeHash(hash.ToString(), m)
		}

		hashes = append(hashes, hash.ToString())
	}

	return hashes
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

func checkInHashes(s *discordgo.Session, m *discordgo.Message) (bool, *discordgo.Message) {
	oldHashes := readAllHashes()
	newHashes := hashAttachments(s, m, false)
	for _, newHash := range newHashes {
		if checkHash(newHash) {
			return true, oldHashes[newHash]
		}
	}

	for oldHash := range readAllHashes() {
		for _, newHash := range newHashes {
			oldMsg := readHash(oldHash)
			hash1 := stringToHash(oldHash)
			hash2 := stringToHash(newHash)
			distance, _ := hash1.Distance(hash2)
			if distance < 3 {
				return true, oldMsg
			}
		}
	}

	return false, nil
}

func stringToHash(s string) *goimagehash.ExtImageHash {
	extHash, err := goimagehash.ExtImageHashFromString(s)
	if err != nil {
		log.Fatal(err)
	}
	return extHash
}

func downloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func downloadOpenFile(url string) (*os.File, error) {
	// Get the data
	f, err := os.CreateTemp("", "hash"+getFileExt(url))
	if err != nil {
		return nil, err
	}

	err = downloadFile(f.Name(), url)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(f.Name())
	if err != nil {
		return nil, err
	}

	return file, nil
}
