package utility

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"sync"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func VideoToBase64Images(urlStr string) ([]string, error) {
	data, err := DownloadBytes(urlStr)
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
