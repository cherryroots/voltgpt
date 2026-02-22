package utility

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

func DownloadBytes(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func URLToExt(urlStr string) (string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	fileExt := filepath.Ext(parsedURL.Path)
	fileExt = strings.Split(fileExt, ":")[0]
	fileExt = strings.ToLower(fileExt)
	return fileExt, nil
}

func IsImageURL(urlStr string) bool {
	fileExt, err := URLToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func IsVideoURL(urlStr string) bool {
	fileExt, err := URLToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".mp4", ".webm", ".mov":
		return true
	default:
		return false
	}
}

func IsPDFURL(urlStr string) bool {
	fileExt, err := URLToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".pdf":
		return true
	default:
		return false
	}
}

func IsYTURL(urlStr string) bool {
	return strings.Contains(urlStr, "youtube.com") || strings.Contains(urlStr, "youtu.be")
}

func MediaType(urlStr string) string {
	fileExt, err := URLToExt(urlStr)
	if err != nil {
		return ""
	}

	switch fileExt {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func HasExtension(URL string, extensions []string) bool {
	if extensions == nil {
		return false
	}
	for _, extension := range extensions {
		urlExt, _ := URLToExt(URL)
		if urlExt == extension {
			return true
		}
	}
	return false
}

func MatchYTDLPWebsites(urlStr string) bool {
	if urlStr == "" {
		return false
	}
	// vimeo
	urlRegexes := []*regexp.Regexp{
		regexp.MustCompile(`^((?:https?:)?\/\/)?((?:www|m)\.)?((?:vimeo\.com))(\/)([\w\-]+)(\S+)?$`),
	}

	for _, r := range urlRegexes {
		if r.MatchString(urlStr) {
			return true
		}
	}
	return false
}

func cleanURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return parsedURL.Path
}
