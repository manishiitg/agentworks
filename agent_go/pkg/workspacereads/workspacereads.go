// Package workspacereads counts reads from the workspace API so the logs show
// which files and which code cause them (PLAT-746-853). Each minute with reads
// logs one [WORKSPACE_READS] summary: the busiest files and the functions that
// read them. A request's own reads are counted on its context, for the slow
// request line.
package workspacereads

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Request counts the workspace reads made on behalf of one HTTP request.
type Request struct {
	reads atomic.Int64
	nanos atomic.Int64
}

type requestKey struct{}

// WithRequest attaches a fresh counter to ctx.
func WithRequest(ctx context.Context) (context.Context, *Request) {
	counter := &Request{}
	return context.WithValue(ctx, requestKey{}, counter), counter
}

// Reads is the number of reads counted so far.
func (r *Request) Reads() int64 { return r.reads.Load() }

// Millis is their total time.
func (r *Request) Millis() int64 { return time.Duration(r.nanos.Load()).Milliseconds() }

type tally struct {
	count int64
	nanos int64
}

var (
	mu      sync.Mutex
	files   = map[string]*tally{}
	callers = map[string]*tally{}
	total   tally
	once    sync.Once
)

// Record counts one read of path that took elapsed.
func Record(ctx context.Context, path string, elapsed time.Duration) {
	if ctx != nil {
		if counter, ok := ctx.Value(requestKey{}).(*Request); ok {
			counter.reads.Add(1)
			counter.nanos.Add(int64(elapsed))
		}
	}
	caller := callerName()
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	mu.Lock()
	add(files, strings.Trim(path, "/"), elapsed)
	add(callers, caller, elapsed)
	total.count++
	total.nanos += int64(elapsed)
	mu.Unlock()
}

func add(m map[string]*tally, key string, elapsed time.Duration) {
	t := m[key]
	if t == nil {
		t = &tally{}
		m[key] = t
	}
	t.count++
	t.nanos += int64(elapsed)
}

// helper frames are skipped so the caller named is the code that wanted the file.
var helperFrames = []string{
	"workspacereads.", "readFileFromWorkspace", "readWorkspaceFile", "ReadWorkspaceFile",
	"loadDocument", "readFileFromWorkspaceQuery", "readJSONFromWorkspace", "readWorkspaceJSON",
}

func callerName() string {
	var pcs [16]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var names []string
	for {
		frame, more := frames.Next()
		name := frame.Function
		if name != "" && !isHelper(name) {
			names = append(names, shortName(name))
			if len(names) == 2 {
				break
			}
		}
		if !more {
			break
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, "<")
}

func isHelper(name string) bool {
	for _, helper := range helperFrames {
		if strings.Contains(name, helper) {
			return true
		}
	}
	return false
}

// shortName drops the module path: ".../cmd/server.(*StreamingAPI).foo" -> "server.(*StreamingAPI).foo".
func shortName(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// StartSummary logs a summary every interval while reads happen.
func StartSummary(interval time.Duration, logf func(format string, args ...any)) {
	once.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				mu.Lock()
				snapshotFiles, snapshotCallers, snapshotTotal := files, callers, total
				files, callers, total = map[string]*tally{}, map[string]*tally{}, tally{}
				mu.Unlock()
				if snapshotTotal.count == 0 {
					continue
				}
				ms := time.Duration(snapshotTotal.nanos).Milliseconds()
				logf("[WORKSPACE_READS] last_%s reads=%d read_ms=%d top_files=%s",
					interval, snapshotTotal.count, ms, top(snapshotFiles, 8))
				logf("[WORKSPACE_READS] last_%s top_callers=%s", interval, top(snapshotCallers, 8))
			}
		}()
	})
}

func top(m map[string]*tally, n int) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]].count != m[keys[j]].count {
			return m[keys[i]].count > m[keys[j]].count
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = fmt.Sprintf("%s:%d/%dms", key, m[key].count, time.Duration(m[key].nanos).Milliseconds())
	}
	return strings.Join(parts, ", ")
}
