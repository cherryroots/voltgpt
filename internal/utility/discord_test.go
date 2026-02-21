package utility

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/config"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{
			name: "first admin",
			id:   config.Admins[0],
			want: true,
		},
		{
			name: "second admin",
			id:   config.Admins[1],
			want: true,
		},
		{
			name: "not an admin",
			id:   "9999999999999999999",
			want: false,
		},
		{
			name: "empty id",
			id:   "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAdmin(tt.id)
			if got != tt.want {
				t.Errorf("IsAdmin(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestLinkFromIMessage(t *testing.T) {
	m := &discordgo.Message{
		ID:        "111",
		ChannelID: "222",
	}
	got := LinkFromIMessage("333", m)
	want := "https://discord.com/channels/333/222/111"
	if got != want {
		t.Errorf("LinkFromIMessage() = %q, want %q", got, want)
	}
}

func TestResolveMentions(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		mentions []*discordgo.User
		want     string
	}{
		{
			name:    "replace with GlobalName",
			content: "hello <@123>",
			mentions: []*discordgo.User{
				{ID: "123", GlobalName: "Alice", Username: "alice_user"},
			},
			want: "hello Alice",
		},
		{
			name:    "fallback to Username when GlobalName empty",
			content: "hello <@456>",
			mentions: []*discordgo.User{
				{ID: "456", GlobalName: "", Username: "bob_user"},
			},
			want: "hello bob_user",
		},
		{
			name:    "no mentions",
			content: "hello world",
			mentions: []*discordgo.User{
				{ID: "789", GlobalName: "Charlie"},
			},
			want: "hello world",
		},
		{
			name:     "nil mentions slice",
			content:  "hello <@123>",
			mentions: nil,
			want:     "hello <@123>",
		},
		{
			name:    "multiple mentions",
			content: "hey <@1> and <@2>",
			mentions: []*discordgo.User{
				{ID: "1", GlobalName: "Alice"},
				{ID: "2", GlobalName: "Bob"},
			},
			want: "hey Alice and Bob",
		},
		{
			name:    "mention not in list stays unchanged",
			content: "hello <@999>",
			mentions: []*discordgo.User{
				{ID: "123", GlobalName: "Alice"},
			},
			want: "hello <@999>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveMentions(tt.content, tt.mentions)
			if got != tt.want {
				t.Errorf("ResolveMentions(%q, ...) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestCleanName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "alphanumeric unchanged",
			input: "HelloWorld123",
			want:  "HelloWorld123",
		},
		{
			name:  "underscore and dash preserved",
			input: "hello_world-test",
			want:  "hello_world-test",
		},
		{
			name:  "spaces removed",
			input: "hello world",
			want:  "helloworld",
		},
		{
			name:  "special chars removed",
			input: "hello!@#$%",
			want:  "hello",
		},
		{
			name:  "unicode removed",
					input: "仙女Alice",
			want:  "Alice",
		},
		{
			name:  "truncate at 64 chars before cleaning",
			input: strings.Repeat("a", 70),
			want:  strings.Repeat("a", 64),
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "all special chars returns empty",
			input: "!@#$%^&*()",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanName(tt.input)
			if got != tt.want {
				t.Errorf("CleanName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetMessageMediaURL(t *testing.T) {
	t.Run("image attachment with dimensions", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{
					URL:    "https://example.com/image.png",
					Width:  100,
					Height: 100,
				},
			},
		}
		images, videos, pdfs, ytURLs := GetMessageMediaURL(m)
		if len(images) != 1 || images[0] != "https://example.com/image.png" {
			t.Errorf("expected 1 image, got %v", images)
		}
		if len(videos) != 0 || len(pdfs) != 0 || len(ytURLs) != 0 {
			t.Errorf("expected no other media, got videos=%v pdfs=%v yt=%v", videos, pdfs, ytURLs)
		}
	})

	t.Run("image attachment without dimensions skipped", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{
					URL:    "https://example.com/image.png",
					Width:  0,
					Height: 0,
				},
			},
		}
		images, _, _, _ := GetMessageMediaURL(m)
		if len(images) != 0 {
			t.Errorf("expected no images for attachment without dimensions, got %v", images)
		}
	})

	t.Run("pdf attachment always included", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{
					URL:    "https://example.com/doc.pdf",
					Width:  0,
					Height: 0,
				},
			},
		}
		_, _, pdfs, _ := GetMessageMediaURL(m)
		if len(pdfs) != 1 {
			t.Errorf("expected 1 pdf, got %v", pdfs)
		}
	})

	t.Run("video attachment with dimensions", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{
					URL:    "https://example.com/video.mp4",
					Width:  1920,
					Height: 1080,
				},
			},
		}
		_, videos, _, _ := GetMessageMediaURL(m)
		if len(videos) != 1 {
			t.Errorf("expected 1 video, got %v", videos)
		}
	})

	t.Run("image URL in message content", func(t *testing.T) {
		m := &discordgo.Message{
			Content: "check this out https://example.com/photo.jpg",
		}
		images, _, _, _ := GetMessageMediaURL(m)
		if len(images) != 1 {
			t.Errorf("expected 1 image from content, got %v", images)
		}
	})

	t.Run("youtube URL in content", func(t *testing.T) {
		m := &discordgo.Message{
			Content: "watch https://youtube.com/watch?v=abc123",
		}
		_, _, _, ytURLs := GetMessageMediaURL(m)
		if len(ytURLs) != 1 {
			t.Errorf("expected 1 yt URL, got %v", ytURLs)
		}
	})

	t.Run("tenor embed thumbnail excluded", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Provider: &discordgo.MessageEmbedProvider{Name: "tenor"},
					Thumbnail: &discordgo.MessageEmbedThumbnail{
						URL: "https://media.tenor.com/something.gif",
					},
				},
			},
		}
		images, _, _, _ := GetMessageMediaURL(m)
		if len(images) != 0 {
			t.Errorf("tenor thumbnail should be excluded, got %v", images)
		}
	})

	t.Run("deduplication same URL twice", func(t *testing.T) {
		m := &discordgo.Message{
			Content: "https://example.com/image.png https://example.com/image.png",
		}
		images, _, _, _ := GetMessageMediaURL(m)
		if len(images) != 1 {
			t.Errorf("expected 1 deduplicated image, got %v", images)
		}
	})

	t.Run("embed image URL", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Image: &discordgo.MessageEmbedImage{
						URL: "https://example.com/embed_image.png",
					},
				},
			},
		}
		images, _, _, _ := GetMessageMediaURL(m)
		if len(images) != 1 {
			t.Errorf("expected 1 image from embed, got %v", images)
		}
	})
}

