package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// PLAT-746: native journals can be many gigabytes. UI history is a recent
// window, not the provider's context. Never materialize an entire journal.
const nativeHistoryReadBytes int64 = 8 << 20
const nativeHistoryMessages = 200
const nativeHistoryTextBytes = 512 << 10
const nativeHistoryMessageBytes = 16 << 10

type nativeHistoryRow struct {
	message builderConversationMessage
	stamp   time.Time
}
type nativeHistoryCache struct {
	mu       sync.Mutex
	info     os.FileInfo
	offset   int64
	rows     []nativeHistoryRow
	stamp    time.Time
	complete bool // Every visible prompt is retained verbatim from the file start.
}

var nativeHistoryWindows = struct {
	sync.Mutex
	entries map[string]*nativeHistoryCache
}{entries: make(map[string]*nativeHistoryCache)}

func nativeHistoryWindow(path, provider string) *nativeHistoryCache {
	nativeHistoryWindows.Lock()
	defer nativeHistoryWindows.Unlock()
	key := provider + "\x00" + path
	if cached := nativeHistoryWindows.entries[key]; cached != nil {
		return cached
	}
	// A small fixed cache; eviction only loses an I/O optimization.
	if len(nativeHistoryWindows.entries) >= 16 {
		for old := range nativeHistoryWindows.entries {
			delete(nativeHistoryWindows.entries, old)
			break
		}
	}
	cached := &nativeHistoryCache{}
	nativeHistoryWindows.entries[key] = cached
	return cached
}

