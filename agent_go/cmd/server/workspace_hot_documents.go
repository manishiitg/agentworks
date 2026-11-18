package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	workspaceutils "github.com/manishiitg/coding-agent-loop/workspace/utils"
)

const hotDocumentLimit = 4 << 20
const hotDocumentsBudget = 16 << 20

type hotDocumentStamp struct{ file os.FileInfo }
type hotDocumentEntry struct {
	stamp   hotDocumentStamp
	content string
	// missing: the file was absent; stamp is then its parent folder, whose
	// change time moves whenever an entry is created, renamed or removed.
	missing bool
}

type hotDocumentState int

const (
	hotIneligible hotDocumentState = iota
	hotPresent
	hotMissing
)

var hotDocuments = struct {
	sync.Mutex
	entries                     map[string]hotDocumentEntry
	bytes                       int
	epoch                       uint64
	hits, misses, invalidations int
	lastLog                     time.Time
}{entries: map[string]hotDocumentEntry{}}

func hotDocumentPath(file string) bool {
	switch path.Base(file) {
	case "product.json", "workflow.json", "product-conversations.json",
		// #3147: per-Crew action files are read for every Crew on each
		// scheduler/watcher pass, and most Crews have none of them.
		"schedules.json", "functions.json", "webhooks.json":
		return true
	case "user-product-access.json", "users.json":
		return file == "config/"+path.Base(file)
	}
	return false
}

// Match a colocated workspace service's own path resolver in both deployment modes. No remote filesystem
// assumptions, query/tail views, or file byte reads on a hit. An absent file
// is remembered only against its existing parent folder. All application
// admission remains outside this byte cache.
func localHotDocument(file string) (string, hotDocumentStamp, hotDocumentState) {
	if !hotDocumentPath(file) {
		return "", hotDocumentStamp{}, hotIneligible
	}
	endpoint, err := url.Parse(getWorkspaceAPIURL())
	if err != nil {
		return "", hotDocumentStamp{}, hotIneligible
	}
	host := endpoint.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", hotDocumentStamp{}, hotIneligible
	}
	physical, err := workspaceutils.ResolveUserPath(fsutil.WorkspaceDocsRoot(), file, "")
	if err != nil {
		return "", hotDocumentStamp{}, hotIneligible
	}
	stamp, state := statHotDocument(physical)
	return physical, stamp, state
}

// statHotDocument stamps a present regular file, or the parent folder of an
// absent one. Anything else (no change time, other errors) stays uncached.
func statHotDocument(physical string) (hotDocumentStamp, hotDocumentState) {
	info, err := os.Stat(physical)
	if err == nil {
		if _, ok := hotChangeTime(info); ok && info.Mode().IsRegular() && info.Size() <= hotDocumentLimit {
			return hotDocumentStamp{file: info}, hotPresent
		}
		return hotDocumentStamp{}, hotIneligible
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return hotDocumentStamp{}, hotIneligible
	}
	parent, err := os.Stat(filepath.Dir(physical))
	if err != nil || !parent.IsDir() {
		return hotDocumentStamp{}, hotIneligible
	}
	if _, ok := hotChangeTime(parent); !ok {
		return hotDocumentStamp{}, hotIneligible
	}
	return hotDocumentStamp{file: parent}, hotMissing
}

// Linux and macOS expose ctime; atime is deliberately excluded because a
// read itself may update it. Unsupported stat formats stay uncached.
func hotChangeTime(info os.FileInfo) ([2]int64, bool) {
	if info == nil {
		return [2]int64{}, false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return [2]int64{}, false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.Struct {
			sec, nsec := field.FieldByName("Sec"), field.FieldByName("Nsec")
			if sec.IsValid() && nsec.IsValid() && sec.CanInt() && nsec.CanInt() {
				return [2]int64{sec.Int(), nsec.Int()}, true
			}
		}
	}
	return [2]int64{}, false
}

func sameHotStamp(a, b hotDocumentStamp) bool {
	ac, aok := hotChangeTime(a.file)
	bc, bok := hotChangeTime(b.file)
	return aok && bok && ac == bc && os.SameFile(a.file, b.file) &&
		a.file.Size() == b.file.Size() && a.file.ModTime().Equal(b.file.ModTime()) && a.file.Mode() == b.file.Mode()
}

