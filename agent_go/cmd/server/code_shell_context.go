package server

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/commandlabel"
)

const codeShellContextPrefix = "\x1b]6973;"

var codeShellContextID = regexp.MustCompile(`^[a-f0-9-]{36}-[1-9][0-9]{0,8}$`)

type codeShellContextCommand struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// A tiny incremental OSC filter. Raw command text lives only in this bounded
// frame, before the existing display-label formatter; neither argv nor output
// is retained. Ordinary terminal bytes pass through unchanged.
type codeShellContextStream struct {
	shellID  string
	pending  []byte
	dropping bool
}

func (s *codeShellContextStream) feed(data []byte) []byte {
	s.pending = append(s.pending, data...)
	var out []byte
	for len(s.pending) > 0 {
		if s.dropping {
			end := bytes.IndexByte(s.pending, '\a')
			if end < 0 {
				s.pending = nil
				break
			}
			s.pending = s.pending[end+1:]
			s.dropping = false
		}
		start := bytes.Index(s.pending, []byte(codeShellContextPrefix))
		if start < 0 {
			keep := 0
			for n := 1; n < len(codeShellContextPrefix) && n <= len(s.pending); n++ {
				if bytes.HasSuffix(s.pending, []byte(codeShellContextPrefix[:n])) {
					keep = n
				}
			}
			out = append(out, s.pending[:len(s.pending)-keep]...)
			s.pending = append([]byte(nil), s.pending[len(s.pending)-keep:]...)
			break
		}
		out = append(out, s.pending[:start]...)
		s.pending = s.pending[start:]
		end := bytes.IndexByte(s.pending, '\a')
		if end < 0 {
			if len(s.pending) > 48<<10 {
				s.pending = nil
				s.dropping = true
			}
			break
		}
		if end <= 48<<10 {
			out = append(out, s.marker(string(s.pending[len(codeShellContextPrefix):end]))...)
		}
		s.pending = s.pending[end+1:]
	}
	return out
}

func (s *codeShellContextStream) marker(raw string) []byte {
	fields := strings.SplitN(raw, ";", 3)
	if len(fields) != 3 || !codeShellContextID.MatchString(fields[1]) {
		return nil
	}
	id := fields[1]
	var label string
	var exit *int
	switch fields[0] {
	case "C":
		command, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil || len(command) == 0 || len(command) > 32768 {
			return nil
		}
		label = commandlabel.Label(string(command))
		if label == "" {
			return nil
		}
	case "D":
		n, err := strconv.Atoi(fields[2])
		if err != nil || n < 0 || n > 255 {
			return nil
		}
		exit = &n
	default:
		return nil
	}
	codeShells.Lock()
	defer codeShells.Unlock()
	state := codeShells.byID[s.shellID]
	if state == nil {
		return nil
	}
	found := -1
	for i, command := range state.contextCommands {
		if command.ID == id {
			found = i
			break
		}
	}
	if fields[0] == "C" {
		if found < 0 {
			if len(state.contextCommands) >= 64 {
				state.contextCommands = state.contextCommands[1:]
			}
			state.contextCommands = append(state.contextCommands, codeShellContextCommand{ID: id, Command: label})
		} else if state.contextCommands[found].Command != label {
			return nil
		}
		return []byte(codeShellContextPrefix + "C;" + id + ";" + base64.StdEncoding.EncodeToString([]byte(label)) + "\a")
	}
	if found < 0 {
		return nil
	}
	state.contextCommands[found].ExitCode = exit
	return []byte(codeShellContextPrefix + "D;" + id + ";" + strconv.Itoa(*exit) + "\a")
}

func codeShellContextCommands(shellID string) []codeShellContextCommand {
	codeShells.Lock()
	defer codeShells.Unlock()
	if state := codeShells.byID[shellID]; state != nil {
		return append([]codeShellContextCommand(nil), state.contextCommands...)
	}
	return nil
}
