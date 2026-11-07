// Package chatvisuals formats saved presentation data. It has no credentials,
// network client, product access or agent execution tools.
package chatvisuals

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed slack-chat.cjs
var formatter []byte

var scriptPath = sync.OnceValues(func() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root = filepath.Join(root, "agentworks", "renderers")
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	name := filepath.Join(root, fmt.Sprintf("slack-%x.cjs", sha256.Sum256(formatter)))
	if err = os.WriteFile(name, formatter, 0600); err != nil {
		return "", err
	}
	return name, nil
})

type Reply struct {
	Blocks []json.RawMessage `json:"blocks"`
	Text   string            `json:"text"`
	Error  string            `json:"error"`
}

func Format(ctx context.Context, content string, final bool) (Reply, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return Reply{}, errors.New("visual formatter unavailable")
	}
	script, err := scriptPath()
	if err != nil {
		return Reply{}, errors.New("visual formatter unavailable")
	}
	// A wedged formatter cannot hold up a person's reply. The caller delivers
	// normal prose and a readable visual notice when this bounded call fails.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data, err := json.Marshal(struct {
		Content string `json:"content"`
		Final   bool   `json:"final"`
	}{content, final})
	if err != nil {
		return Reply{}, err
	}
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Stdin = strings.NewReader(string(data))
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return Reply{}, errors.New("visual formatter failed")
	}
	var reply Reply
	if err = json.Unmarshal(out, &reply); err != nil || reply.Error != "" || len(reply.Blocks) == 0 || len(reply.Blocks) > 50 {
		return reply, errors.New("visual formatter failed")
	}
	return reply, nil
}

var fenceLine = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
var visualStart = regexp.MustCompile("(?m)^ {0,3}(?:`{3,}|~{3,})[ \\t]*chat-ui(?:[ \\t\\r]|$)")

func HasVisual(content string) bool { return visualStart.MatchString(content) }

// MapProse preserves visual source byte-for-byte while ordinary file/link
// formatting runs. On formatter failure Fallback replaces only those sources.
func MapProse(content string, prose func(string) string, visual func(string) string) string {
	lines := strings.SplitAfter(content, "\n")
	var out, part strings.Builder
	marker, ordinary := "", ""
	flush := func(isVisual bool) {
		if isVisual {
			out.WriteString(visual(part.String()))
		} else {
			out.WriteString(prose(part.String()))
		}
		part.Reset()
	}
	for _, line := range lines {
		match := fenceLine.FindStringSubmatch(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		fence, info := "", ""
		if match != nil {
			fence = match[1]
			info = strings.TrimSpace(match[2])
		}
		if marker != "" {
			part.WriteString(line)
			if fence != "" && fence[0] == marker[0] && len(fence) >= len(marker) && info == "" {
				flush(true)
				marker = ""
			}
		} else if ordinary != "" {
			part.WriteString(line)
			if fence != "" && fence[0] == ordinary[0] && len(fence) >= len(ordinary) && info == "" {
				ordinary = ""
			}
		} else if fence != "" && len(strings.Fields(info)) > 0 && strings.Fields(info)[0] == "chat-ui" {
			flush(false)
			marker = fence
			part.WriteString(line)
		} else {
			part.WriteString(line)
			if fence != "" {
				ordinary = fence
			}
		}
	}
	flush(marker != "")
	return out.String()
}

func Fallback(content string, final bool) string {
	notice := "Preparing visual…\n"
	if final {
		notice = "This visual could not be displayed. Ask for a text summary.\n"
	}
	return strings.TrimSpace(MapProse(content, func(text string) string { return text }, func(string) string { return notice }))
}
