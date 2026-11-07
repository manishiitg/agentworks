package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/chatvisuals"
	"github.com/slack-go/slack"
)

// Raw blocks preserve official-adapter elements newer than the Go Slack SDK.
// Only our embedded formatter supplies them; agent output is never passed raw.
type visualSlackBlock struct{ json.RawMessage }

func (b visualSlackBlock) ID() string { return "" }

func (b visualSlackBlock) BlockType() slack.MessageBlockType {
	var header struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b.RawMessage, &header)
	return slack.MessageBlockType(header.Type)
}

// WriteReply retains the connection/thread selected by the server. Formatting
// knows no token, account, routing grant or function execution capability.
func (s *SlackService) WriteReply(ctx context.Context, threadID ThreadID, messageID, content string, final bool) (string, error) {
	if target, err := s.serviceForThread(threadID); err != nil {
		return "", err
	} else if target != s {
		return target.WriteReply(ctx, threadID, messageID, content, final)
	}
	if s.client == nil {
		return "", fmt.Errorf("slack client not initialized")
	}
	if !chatvisuals.HasVisual(content) {
		if messageID == "" {
			return s.sendPlainThreadMessage(ctx, threadID, content)
		}
		return messageID, s.updatePlainMessage(ctx, threadID, messageID, content)
	}
	plain := func(text string) (string, error) {
		if messageID == "" {
			return s.sendPlainThreadMessage(ctx, threadID, text)
		}
		return messageID, s.updatePlainMessage(ctx, threadID, messageID, text)
	}
	reply, err := chatvisuals.Format(ctx, content, final)
	if err != nil {
		log.Printf("[SLACK_VISUAL] channel=%s thread=%s final=%t result=plain_fallback", threadID.ChannelID, threadID.ThreadTS, final)
		fallback := reply.Text
		if fallback == "" {
			fallback = chatvisuals.Fallback(content, final)
		}
		return plain(fallback)
	}
	blocks := make([]slack.Block, 0, len(reply.Blocks))
	for _, block := range reply.Blocks {
		blocks = append(blocks, visualSlackBlock{block})
	}
	opts := []slack.MsgOption{slack.MsgOptionText(reply.Text, false), slack.MsgOptionBlocks(blocks...)}
	if messageID == "" {
		_, id, err := s.client.PostMessageContext(ctx, threadID.ChannelID, append(opts, slackThreadOption(threadID.ChannelID, threadID.ThreadTS)...)...)
		if rejectedVisualBlocks(err) {
			return plain(reply.Text)
		}
		if err != nil {
			return "", err
		}
		log.Printf("[SLACK_VISUAL] channel=%s thread=%s message=%s final=%t blocks=%d action=post", threadID.ChannelID, threadID.ThreadTS, id, final, len(blocks))
		return id, nil
	}
	_, _, _, err = s.client.UpdateMessageContext(ctx, threadID.ChannelID, messageID, opts...)
	if rejectedVisualBlocks(err) {
		return plain(reply.Text)
	}
	if err == nil {
		log.Printf("[SLACK_VISUAL] channel=%s thread=%s message=%s final=%t blocks=%d action=edit", threadID.ChannelID, threadID.ThreadTS, messageID, final, len(blocks))
	}
	return messageID, err
}

// Retry only Slack's definite rejection of the blocks. An ambiguous network
// error must not cause a second post and duplicate a person's reply.
func rejectedVisualBlocks(err error) bool {
	var response slack.SlackErrorResponse
	return errors.As(err, &response) && (response.Err == "invalid_blocks" || response.Err == "invalid_blocks_format")
}
