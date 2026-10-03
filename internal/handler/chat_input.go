package handler

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/openai/openai-go/v3/responses"

	openaiapi "voltgpt/internal/apis/openai"
	"voltgpt/internal/config"
	"voltgpt/internal/utility"
)

// prependReplyMessages walks the reply chain above message and prepends each
// referenced message to chatMessages, oldest first.
func prependReplyMessages(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]responses.ResponseInputItemUnionParam) {
	reference := utility.GetReferencedMessage(s, message, cache)
	if reference == nil {
		return
	}

	reply := utility.CleanMessage(s, reference)
	reply.Content = utility.ResolveMentions(reply.Content, reply.Mentions)
	images, videos, _, _ := utility.GetMessageMediaURL(reply)

	replyContent := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("%s%s%s",
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			reply.Content,
		)),
		Images: images,
		Videos: videos,
	}

	role := "user"
	if reply.Author != nil && reply.Author.ID == s.State.User.ID {
		role = "assistant"
	} else {
		replyContent.Text = fmt.Sprintf("<user name=\"%s\"> %s </user>", reply.Author.Username, replyContent.Text)
	}

	newMsg := createChatInput(role, replyContent)
	*chatMessages = append([]responses.ResponseInputItemUnionParam{newMsg}, *chatMessages...)

	if reply.Type == discordgo.MessageTypeReply {
		prependReplyMessages(s, reference, cache, chatMessages)
	}
}

// createChatInput downloads the content's media and builds an OpenAI input
// message. PDFs are intentionally omitted because the chat path does not
// support them.
func createChatInput(role string, content config.RequestContent) responses.ResponseInputItemUnionParam {
	var images []openaiapi.Image

	if role != "assistant" {
		for _, imageURL := range content.Images {
			image, err := downloadInputImage(imageURL)
			if err != nil {
				log.Printf("chat: skip image %s: %v", imageURL, err)
				continue
			}
			images = append(images, image)
		}

		for _, videoURL := range content.Videos {
			frames, err := utility.VideoToBase64Images(videoURL)
			if err != nil {
				log.Printf("chat: skip video %s: %v", videoURL, err)
				continue
			}
			for _, frame := range frames {
				images = append(images, openaiapi.Image{MIME: "image/png", Data: frame})
			}
		}
	}

	return openaiapi.InputMessage(role, content.Text, images)
}

func downloadInputImage(imageURL string) (openaiapi.Image, error) {
	mime := utility.MediaType(imageURL)
	if mime == "" {
		return openaiapi.Image{}, fmt.Errorf("unknown media type")
	}

	data, err := utility.DownloadBytes(imageURL)
	if err != nil {
		return openaiapi.Image{}, err
	}

	return openaiapi.Image{MIME: mime, Data: base64.StdEncoding.EncodeToString(data)}, nil
}