func boundedNativeText(text string) string {
	if len(text) <= nativeHistoryMessageBytes {
		return text
	}
	cut := nativeHistoryMessageBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func (cache *nativeHistoryCache) trim() {
	cutoff := cache.stamp.Add(-72 * time.Hour)
	total := 0
	start := len(cache.rows)
	for start > 0 && len(cache.rows)-start < nativeHistoryMessages {
		row := cache.rows[start-1]
		size := len(builderConversationMessageText(row.message))
		if row.stamp.Before(cutoff) || total+size > nativeHistoryTextBytes {
			break
		}
		total += size
		start--
	}
	if start > 0 {
		cache.complete = false
	}
	cache.rows = append([]nativeHistoryRow(nil), cache.rows[start:]...)
}

func readRecentNativeTranscript(path, provider string, since time.Time) ([]builderConversationMessage, time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cache := nativeHistoryWindow(path, provider)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	unchanged := cache.info != nil && os.SameFile(cache.info, info) && info.Size() == cache.info.Size() && info.ModTime().Equal(cache.info.ModTime())
	if !unchanged {
		start := cache.offset
		reset := cache.info == nil || !os.SameFile(cache.info, info) || info.Size() < cache.info.Size() || (info.Size() == cache.info.Size() && !info.ModTime().Equal(cache.info.ModTime())) || info.Size()-start > nativeHistoryReadBytes
		if reset {
			start = max(int64(0), info.Size()-nativeHistoryReadBytes)
		}
		// ReadAt a bounded tail in blocks. Discard an incomplete first/last row.
		raw := make([]byte, info.Size()-start)
		for at := 0; at < len(raw); {
			if err := ctx.Err(); err != nil {
				return nil, time.Time{}, err
			}
			n, readErr := file.ReadAt(raw[at:min(at+(64<<10), len(raw))], start+int64(at))
			at += n
			if readErr != nil && readErr != io.EOF {
				return nil, time.Time{}, readErr
			}
			if n == 0 {
				raw = raw[:at]
				break
			}
		}
		rows, stamp, complete := cache.rows, cache.stamp, cache.complete
		if reset {
			rows, stamp, complete = nil, time.Time{}, start == 0
		}
		end := bytes.LastIndexByte(raw, '\n') + 1
		consumed := end
		rowOffset := start
		if reset && start > 0 {
			first := bytes.IndexByte(raw[:end], '\n') + 1
			rowOffset += int64(first)
			raw = raw[first:end]
		} else {
			raw = raw[:end]
		}
		for len(raw) > 0 {
			if err := ctx.Err(); err != nil {
				return nil, time.Time{}, err
			}
			next := bytes.IndexByte(raw, '\n')
			line := raw[:next]
			identity := fmt.Sprintf("%s:%d:%x", provider, rowOffset, sha256.Sum256(line))
			rowOffset += int64(next + 1)
			raw = raw[next+1:]
			// Large tool/image rows are not visible prose; skip before JSON decoding.
			if len(line) > 1<<20 || !json.Valid(line) {
				complete = false
				continue
			}
			var message builderConversationMessage
			var at time.Time
			var ok bool
			if provider == "codex-cli" {
				message, at, ok = parseCodexTranscriptLine(line)
			} else {
				message, at, ok = parseClaudeTranscriptLine(line)
			}
			if !ok {
				continue
			}
			message.NativeID = identity
			message.nativeTextBytes = len(builderConversationMessageText(message))
			text := builderConversationMessageText(message)
			if len(text) > nativeHistoryMessageBytes {
				complete = false
			}
			message.Parts = []builderConversationPart{{Text: boundedNativeText(text)}}
			if at.IsZero() {
				at = info.ModTime()
			}
			if at.After(stamp) {
				stamp = at
			}
			rows = append(rows, nativeHistoryRow{message, at})
			if len(rows) > nativeHistoryMessages*2 {
				complete = false
				rows = append([]nativeHistoryRow(nil), rows[len(rows)-nativeHistoryMessages:]...)
			}
		}
		cache.rows, cache.stamp, cache.offset, cache.info, cache.complete = rows, stamp, start+int64(consumed), info, complete
		cache.trim()
	}
	messages := make([]builderConversationMessage, 0, len(cache.rows))
	for _, row := range cache.rows {
		if row.stamp.After(since) {
			messages = append(messages, row.message)
		}
	}
	return messages, cache.stamp, nil
}

func recentNativePromptTime(path, provider, text string, fallback time.Time) time.Time {
	if _, _, err := readRecentNativeTranscript(path, provider, time.Time{}); err != nil {
		return fallback
	}
	cache := nativeHistoryWindow(path, provider)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for i := len(cache.rows) - 1; i >= 0; i-- {
		row := cache.rows[i]
		if row.message.Role == "human" && strings.Join(strings.Fields(builderConversationMessageText(row.message)), " ") == strings.Join(strings.Fields(text), " ") {
			return row.stamp
		}
	}
	return fallback
}

// A missing prompt in a bounded or truncated view cannot prove non-delivery.
// Other providers retain their existing complete-reader contract.
func nativeHistoryCanProveAbsence(provider, path string) bool {
	if provider != "claude-code" && provider != "codex-cli" {
		return true
	}
	cache := nativeHistoryWindow(path, provider)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.complete && cache.info != nil && cache.offset == cache.info.Size()
}

// Only accepted terminal inputs need their full text. Re-read one bounded row
// at its stable source offset rather than retaining large prompts in the UI cache.
func nativeTerminalInputText(path, provider string, message builderConversationMessage) string {
	excerpt := builderConversationMessageText(message)
	if message.nativeTextBytes <= nativeHistoryMessageBytes {
		return excerpt
	}
	unavailable := fmt.Sprintf("This long message could not be saved in full (%d bytes).\n%s", message.nativeTextBytes, excerpt)
	parts := strings.Split(message.NativeID, ":")
	if len(parts) != 3 || parts[0] != provider {
		return unavailable
	}
	offset, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || offset < 0 {
		return unavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return unavailable
	}
	defer f.Close()
	// The display reader accepts at most 1 MiB per source row. Use the same
	// limit here, checking the hash before persisting any reread content.
	raw, err := bufio.NewReader(io.NewSectionReader(f, offset, (1<<20)+1)).ReadBytes('\n')
	if err != nil || len(raw) > (1<<20)+1 {
		return unavailable
	}
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != parts[2] {
		return unavailable
	}
	var full builderConversationMessage
	var ok bool
	if provider == "codex-cli" {
		full, _, ok = parseCodexTranscriptLine(raw)
	} else {
		full, _, ok = parseClaudeTranscriptLine(raw)
	}
	if !ok || (full.Role != "human" && full.Role != "user") {
		return unavailable
	}
	return builderConversationMessageText(full)
}
