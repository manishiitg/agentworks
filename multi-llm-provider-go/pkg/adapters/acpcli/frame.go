package acpcli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// Native session/update content can contain base64. Strip data only at its
// exact content-object path, never rawInput/tool arguments. Newline always
// ends a frame: malformed/non-JSON diagnostic lines remain safely skippable.
// Retained frame32MiB, wire128MiB, nesting128; cap violations close the reader.
func readImageSafeFrame(r *bufio.Reader) ([]byte, error) {
	type frameLevel struct {
		kind        byte
		key         bool
		field, path string
		imagePart   bool
	}
	var stack []frameLevel
	frame := make([]byte, 0, 4096)
	inString, escaped, keyString, strip := false, false, false, false
	start, scanned := 0, 0
	for {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && len(frame) > 0 {
				return frame, nil
			}
			return nil, err
		}
		scanned++
		if scanned > 128<<20 {
			return nil, errors.New("ACP image frame exceeds 128 MiB")
		}
		if c == '\n' {
			return frame, nil
		}
		if inString {
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
					level.imagePart = c == '{' && level.path == "params.update.content"
				}
				if len(stack) >= 128 {
					return nil, errors.New("ACP frame nesting exceeds 128")
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
		if len(frame) > 32<<20 {
			return nil, errors.New("ACP non-image frame exceeds 32 MiB")
		}
	}
}
