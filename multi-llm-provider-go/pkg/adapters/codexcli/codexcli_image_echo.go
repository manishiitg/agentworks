package codexcli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// Native user-message notifications may echo input data URLs. Remove those
// string values while framing the line, before unmarshaling/enqueuing it. This
// keeps an 80 MB image turn out of the host's event queue and JSON buffers.
// Normal strings, escapes, and the existing 16 MB non-image frame bound stay
// unchanged. The native process still owns its own image-bearing transcript.
func readCodexImageSafeFrame(reader *bufio.Reader) ([]byte, error) {
	const maxFrame = 16 << 20
	frame := make([]byte, 0, 4096)
	inString, escaped := false, false
	for {
		c, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF && len(frame) > 0 {
				return frame, nil
			}
			return nil, err
		}
		if !inString && c == '\n' {
			return frame, nil
		}
		if inString {
			frame = append(frame, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		} else {
			frame = append(frame, c)
			if c == '"' {
				// Compare incrementally: Peek(11) would deadlock on a short
				// final string when the process waits for this frame's response.
				marker := []byte("data:image/")
				prefix := make([]byte, 0, len(marker))
				for _, wanted := range marker {
					next, e := reader.ReadByte()
					if e != nil {
						return nil, e
					}
					prefix = append(prefix, next)
					if next != wanted {
						break
					}
				}

				if bytes.Equal(prefix, []byte("data:image/")) {
					// Base64 is ASCII with no quotes; still honor JSON escapes so a
					// malformed string cannot consume another field/frame silently.
					skipEscape := false
					for {
						b, err := reader.ReadByte()
						if err != nil {
							return nil, err
						}
						if b == '\n' || b == '\r' {
							return nil, errors.New("invalid image echo string")
						}
						if skipEscape {
							skipEscape = false
							continue
						}
						if b == '\\' {
							skipEscape = true
							continue
						}
						if b == '"' {
							break
						}
					}
					frame = append(frame, []byte("[native image omitted]\"")...)
				} else {
					frame = append(frame, prefix...)
					inString = true
					last := prefix[len(prefix)-1]
					if last == '"' {
						inString = false
					} else if last == '\\' {
						escaped = true
					}
				}
			}
		}
		if len(frame) > maxFrame {
			return nil, errors.New("codex app-server non-image frame exceeds 16 MB")
		}
	}
}