func TestHasImageURL(t *testing.T) {
	t.Run("has image attachment", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{URL: "https://example.com/image.png"},
			},
		}
		if !HasImageURL(m) {
			t.Error("expected true, got false")
		}
	})

	t.Run("no image", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{URL: "https://example.com/doc.pdf"},
			},
		}
		if HasImageURL(m) {
			t.Error("expected false, got true")
		}
	})

	t.Run("image URL in content", func(t *testing.T) {
		m := &discordgo.Message{
			Content: "look at https://example.com/photo.jpg",
		}
		if !HasImageURL(m) {
			t.Error("expected true for image URL in content, got false")
		}
	})

	t.Run("embed thumbnail image", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Thumbnail: &discordgo.MessageEmbedThumbnail{
						URL: "https://example.com/thumb.png",
					},
				},
			},
		}
		if !HasImageURL(m) {
			t.Error("expected true for embed thumbnail, got false")
		}
	})

	t.Run("empty message", func(t *testing.T) {
		m := &discordgo.Message{}
		if HasImageURL(m) {
			t.Error("expected false for empty message, got true")
		}
	})
}

func TestHasVideoURL(t *testing.T) {
	t.Run("has video attachment", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{URL: "https://example.com/video.mp4"},
			},
		}
		if !HasVideoURL(m) {
			t.Error("expected true, got false")
		}
	})

	t.Run("no video", func(t *testing.T) {
		m := &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{URL: "https://example.com/image.png"},
			},
		}
		if HasVideoURL(m) {
			t.Error("expected false, got true")
		}
	})

	t.Run("video URL in content", func(t *testing.T) {
		m := &discordgo.Message{
			Content: "https://example.com/clip.webm",
		}
		if !HasVideoURL(m) {
			t.Error("expected true for video URL in content, got false")
		}
	})

	t.Run("embed video URL", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Video: &discordgo.MessageEmbedVideo{
						URL: "https://example.com/video.mp4",
					},
				},
			},
		}
		if !HasVideoURL(m) {
			t.Error("expected true for embed video, got false")
		}
	})
}

