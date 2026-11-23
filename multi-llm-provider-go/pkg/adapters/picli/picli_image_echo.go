package picli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// Pi echoes user images in message.content (and get_messages). Strip only the
// image-part data field while framing, before JSON buffers or event queues can
// retain base64. Field order is irrelevant; text/tool arguments remain intact.
// Pi's content-part contract reserves data for image parts. Native transcripts
// still belong to Pi. Keep the existing 16 MiB non-image frame bound.
func readPiImageSafeFrame(r *bufio.Reader) ([]byte, error) {
	type frameLevel struct {
		kind                  byte
		key                   bool
		field, path           string
		imageArray, imagePart bool
	}
	var stack []frameLevel
	frame := make([]byte, 0, 4096)
	inString, escaped, keyString, strip := false, false, false, false
	start, scanned := 0, 0
	for {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && inString {
				return nil, errors.New("unterminated Pi RPC string")
			}
			if err == io.EOF && len(frame) > 0 && !inString {
				return frame, nil
			}
			return nil, err
		}
		scanned++
		if scanned > 128<<20 {
			return nil, errors.New("Pi RPC image frame exceeds 128 MiB")
		}
		if !inString && c == '\n' {
			return frame, nil
		}
		if inString {
			if c == '\n' || c == '\r' {
				return nil, errors.New("invalid Pi RPC string")
			}
			if !strip {
				frame = append(frame, c)
			}
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
				if strip {
					frame = append(frame, []byte("[native image omitted]\"")...)
				}
				if keyString && len(stack) > 0 {
					field := ""
					if len(frame)-start < 64 {
						_ = json.Unmarshal(frame[start:], &field)
					}
					stack[len(stack)-1].field = field
				}
			}
		} else {
			frame = append(frame, c)
			switch c {
			case '"':
				inString = true
				escaped = false
				keyString = false
				strip = false
				start = len(frame) - 1
				if len(stack) > 0 {
					top := stack[len(stack)-1]
					keyString = top.kind == '{' && top.key
					strip = top.kind == '{' && !top.key && top.imagePart && top.field == "data"
				}
			case '{', '[':
				level := frameLevel{kind: c, key: c == '{'}
				if len(stack) > 0 {
					top := stack[len(stack)-1]
					level.path = top.path
					if top.kind == '[' {
						level.path += "[]"
					} else if top.path == "" {
						level.path = top.field
					} else {
						level.path += "." + top.field
					}
					// Unknown/deep paths can never become native message paths.
					if len(level.path) > 96 || top.path == "#" {
						level.path = "#"
					}
					level.imageArray = c == '[' && piNativeImageArrayPath(level.path)
					level.imagePart = c == '{' && top.kind == '[' && top.imageArray
				}
				if len(stack) >= 128 {
					return nil, errors.New("Pi RPC frame nesting exceeds 128")
				}
				stack = append(stack, level)
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case ':':
				if len(stack) > 0 {
					stack[len(stack)-1].key = false
				}
			case ',':
				if len(stack) > 0 && stack[len(stack)-1].kind == '{' {
					stack[len(stack)-1].key = true
					stack[len(stack)-1].field = ""
				}
			}
		}
		if len(frame) > 16<<20 {
			return nil, errors.New("Pi RPC non-image frame exceeds 16 MiB")
		}
	}
}

// These are native message/content locations, not arbitrary content/images
// keys inside a tool's arguments or result details (PR184 review regression).
func piNativeImageArrayPath(path string) bool {
	switch path {
	case "message.content", "messages[].content", "assistantMessageEvent.partial.content",
		"data.messages[].content", "result.content", "toolResults[].content":
		return true
	}
	return false
}
