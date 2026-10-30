package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Read only recent complete lines. A tail beginning inside a line must not
// expose a credential whose label was outside the retained range.
func helperStartupLines(path string, since int64, count int) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("helper startup log is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	start := max(since, info.Size()-(64<<10))
	if start >= info.Size() {
		return nil, nil
	}
	scanner := bufio.NewScanner(io.NewSectionReader(f, start, info.Size()-start))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if start > since {
		scanner.Scan() // discard the partial first line
	}
	var lines []string
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
			if len(lines) > count {
				lines = lines[1:]
			}
		}
	}
	return lines, scanner.Err()
}

func shortStartupDetail(text string) string {
	// Redact the entire value before clipping: a clipped credential may no
	// longer match its original token pattern.
	text = strings.Join(strings.Fields(redactDebug(text)), " ")
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:199]) + "…"
	}
	return text
}

func helperStartupDetail(path string, since int64) string {
	lines, err := helperStartupLines(path, since, 1)
	if err != nil {
		return shortStartupDetail("Startup detail unavailable: " + err.Error())
	}
	if len(lines) == 0 {
		return "No new startup detail was recorded."
	}
	return shortStartupDetail(lines[0])
}

func writeUpdateMessagingStatus(out io.Writer, root string, startupErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"})
	if err == nil {
		build := reply.Version
		if build == "" {
			build = "unknown"
		}
		fmt.Fprintf(out, "AgentWorks messaging: ready (build %s)\n", build)
		return
	}
	if startupErr != nil {
		err = startupErr
	}
	fmt.Fprintf(out, "AgentWorks messaging: not ready: %s\n", shortStartupDetail(err.Error()))
}

func reportUpdateMessaging(o *options, startupErr error) {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		fmt.Fprintf(o.stderr, "AgentWorks messaging: not ready: %s\n", shortStartupDetail(err.Error()))
		return
	}
	writeUpdateMessagingStatus(o.stderr, root, startupErr)
}
