package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// ChannelHistoryReader is optional for connectors advertising ChannelHistory.
// Callers must authorize the channel before invoking this transport interface.
type ChannelHistoryReader interface {
	ReadChannelHistory(context.Context, string, ChannelHistoryRequest) (*ChannelHistoryResult, error)
}
type ChannelHistoryRequest struct {
	Limit          int
	Since          time.Time
	Before         time.Time
	IncludeThreads bool
}
type ChannelHistoryMessage struct {
	MessageID   string          `json:"message_id"`
	ThreadID    string          `json:"thread_id,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	BotID       string          `json:"bot_id,omitempty"`
	Text        string          `json:"text"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
}
type ChannelHistoryResult struct {
	ChannelID string                             `json:"channel_id"`
	Messages  []ChannelHistoryMessage            `json:"messages"`
	Threads   map[string][]ChannelHistoryMessage `json:"threads,omitempty"`
	Truncated bool                               `json:"truncated"`
}

func historyMessage(message slack.Message) ChannelHistoryMessage {
	attachments, _ := json.Marshal(message.Attachments)
	if len(message.Attachments) == 0 {
		attachments = nil
	}
	return ChannelHistoryMessage{MessageID: message.Timestamp, ThreadID: message.ThreadTimestamp, UserID: message.User, BotID: message.BotID, Text: message.Text, Attachments: attachments}
}
func channelHistoryTimestamp(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

// SlackMessageTime parses timestamps without floating-point precision loss.
func SlackMessageTime(timestamp string) (time.Time, error) {
	seconds, fraction, ok := strings.Cut(timestamp, ".")
	if !ok || len(fraction) < 1 || len(fraction) > 9 || len(seconds) > 12 {
		return time.Time{}, fmt.Errorf("invalid Slack message timestamp")
	}
	for _, c := range seconds + fraction {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("invalid Slack message timestamp")
		}
	}
	sec, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}, fmt.Errorf("invalid Slack message timestamp")
	}
	nanos, err := strconv.ParseInt(fraction+strings.Repeat("0", 9-len(fraction)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid Slack message timestamp")
	}
	return time.Unix(sec, nanos), nil
}
func (s *SlackService) ReadChannelHistory(ctx context.Context, channel string, request ChannelHistoryRequest) (*ChannelHistoryResult, error) {
	cli := s.apiClient()
	if s == nil || cli == nil {
		return nil, fmt.Errorf("Slack service unavailable")
	}
	if channel == "" || request.Limit < 1 || request.Limit > 100 || request.Since.IsZero() || request.Before.IsZero() || !request.Since.Before(request.Before) || request.Before.Sub(request.Since) > 24*time.Hour {
		return nil, fmt.Errorf("history requires a channel, limit 1–100, and a time window up to 24 hours")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	oldest, latest := channelHistoryTimestamp(request.Since), channelHistoryTimestamp(request.Before)
	channelLimit := request.Limit
	if request.IncludeThreads && channelLimit > 1 {
		channelLimit = (channelLimit + 1) / 2
	}
	history, err := cli.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channel, Limit: channelLimit, Oldest: oldest, Latest: latest, Inclusive: true})
	if err != nil {
		return nil, fmt.Errorf("Slack channel history: %w; verify channels:history/groups:history and bot channel membership", err)
	}
	result := &ChannelHistoryResult{ChannelID: channel, Messages: []ChannelHistoryMessage{}, Threads: map[string][]ChannelHistoryMessage{}, Truncated: history.HasMore}
	remaining := request.Limit
	// Bound context independently from API pagination. No retries or unbounded
	// reads, and at most five thread calls per analysis invocation.
	add := func(message slack.Message) bool {
		if remaining == 0 {
			result.Truncated = true
			return false
		}
		sentAt, err := SlackMessageTime(message.Timestamp)
		if err != nil || sentAt.Before(request.Since) || sentAt.After(request.Before) {
			return true
		}
		candidate := historyMessage(message)
		raw, _ := json.Marshal(candidate)
		if len(raw) > 8192 {
			candidate.Text = "[Message omitted: exceeds analysis context limit]"
			candidate.Attachments = nil
			result.Truncated = true
		}
		remaining--
		return appendHistoryMessage(result, candidate)
	}
	for _, message := range history.Messages {
		if !add(message) {
			break
		}
	}
	if request.IncludeThreads {
		calls := 0
		for _, message := range history.Messages {
			if message.ReplyCount == 0 {
				continue
			}
			if calls == 5 || remaining == 0 {
				result.Truncated = true
				break
			}
			calls++
			replies, more, _, err := cli.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: message.Timestamp, Limit: remaining + 1, Oldest: oldest, Latest: latest, Inclusive: true})
			if err != nil {
				return nil, fmt.Errorf("Slack thread context: %w", err)
			}
			result.Truncated = result.Truncated || more
			for _, reply := range replies {
				if reply.Timestamp == message.Timestamp {
					continue
				}
				reply.ThreadTimestamp = message.Timestamp
				if !add(reply) {
					break
				}
			}
		}
	}
	return result, nil
}
func appendHistoryMessage(result *ChannelHistoryResult, message ChannelHistoryMessage) bool {
	// Preserve one total byte budget across channel messages and replies.
	if message.ThreadID != "" && message.ThreadID != message.MessageID {
		result.Threads[message.ThreadID] = append(result.Threads[message.ThreadID], message)
	} else {
		result.Messages = append(result.Messages, message)
	}
	raw, _ := json.Marshal(result)
	if len(raw) <= 64*1024 {
		return true
	}
	if message.ThreadID != "" && message.ThreadID != message.MessageID {
		list := result.Threads[message.ThreadID]
		result.Threads[message.ThreadID] = list[:len(list)-1]
	} else {
		result.Messages = result.Messages[:len(result.Messages)-1]
	}
	result.Truncated = true
	return false
}
