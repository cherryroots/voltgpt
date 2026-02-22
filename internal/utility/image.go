package utility

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
)

func Base64ImageDownload(urlStr string) ([]string, error) {
	fileExt, err := URLToExt(urlStr)
	if err != nil {
		return nil, err
	}

	var imageStrings []string

	switch fileExt {
	case ".jpg", ".jpeg":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/jpeg", b))
		}
	case ".png":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	case ".gif":
		b64, err := GifToBase64Images(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	case ".webp":
		b64, err := Base64Image(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/webp", b))
		}
	case ".mp4", ".webm", ".mov":
		b64, err := VideoToBase64Images(urlStr)
		if err != nil {
			return nil, err
		}
		for _, b := range b64 {
			imageStrings = append(imageStrings, fmt.Sprintf("data:%s;base64,%s", "image/png", b))
		}
	default:
		return nil, fmt.Errorf("unknown file extension: %s", fileExt)
	}

	return imageStrings, nil
}

func GetAspectRatio(url string) (float64, error) {
	data, err := DownloadBytes(url)
	if err != nil {
		return 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	return float64(img.Bounds().Dx()) / float64(img.Bounds().Dy()), nil
}

func Base64Image(url string) ([]string, error) {
	data, err := DownloadBytes(url)
	if err != nil {
		return nil, err
	}
	return []string{base64.StdEncoding.EncodeToString(data)}, nil
}

func GifToBase64Images(url string) ([]string, error) {
	data, err := DownloadBytes(url)
	if err != nil {
		return nil, err
	}

	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	targetInterval := 100.0 / 3.0

	var selectedFrames []int
	currentTime := 0.0
	for i := range len(g.Image) {
		if currentTime >= float64(len(selectedFrames))*targetInterval {
			selectedFrames = append(selectedFrames, i)
		}
		delay := g.Delay[i]
		if delay == 0 {
			delay = 10
		}
		currentTime += float64(delay)
	}

	if len(selectedFrames) == 0 {
		selectedFrames = []int{0}
	}

	b := [][9]*bytes.Buffer{}
	for i, frameIndex := range selectedFrames {
		if i%9 == 0 {
			b = append(b, [9]*bytes.Buffer{})
		}
		chunkIndex := len(b) - 1
		positionInChunk := i % 9

		b[chunkIndex][positionInChunk] = &bytes.Buffer{}
		png.Encode(b[chunkIndex][positionInChunk], g.Image[frameIndex])
	}

	var base64s []string
	for _, chunk := range b {
		var validBuffers []*bytes.Buffer
		for i := range 9 {
			if chunk[i] != nil && chunk[i].Len() > 0 {
				validBuffers = append(validBuffers, chunk[i])
			}
		}

		if len(validBuffers) > 0 {
			gridBuffer, err := CombinePNGsToGridSimple(validBuffers)
			if err != nil {
				return nil, err
			}
			base64s = append(base64s, base64.StdEncoding.EncodeToString(gridBuffer.Bytes()))
		}
	}

	return base64s, nil
}

func CombinePNGsToGrid(pngBuffers []*bytes.Buffer, cellSize int) (*bytes.Buffer, error) {
	if len(pngBuffers) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	// Calculate grid dimensions (square grid)
	gridSize := int(math.Ceil(math.Sqrt(float64(len(pngBuffers)))))

	// Decode all PNG images
	images := make([]image.Image, len(pngBuffers))
	for i, buf := range pngBuffers {
		img, err := png.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PNG %d: %w", i, err)
		}
		images[i] = img
	}

	// Create output image
	outputWidth := gridSize * cellSize
	outputHeight := gridSize * cellSize
	outputImg := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))

	// Fill with white background
	for y := 0; y < outputHeight; y++ {
		for x := 0; x < outputWidth; x++ {
			outputImg.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}

	// Draw images into grid
	for i, img := range images {
		row := i / gridSize
		col := i % gridSize

		// Calculate position in grid
		startX := col * cellSize
		startY := row * cellSize

		// Get source image bounds
		srcBounds := img.Bounds()
		srcWidth := srcBounds.Dx()
		srcHeight := srcBounds.Dy()

		// Calculate scaling to fit in cell while maintaining aspect ratio
		scaleX := float64(cellSize) / float64(srcWidth)
		scaleY := float64(cellSize) / float64(srcHeight)
		scale := math.Min(scaleX, scaleY)

		newWidth := int(float64(srcWidth) * scale)
		newHeight := int(float64(srcHeight) * scale)

		// Center the image in the cell
		offsetX := (cellSize - newWidth) / 2
		offsetY := (cellSize - newHeight) / 2

		// Draw the scaled image
		for y := 0; y < newHeight; y++ {
			for x := 0; x < newWidth; x++ {
				// Calculate source pixel
				srcX := int(float64(x) / scale)
				srcY := int(float64(y) / scale)

				if srcX < srcWidth && srcY < srcHeight {
					srcColor := img.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY)
					outputImg.Set(startX+offsetX+x, startY+offsetY+y, srcColor)
				}
			}
		}
	}

	// Encode to PNG
	var outputBuffer bytes.Buffer
	err := png.Encode(&outputBuffer, outputImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode output PNG: %w", err)
	}

	return &outputBuffer, nil
}

