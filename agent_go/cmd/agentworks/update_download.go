package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"
)

// The small metadata requests keep their existing total timeout. The binary
// download instead times out only when it stops receiving data, so a slow link
// can finish without letting a stalled body read wait forever.
var updateDownloadClient = &http.Client{}

func updateStep(w io.Writer, step string) {
	if w != nil {
		fmt.Fprintln(w, step)
	}
}

func downloadUpdate(url string, progress io.Writer, idleTimeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := time.AfterFunc(idleTimeout, cancel)
	defer idle.Stop()
	failure := func(err error) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("download stopped receiving data; run agentworks update again")
		}
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := updateDownloadClient.Do(req)
	if err != nil {
		return failure(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d for %s", resp.StatusCode, url)
	}
	idle.Reset(idleTimeout)
	const limit = 64 << 20
	total := resp.ContentLength
	if total > limit {
		return nil, fmt.Errorf("server offered an unexpectedly large AgentWorks download")
	}
	live := false
	if f, ok := progress.(*os.File); ok {
		live = term.IsTerminal(int(f.Fd()))
	}
	if live {
		defer fmt.Fprintln(progress)
	}
	lastTick, lastBucket := time.Time{}, -1
	report := func(received int, done bool) {
		if progress == nil {
			return
		}
		bucket := received / (1 << 20)
		line := fmt.Sprintf("Download: %.1f MB received", float64(received)/1e6)
		if total > 0 {
			percent := int(int64(received) * 100 / total)
			bucket = percent / 10
			line = fmt.Sprintf("Download: %d%% (%.1f of %.1f MB)", percent, float64(received)/1e6, float64(total)/1e6)
		}
		if live && (done || time.Since(lastTick) >= 200*time.Millisecond) {
			fmt.Fprintf(progress, "\r%s   ", line)
			lastTick = time.Now()
		} else if !live && (bucket > lastBucket || (done && total <= 0)) {
			fmt.Fprintln(progress, line)
			lastBucket = bucket
		}
	}
	buf := make([]byte, 0, 1<<20)
	chunk := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			idle.Reset(idleTimeout)
			buf = append(buf, chunk[:n]...)
			if len(buf) > limit {
				return nil, fmt.Errorf("server offered an unexpectedly large AgentWorks download")
			}
			report(len(buf), false)
		}
		if readErr == io.EOF {
			report(len(buf), true)
			return buf, nil
		}
		if readErr != nil {
			return failure(readErr)
		}
	}
}
