package handler

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"

	wave "voltgpt/internal/apis/wavespeed"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

const invalidImageURLMessage = "Please provide a valid image URL [jpg, jpeg, png]"

func drawCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferResponse(s, i)

	var prompt string
	var resolution string
	var imgs []*string
	var amount int
	syncMode := true
	for _, option := range i.ApplicationCommandData().Options {
		switch {
		case option.Name == "prompt":
			prompt = option.StringValue()
		case option.Name == "amount":
			amount = int(option.IntValue())
		case option.Name == "resolution":
			resolution = option.StringValue()
		case strings.HasPrefix(option.Name, "image"):
			if val, ok := option.Value.(string); ok {
				if att, exists := i.ApplicationCommandData().Resolved.Attachments[val]; exists {
					imgs = append(imgs, &att.URL)
				}
			}
		case option.Name == "urls":
			for _, url := range strings.Split(option.StringValue(), " ") {
				if !utility.IsWavespeedImageURL(url) {
					respond(s, i, invalidImageURLMessage)
					return
				}
				imgs = append(imgs, &url)
			}
		}
	}
	imgFilled := len(imgs) > 0

	if amount < 1 {
		amount = 2
	}

	if resolution == "" {
		resolution = "2048*2048"
	}

	// Validate and download the source images once, before spawning the
	// generation goroutines that all share them.
	var base64Images []*string
	if imgFilled {
		aspectRatio, err := utility.GetAspectRatio(*imgs[0])
		if err != nil {
			respond(s, i, err.Error())
			return
		}
		resolution = editResolution(aspectRatio)

		for _, img := range imgs {
			if !utility.IsWavespeedImageURL(*img) {
				respond(s, i, invalidImageURLMessage)
				return
			}
		}
		for _, img := range imgs {
			base64Image, err := utility.Base64ImageDownload(*img)
			if err != nil {
				respond(s, i, err.Error())
				return
			}
			base64Images = append(base64Images, &base64Image[0])
		}
	}

	var images []*discordgo.File
	var imgMu sync.Mutex

	var wg sync.WaitGroup
	for range amount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp *wave.WaveSpeedResponse
			var err error
			if imgFilled {
				resp, err = wave.Send(ctx, wave.SeedDreamEdit, wave.SeedDreamEditSubmissionRequest{
					Prompt:   prompt,
					Size:     &resolution,
					Images:   base64Images,
					SyncMode: &syncMode,
				})
			} else {
				resp, err = wave.Send(ctx, wave.SeedDream, wave.SeedDreamSubmissionRequest{
					Prompt:   prompt,
					Size:     &resolution,
					SyncMode: &syncMode,
				})
			}
			if err != nil {
				respondErr(s, i, err)
				return
			}
			image, err := wave.DownloadResult(resp)
			if err != nil {
				respondErr(s, i, err)
				return
			}
			imgMu.Lock()
			images = append(images, image...)
			imgMu.Unlock()
		}()
	}
	wg.Wait()
	mode := "drawing"
	if imgFilled {
		mode = "editing"
	}
	message := fmt.Sprintf("Prompt: %s\nResolution: %s\nMode: %s", prompt, resolution, mode)
	_, err := discord.SendFollowupFile(s, i, message, images)
	if err != nil {
		log.Println(err)
	}
}

// editResolution picks an output size matching the source image's aspect
// ratio, with the short side at 2048 and the long side capped at 4096.
func editResolution(aspectRatio float64) string {
	if aspectRatio >= 1 {
		H := 2048
		W := int(float64(H) * aspectRatio)
		if W > 4096 {
			W = 4096
			H = max(int(float64(W)/aspectRatio), 1024)
		}
		return fmt.Sprintf("%d*%d", W, H)
	}
	W := 2048
	H := int(float64(W) / aspectRatio)
	if H > 4096 {
		H = 4096
		W = max(int(float64(H)*aspectRatio), 1024)
	}
	return fmt.Sprintf("%d*%d", W, H)
}

func videoCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferResponse(s, i)

	var prompt string
	var negativePrompt string
	var img string
	var duration int
	var seed int
	for _, option := range i.ApplicationCommandData().Options {
		switch option.Name {
		case "prompt":
			prompt = option.StringValue()
		case "negative_prompt":
			negativePrompt = option.StringValue()
		case "image":
			if val, ok := option.Value.(string); ok {
				if att, exists := i.ApplicationCommandData().Resolved.Attachments[val]; exists {
					img = att.URL
				}
			}
		case "duration":
			duration = int(option.IntValue())
		case "seed":
			seed = int(option.IntValue())
		}
	}

	if prompt == "" && img == "" {
		respond(s, i, "Please provide a prompt or an image")
		return
	}

	if duration == 0 {
		duration = 5
	}

	if seed == 0 {
		seed = rand.Intn(2147483647)
	}
	i2vSize := "480p"
	t2vSize := "832*480"

	if duration != 5 && duration != 10 {
		respond(s, i, "Duration must be 5 or 10")
		return
	}

	var resp *wave.WaveSpeedResponse
	var err error
	if img != "" {
		if !utility.IsWavespeedImageURL(img) {
			respond(s, i, invalidImageURLMessage)
			return
		}
		base64Image, err := utility.Base64ImageDownload(img)
		if err != nil {
			respond(s, i, err.Error())
			return
		}
		resp, err = wave.Send(ctx, wave.WanImage2Video, wave.WanI2VSubmissionRequest{
			Prompt:         &prompt,
			NegativePrompt: &negativePrompt,
			Image:          base64Image[0],
			Duration:       &duration,
			Seed:           &seed,
			Resolution:     &i2vSize,
		})
		if err != nil {
			respondErr(s, i, err)
			return
		}
	} else {
		resp, err = wave.Send(ctx, wave.WanText2Video, wave.WanT2VSubmissionRequest{
			Prompt:         prompt,
			NegativePrompt: &negativePrompt,
			Duration:       &duration,
			Seed:           &seed,
			Size:           &t2vSize,
		})
		if err != nil {
			respondErr(s, i, err)
			return
		}
	}

	msg, err := discord.SendFollowup(s, i, "Processing...")
	if err != nil {
		log.Println(err)
		return
	}

	resp, err = wave.WaitForComplete(ctx, resp.Data.ID)
	if err != nil {
		respondErr(s, i, err)
		return
	}
	video, err := wave.DownloadResult(resp)
	if err != nil {
		respondErr(s, i, err)
		return
	}

	message := fmt.Sprintf("Gen time: %ds\nPrompt: %s\nNegative Prompt: %s", resp.Data.Timings.Inference/1000, prompt, negativePrompt)
	_, err = discord.EditFollowupFile(s, i, msg.ID, message, video)
	if err != nil {
		log.Println(err)
	}
}