func TestEmbedText(t *testing.T) {
	t.Run("empty embeds returns empty string", func(t *testing.T) {
		m := &discordgo.Message{}
		got := EmbedText(m)
		if got != "" {
			t.Errorf("EmbedText() = %q, want empty string", got)
		}
	})

	t.Run("embed with title and description", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "Test Title",
					Description: "Test Description",
				},
			},
		}
		got := EmbedText(m)
		if !strings.Contains(got, "Test Title") {
			t.Errorf("EmbedText() should contain title, got %q", got)
		}
		if !strings.Contains(got, "Test Description") {
			t.Errorf("EmbedText() should contain description, got %q", got)
		}
		if !strings.Contains(got, "<embeds>") || !strings.Contains(got, "</embeds>") {
			t.Errorf("EmbedText() should be wrapped in <embeds> tags, got %q", got)
		}
	})

	t.Run("embed with fields", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{
					Fields: []*discordgo.MessageEmbedField{
						{Name: "FieldName", Value: "FieldValue"},
					},
				},
			},
		}
		got := EmbedText(m)
		if !strings.Contains(got, "FieldName") {
			t.Errorf("EmbedText() should contain field name, got %q", got)
		}
		if !strings.Contains(got, "FieldValue") {
			t.Errorf("EmbedText() should contain field value, got %q", got)
		}
	})

	t.Run("multiple embeds", func(t *testing.T) {
		m := &discordgo.Message{
			Embeds: []*discordgo.MessageEmbed{
				{Title: "First"},
				{Title: "Second"},
			},
		}
		got := EmbedText(m)
		if !strings.Contains(got, "First") || !strings.Contains(got, "Second") {
			t.Errorf("EmbedText() should contain both embed titles, got %q", got)
		}
	})
}

func TestMessageToEmbeds(t *testing.T) {
	m := &discordgo.Message{
		ID:        "msg1",
		ChannelID: "chan1",
		Content:   "hello",
		Author: &discordgo.User{
			ID:       "user1",
			Username: "Alice",
		},
	}
	embeds := MessageToEmbeds("guild1", m, 7)

	if len(embeds) == 0 {
		t.Fatal("MessageToEmbeds() returned no embeds")
	}
	first := embeds[0]
	if first.Title != "Message link" {
		t.Errorf("embed Title = %q, want \"Message link\"", first.Title)
	}
	if first.Description != "hello" {
		t.Errorf("embed Description = %q, want \"hello\"", first.Description)
	}
	if first.Footer == nil || !strings.Contains(first.Footer.Text, "7bit distance") {
		t.Errorf("embed Footer = %v, want text containing \"7bit distance\"", first.Footer)
	}
	wantURL := "https://discord.com/channels/guild1/chan1/msg1"
	if first.URL != wantURL {
		t.Errorf("embed URL = %q, want %q", first.URL, wantURL)
	}
}

func TestMessageToEmbedsIncludesMessageEmbeds(t *testing.T) {
	inner := &discordgo.MessageEmbed{Title: "inner"}
	m := &discordgo.Message{
		ID:        "msg2",
		ChannelID: "chan1",
		Author:    &discordgo.User{ID: "u1", Username: "Bob"},
		Embeds:    []*discordgo.MessageEmbed{inner},
	}
	embeds := MessageToEmbeds("guild1", m, 0)
	if len(embeds) != 2 {
		t.Fatalf("MessageToEmbeds() len = %d, want 2", len(embeds))
	}
	if embeds[1].Title != "inner" {
		t.Errorf("embeds[1].Title = %q, want \"inner\"", embeds[1].Title)
	}
}

func TestAttachmentTextEmpty(t *testing.T) {
	m := &discordgo.Message{}
	got := AttachmentText(m)
	if got != "" {
		t.Errorf("AttachmentText() = %q for no attachments, want \"\"", got)
	}
}

func TestHasImageURLFromContent(t *testing.T) {
	m := &discordgo.Message{
		Content: "check this out https://example.com/photo.png cool right",
	}
	if !HasImageURL(m) {
		t.Error("HasImageURL() = false for message with image URL in content, want true")
	}
}

func TestHasVideoURLFromContent(t *testing.T) {
	m := &discordgo.Message{
		Content: "https://example.com/clip.mp4",
	}
	if !HasVideoURL(m) {
		t.Error("HasVideoURL() = false for message with video URL in content, want true")
	}
}