func hotDocumentLogLocked() {
	if time.Since(hotDocuments.lastLog) < time.Minute {
		return
	}
	log.Printf("[WORKSPACE_DOC_CACHE] action=batch hits=%d misses=%d invalidations=%d entries=%d bytes=%d", hotDocuments.hits, hotDocuments.misses, hotDocuments.invalidations, len(hotDocuments.entries), hotDocuments.bytes)
	hotDocuments.lastLog = time.Now()
	hotDocuments.hits, hotDocuments.misses, hotDocuments.invalidations = 0, 0, 0
}

func readFileFromWorkspaceHotDocument(ctx context.Context, file string, load func() (string, bool, error)) (string, bool, error) {
	physical, before, state := localHotDocument(file)
	if state == hotIneligible {
		return load()
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	// Endpoint participates too: a fixture/service replacement never inherits
	// bytes fetched from another service. Stat changes evict without a TTL.
	key := getWorkspaceAPIURL() + "\x00" + physical
	hotDocuments.Lock()
	epoch := hotDocuments.epoch
	if cached, ok := hotDocuments.entries[key]; ok && cached.missing == (state == hotMissing) && sameHotStamp(cached.stamp, before) {
		hotDocuments.hits++
		hotDocumentLogLocked()
		hotDocuments.Unlock()
		if cached.missing {
			return "", false, nil
		}
		return cached.content, true, nil
	}
	hotDocuments.misses++
	hotDocumentLogLocked()
	hotDocuments.Unlock()
	content, exists, err := load()
	if err != nil || len(content) > hotDocumentLimit || exists != (state == hotPresent) {
		return content, exists, err
	}
	// A successful HTTP read must agree with the local file before we associate
	// its bytes with that stat. This also refuses a mismatched docs-root setup.
	if exists {
		handle, e := os.Open(physical)
		if e != nil {
			return content, exists, err
		}
		raw, e := io.ReadAll(io.LimitReader(handle, hotDocumentLimit+1))
		_ = handle.Close()
		if e != nil || string(raw) != content {
			return content, exists, err
		}
	}
	after, afterState := statHotDocument(physical)
	if afterState != state || !sameHotStamp(before, after) {
		return content, exists, err
	}
	hotDocuments.Lock()
	defer hotDocuments.Unlock()
	if epoch != hotDocuments.epoch {
		return content, exists, err
	}
	if old, ok := hotDocuments.entries[key]; ok {
		hotDocuments.bytes -= len(old.content)
		delete(hotDocuments.entries, key)
	}
	if len(hotDocuments.entries) >= 4096 || hotDocuments.bytes+len(content) > hotDocumentsBudget {
		hotDocuments.entries = map[string]hotDocumentEntry{}
		hotDocuments.bytes = 0
	}
	hotDocuments.entries[key] = hotDocumentEntry{stamp: after, content: content, missing: !exists}
	hotDocuments.bytes += len(content)
	return content, exists, nil
}

// Invalidate before and after store writes. The generation refuses a racing
// miss's fill, including same-size writes with deliberately preserved mtimes.
func invalidateHotDocuments(file string, folder bool) {
	file = strings.TrimPrefix(file, "/")
	if !folder && !hotDocumentPath(file) {
		return
	}
	hotDocuments.Lock()
	defer hotDocuments.Unlock()
	hotDocuments.epoch++
	hotDocuments.invalidations++
	// A mutation to one project must not evict every other project's snapshot.
	// Keep the global generation so a racing miss cannot refill changed bytes.
	physical, err := workspaceutils.ResolveUserPath(fsutil.WorkspaceDocsRoot(), file, "")
	if err != nil {
		hotDocuments.entries = map[string]hotDocumentEntry{}
		hotDocuments.bytes = 0
		return
	}
	for key, entry := range hotDocuments.entries {
		_, cachedPath, _ := strings.Cut(key, "\x00")
		if cachedPath == physical || folder && strings.HasPrefix(cachedPath, physical+string(os.PathSeparator)) {
			delete(hotDocuments.entries, key)
			hotDocuments.bytes -= len(entry.content)
		}
	}
}
