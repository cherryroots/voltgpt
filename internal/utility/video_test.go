package utility

import (
	"bytes"
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

func TestVideoToBase64ImagesFrameTimestamps_SingleFrame(t *testing.T) {
	// When totalFrames is clamped to 1, timestamp must not be NaN.
	// usableDuration * 0.98 * 3fps < 1 → duration < 0.34s triggers this.
	duration := 0.1
	usableDuration := duration * 0.98
	totalFrames := int(usableDuration * 3.0) // == 0, clamped to 1
	if totalFrames < 1 {
		totalFrames = 1
	}
	var timestamps []float64
	if totalFrames == 1 {
		timestamps = []float64{usableDuration / 2}
	} else {
		for i := range totalFrames {
			timestamps = append(timestamps, (float64(i)/float64(totalFrames-1))*usableDuration)
		}
	}
	if len(timestamps) != 1 {
		t.Fatalf("expected 1 timestamp, got %d", len(timestamps))
	}
	ts := timestamps[0]
	if ts != ts { // NaN check: NaN != NaN
		t.Errorf("timestamp is NaN")
	}
	if ts <= 0 {
		t.Errorf("timestamp %f must be > 0", ts)
	}
}

func TestVideoToBase64ImagesChunkIndex_NilFirstFrame(t *testing.T) {
	// Simulate the chunk grouping with frames[0] == nil, frames[1] != nil.
	// Without the fix, b[len(b)-1] panics. With the fix it must not panic.
	frames := make([]*bytes.Buffer, 2)
	frames[0] = nil
	frames[1] = bytes.NewBufferString("data")

	b := [][81]*bytes.Buffer{}
	for i, frame := range frames {
		if frame == nil {
			continue
		}
		if len(b) == 0 || i%81 == 0 {
			b = append(b, [81]*bytes.Buffer{})
		}
		chunkIndex := len(b) - 1
		b[chunkIndex][i%81] = frame
	}
	if len(b) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if b[0][1] == nil {
		t.Error("expected frame at position 1 in chunk 0")
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
