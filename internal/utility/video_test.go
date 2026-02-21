package utility

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

const testVideoPath = "testdata/test.mp4"

func TestGetVideoDuration(t *testing.T) {
	if _, err := os.Stat(testVideoPath); err != nil {
		t.Skipf("testdata/test.mp4 not found, skipping: %v", err)
	}
	duration, err := getVideoDuration(testVideoPath)
	if err != nil {
		t.Fatalf("getVideoDuration() error: %v", err)
	}
	// The test video is 1 second; allow some tolerance
	if duration < 0.9 || duration > 2.0 {
		t.Errorf("getVideoDuration() = %f, want ~1.0", duration)
	}
}

func TestExtractVideoFrameAtTime(t *testing.T) {
	if _, err := os.Stat(testVideoPath); err != nil {
		t.Skipf("testdata/test.mp4 not found, skipping: %v", err)
	}
	reader, err := extractVideoFrameAtTime(testVideoPath, 0.0)
	if err != nil {
		t.Fatalf("extractVideoFrameAtTime() error: %v", err)
	}
	if reader == nil {
		t.Fatal("extractVideoFrameAtTime() returned nil reader")
	}
}

func TestVideoToBase64ImagesFromServer(t *testing.T) {
	videoData, err := os.ReadFile(testVideoPath)
	if err != nil {
		t.Skipf("testdata/test.mp4 not found, skipping: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(videoData)
	}))
	defer srv.Close()

	results, err := VideoToBase64Images(srv.URL + "/test.mp4")
	if err != nil {
		t.Fatalf("VideoToBase64Images() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("VideoToBase64Images() returned no results")
	}
}
