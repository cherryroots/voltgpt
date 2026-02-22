package utility

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUrlToExt(t *testing.T) {
	tests := []struct {
		name    string
		urlStr  string
		want    string
		wantErr bool
	}{
		{
			name:   "simple jpg",
			urlStr: "https://example.com/image.jpg",
			want:   ".jpg",
		},
		{
			name:   "uppercase PNG lowercased",
			urlStr: "https://example.com/image.PNG",
			want:   ".png",
		},
		{
			name:   "no extension",
			urlStr: "https://example.com/image",
			want:   "",
		},
		{
			name:   "with query params strips params",
			urlStr: "https://example.com/image.jpg?size=large",
			want:   ".jpg",
		},
		{
			name:   "gif extension",
			urlStr: "https://cdn.example.com/animated.gif",
			want:   ".gif",
		},
		{
			name:   "mp4 video",
			urlStr: "https://cdn.example.com/video.mp4",
			want:   ".mp4",
		},
		{
			name:   "pdf file",
			urlStr: "https://example.com/document.pdf",
			want:   ".pdf",
		},
		{
			name:   "webp image",
			urlStr: "https://example.com/photo.webp",
			want:   ".webp",
		},
		{
			name:   "nested path",
			urlStr: "https://example.com/path/to/file.png",
			want:   ".png",
		},
		{
			name:   "empty string",
			urlStr: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := URLToExt(tt.urlStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("UrlToExt(%q) error = %v, wantErr %v", tt.urlStr, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("UrlToExt(%q) = %q, want %q", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestIsImageURL(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   bool
	}{
		{"jpg", "https://example.com/image.jpg", true},
		{"jpeg", "https://example.com/image.jpeg", true},
		{"png", "https://example.com/image.png", true},
		{"gif", "https://example.com/image.gif", true},
		{"webp", "https://example.com/image.webp", true},
		{"mp4 not image", "https://example.com/video.mp4", false},
		{"pdf not image", "https://example.com/doc.pdf", false},
		{"no extension", "https://example.com/image", false},
		{"uppercase jpg", "https://example.com/IMAGE.JPG", true},
		{"webm not image", "https://example.com/video.webm", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsImageURL(tt.urlStr)
			if got != tt.want {
				t.Errorf("IsImageURL(%q) = %v, want %v", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestIsVideoURL(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   bool
	}{
		{"mp4", "https://example.com/video.mp4", true},
		{"webm", "https://example.com/video.webm", true},
		{"mov", "https://example.com/video.mov", true},
		{"jpg not video", "https://example.com/image.jpg", false},
		{"pdf not video", "https://example.com/doc.pdf", false},
		{"no extension", "https://example.com/video", false},
		{"uppercase MP4", "https://example.com/VIDEO.MP4", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsVideoURL(tt.urlStr)
			if got != tt.want {
				t.Errorf("IsVideoURL(%q) = %v, want %v", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestIsPDFURL(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   bool
	}{
		{"pdf", "https://example.com/doc.pdf", true},
		{"uppercase PDF", "https://example.com/doc.PDF", true},
		{"jpg not pdf", "https://example.com/image.jpg", false},
		{"no extension", "https://example.com/document", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPDFURL(tt.urlStr)
			if got != tt.want {
				t.Errorf("IsPDFURL(%q) = %v, want %v", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestIsYTURL(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   bool
	}{
		{"youtube.com", "https://www.youtube.com/watch?v=abc123", true},
		{"youtu.be short", "https://youtu.be/abc123", true},
		{"not youtube", "https://vimeo.com/123", false},
		{"empty string", "", false},
		{"contains youtube in path", "https://example.com/youtube.com/video", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsYTURL(tt.urlStr)
			if got != tt.want {
				t.Errorf("IsYTURL(%q) = %v, want %v", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestMediaType(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   string
	}{
		{"jpg", "https://example.com/image.jpg", "image/jpeg"},
		{"jpeg", "https://example.com/image.jpeg", "image/jpeg"},
		{"png", "https://example.com/image.png", "image/png"},
		{"gif", "https://example.com/image.gif", "image/gif"},
		{"webp", "https://example.com/image.webp", "image/webp"},
		{"mp4", "https://example.com/video.mp4", "video/mp4"},
		{"webm", "https://example.com/video.webm", "video/webm"},
		{"mov", "https://example.com/video.mov", "video/quicktime"},
		{"pdf", "https://example.com/doc.pdf", "application/pdf"},
		{"unknown extension", "https://example.com/file.xyz", ""},
		{"no extension", "https://example.com/file", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MediaType(tt.urlStr)
			if got != tt.want {
				t.Errorf("MediaType(%q) = %q, want %q", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestHasExtension(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		extensions []string
		want       bool
	}{
		{
			name:       "matching extension",
			url:        "https://example.com/image.jpg",
			extensions: []string{".jpg", ".png"},
			want:       true,
		},
		{
			name:       "no matching extension",
			url:        "https://example.com/image.gif",
			extensions: []string{".jpg", ".png"},
			want:       false,
		},
		{
			name:       "nil extensions returns false",
			url:        "https://example.com/image.jpg",
			extensions: nil,
			want:       false,
		},
		{
			name:       "empty extensions slice returns false",
			url:        "https://example.com/image.jpg",
			extensions: []string{},
			want:       false,
		},
		{
			name:       "uppercase extension matched lowercased",
			url:        "https://example.com/image.JPG",
			extensions: []string{".jpg"},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasExtension(tt.url, tt.extensions)
			if got != tt.want {
				t.Errorf("HasExtension(%q, %v) = %v, want %v", tt.url, tt.extensions, got, tt.want)
			}
		})
	}
}

func TestMatchYTDLPWebsites(t *testing.T) {
	tests := []struct {
		name   string
		urlStr string
		want   bool
	}{
		{
			name:   "vimeo https",
			urlStr: "https://vimeo.com/123456789",
			want:   true,
		},
		{
			name:   "vimeo www",
			urlStr: "https://www.vimeo.com/123456789",
			want:   true,
		},
		{
			name:   "youtube not matched",
			urlStr: "https://youtube.com/watch?v=abc",
			want:   false,
		},
		{
			name:   "empty string",
			urlStr: "",
			want:   false,
		},
		{
			name:   "random url",
			urlStr: "https://example.com/video",
			want:   false,
		},
		{
			name:   "vimeo without scheme",
			urlStr: "//vimeo.com/123456789",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchYTDLPWebsites(tt.urlStr)
			if got != tt.want {
				t.Errorf("MatchYTDLPWebsites(%q) = %v, want %v", tt.urlStr, got, tt.want)
			}
		})
	}
}

func TestDownloadURL(t *testing.T) {
	// Set up a local test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("hello content"))
		case "/notfound":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Run("successful download", func(t *testing.T) {
		data, err := DownloadBytes(server.URL + "/ok")
		if err != nil {
			t.Fatalf("DownloadURL() unexpected error: %v", err)
		}
		if string(data) != "hello content" {
			t.Errorf("DownloadURL() = %q, want %q", string(data), "hello content")
		}
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		_, err := DownloadBytes(server.URL + "/notfound")
		if err == nil {
			t.Error("DownloadURL() expected error for 404 status, got nil")
		}
	})

	t.Run("invalid URL returns error", func(t *testing.T) {
		_, err := DownloadBytes("http://127.0.0.1:0/invalid")
		if err == nil {
			t.Error("DownloadURL() expected error for unreachable URL, got nil")
		}
	})
}

func TestUrlToExtInvalidURL(t *testing.T) {
	_, err := URLToExt("://bad url")
	if err == nil {
		t.Error("UrlToExt() expected error for invalid URL, got nil")
	}
}

func TestIsImageURLInvalidURL(t *testing.T) {
	if IsImageURL("://bad url") {
		t.Error("IsImageURL() = true for invalid URL, want false")
	}
}

func TestIsVideoURLInvalidURL(t *testing.T) {
	if IsVideoURL("://bad url") {
		t.Error("IsVideoURL() = true for invalid URL, want false")
	}
}

func TestIsPDFURLInvalidURL(t *testing.T) {
	if IsPDFURL("://bad url") {
		t.Error("IsPDFURL() = true for invalid URL, want false")
	}
}

func TestMediaTypeUnknown(t *testing.T) {
	got := MediaType("https://example.com/file.xyz")
	if got != "" {
		t.Errorf("MediaType() = %q for unknown extension, want \"\"", got)
	}
}