func CombinePNGsToGridSimple(pngBuffers []*bytes.Buffer) (*bytes.Buffer, error) {
	if len(pngBuffers) == 0 {
		return nil, fmt.Errorf("no images provided")
	}

	// Decode first image to get dimensions
	firstImg, err := png.Decode(bytes.NewReader(pngBuffers[0].Bytes()))
	if err != nil {
		return nil, fmt.Errorf("failed to decode first PNG: %w", err)
	}

	imgBounds := firstImg.Bounds()
	imgWidth := imgBounds.Dx()
	imgHeight := imgBounds.Dy()

	// Calculate grid dimensions
	gridSize := int(math.Ceil(math.Sqrt(float64(len(pngBuffers)))))

	// Create output image
	outputWidth := gridSize * imgWidth
	outputHeight := gridSize * imgHeight
	outputImg := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))

	// Process all images
	for i, buf := range pngBuffers {
		img, err := png.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PNG %d: %w", i, err)
		}

		row := i / gridSize
		col := i % gridSize

		startX := col * imgWidth
		startY := row * imgHeight

		// Copy image pixels
		bounds := img.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				outputImg.Set(startX+x-bounds.Min.X, startY+y-bounds.Min.Y, img.At(x, y))
			}
		}
	}

	// Encode to PNG
	var outputBuffer bytes.Buffer
	err = png.Encode(&outputBuffer, outputImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode output PNG: %w", err)
	}

	return &outputBuffer, nil
}

func BytesToPNG(data []byte) (*bytes.Buffer, error) {
	// 4 bytes for length header + data
	totalLen := len(data) + 4
	numPixels := int(math.Ceil(float64(totalLen) / 4.0))

	// Calculate dimensions for square-ish image
	width := int(math.Ceil(math.Sqrt(float64(numPixels))))
	height := int(math.Ceil(float64(numPixels) / float64(width)))

	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	// Write length header
	binary.BigEndian.PutUint32(img.Pix[0:4], uint32(len(data)))

	// Write data
	copy(img.Pix[4:], data)

	var buf bytes.Buffer
	// Use default compression
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return &buf, nil
}

func PNGToBytes(pngData []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}

	var pix []uint8
	switch i := img.(type) {
	case *image.NRGBA:
		pix = i.Pix
	case *image.RGBA:
		// Warning: this might have altered data due to premultiplication if it wasn't NRGBA
		pix = i.Pix
	default:
		// Convert to NRGBA
		bounds := img.Bounds()
		nrgba := image.NewNRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				nrgba.Set(x, y, img.At(x, y))
			}
		}
		pix = nrgba.Pix
	}

	if len(pix) < 4 {
		return nil, fmt.Errorf("image too small")
	}

	// Read length header
	dataLen := binary.BigEndian.Uint32(pix[0:4])

	if uint64(dataLen) > uint64(len(pix)-4) {
		return nil, fmt.Errorf("data length %d exceeds available pixel data", dataLen)
	}

	// Create a copy of the data to return
	data := make([]byte, dataLen)
	copy(data, pix[4:4+dataLen])

	return data, nil
}
