package utility

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeTestPNG returns a minimal valid 4x4 RGBA PNG encoded as a bytes.Buffer.
func makeTestPNG(t *testing.T) *bytes.Buffer {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("makeTestPNG: %v", err)
	}
	return &buf
}

// makeTestGIF returns a minimal valid 2-frame 4x4 animated GIF.
func makeTestGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	frame1 := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	frame2 := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	frame2.SetColorIndex(2, 2, 1)
	g := &gif.GIF{
		Image: []*image.Paletted{frame1, frame2},
		Delay: []int{10, 10},
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("makeTestGIF: %v", err)
	}
	return buf.Bytes()
}

// serveBytes starts an httptest server that serves the given bytes for any request.
func serveBytes(data []byte, contentType string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}))
}

func TestCombinePNGsToGridSimpleOne(t *testing.T) {
	buf := makeTestPNG(t)
	result, err := CombinePNGsToGridSimple([]*bytes.Buffer{buf})
	if err != nil {
		t.Fatalf("CombinePNGsToGridSimple() error: %v", err)
	}
	if _, err := png.Decode(result); err != nil {
		t.Errorf("CombinePNGsToGridSimple() output is not valid PNG: %v", err)
	}
}

func TestCombinePNGsToGridSimpleFour(t *testing.T) {
	var bufs []*bytes.Buffer
	for range 4 {
		bufs = append(bufs, makeTestPNG(t))
	}
	result, err := CombinePNGsToGridSimple(bufs)
	if err != nil {
		t.Fatalf("CombinePNGsToGridSimple() error: %v", err)
	}
	img, err := png.Decode(result)
	if err != nil {
		t.Fatalf("CombinePNGsToGridSimple() result is not valid PNG: %v", err)
	}
	// 4 images → 2x2 grid → each 4px → 8x8 output
	if img.Bounds().Dx() != 8 || img.Bounds().Dy() != 8 {
		t.Errorf("CombinePNGsToGridSimple() size = %dx%d, want 8x8", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestCombinePNGsToGridSimpleEmpty(t *testing.T) {
	_, err := CombinePNGsToGridSimple([]*bytes.Buffer{})
	if err == nil {
		t.Error("CombinePNGsToGridSimple() expected error for empty input, got nil")
	}
}

func TestCombinePNGsToGrid(t *testing.T) {
	var bufs []*bytes.Buffer
	for range 2 {
		bufs = append(bufs, makeTestPNG(t))
	}
	result, err := CombinePNGsToGrid(bufs, 8)
	if err != nil {
		t.Fatalf("CombinePNGsToGrid() error: %v", err)
	}
	img, err := png.Decode(result)
	if err != nil {
		t.Fatalf("CombinePNGsToGrid() result is not valid PNG: %v", err)
	}
	// 2 images → 2x2 grid, cellSize=8 → 16x16 output
	if img.Bounds().Dx() != 16 || img.Bounds().Dy() != 16 {
		t.Errorf("CombinePNGsToGrid() size = %dx%d, want 16x16", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestCombinePNGsToGridEmpty(t *testing.T) {
	_, err := CombinePNGsToGrid([]*bytes.Buffer{}, 8)
	if err == nil {
		t.Error("CombinePNGsToGrid() expected error for empty input, got nil")
	}
}

func TestBytesToPNGAndPNGToBytes(t *testing.T) {
	original := []byte("round-trip test data 1234567890 abcdef")
	pngBuf, err := BytesToPNG(original)
	if err != nil {
		t.Fatalf("BytesToPNG() error: %v", err)
	}
	recovered, err := PNGToBytes(pngBuf.Bytes())
	if err != nil {
		t.Fatalf("PNGToBytes() error: %v", err)
	}
	if string(recovered) != string(original) {
		t.Errorf("round-trip mismatch: got %q, want %q", recovered, original)
	}
}

func TestBase64ImageFromServer(t *testing.T) {
	pngData := makeTestPNG(t).Bytes()
	srv := serveBytes(pngData, "image/png")
	defer srv.Close()

	results, err := Base64Image(srv.URL + "/test.png")
	if err != nil {
		t.Fatalf("Base64Image() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Base64Image() len = %d, want 1", len(results))
	}
	decoded, err := base64.StdEncoding.DecodeString(results[0])
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if !bytes.Equal(decoded, pngData) {
		t.Error("Base64Image() decoded data does not match original PNG")
	}
}

func TestGetAspectRatioFromServer(t *testing.T) {
	// 8x4 image → aspect ratio 2.0
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	srv := serveBytes(buf.Bytes(), "image/png")
	defer srv.Close()

	ratio, err := GetAspectRatio(srv.URL + "/test.png")
	if err != nil {
		t.Fatalf("GetAspectRatio() error: %v", err)
	}
	if ratio != 2.0 {
		t.Errorf("GetAspectRatio() = %f, want 2.0", ratio)
	}
}

func TestGifToBase64ImagesFromServer(t *testing.T) {
	gifData := makeTestGIF(t)
	srv := serveBytes(gifData, "image/gif")
	defer srv.Close()

	results, err := GifToBase64Images(srv.URL + "/test.gif")
	if err != nil {
		t.Fatalf("GifToBase64Images() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("GifToBase64Images() returned no results")
	}
	if _, err := base64.StdEncoding.DecodeString(results[0]); err != nil {
		t.Errorf("GifToBase64Images() result[0] is not valid base64: %v", err)
	}
}

func TestBase64ImageDownloadPNG(t *testing.T) {
	pngData := makeTestPNG(t).Bytes()
	srv := serveBytes(pngData, "image/png")
	defer srv.Close()

	results, err := Base64ImageDownload(srv.URL + "/test.png")
	if err != nil {
		t.Fatalf("Base64ImageDownload(.png) error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Base64ImageDownload(.png) len = %d, want 1", len(results))
	}
	if !strings.HasPrefix(results[0], "data:image/png;base64,") {
		t.Errorf("Base64ImageDownload(.png) = %q, want data:image/png;base64,...", results[0][:40])
	}
}

func TestBase64ImageDownloadGIF(t *testing.T) {
	gifData := makeTestGIF(t)
	srv := serveBytes(gifData, "image/gif")
	defer srv.Close()

	results, err := Base64ImageDownload(srv.URL + "/test.gif")
	if err != nil {
		t.Fatalf("Base64ImageDownload(.gif) error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Base64ImageDownload(.gif) returned no results")
	}
	if !strings.HasPrefix(results[0], "data:image/png;base64,") {
		t.Errorf("Base64ImageDownload(.gif) = %q, want data:image/png;base64,...", results[0][:40])
	}
}

func TestBase64ImageDownloadUnknownExt(t *testing.T) {
	srv := serveBytes([]byte("data"), "text/plain")
	defer srv.Close()

	_, err := Base64ImageDownload(srv.URL + "/file.xyz")
	if err == nil {
		t.Error("Base64ImageDownload() expected error for unknown extension, got nil")
	}
}
