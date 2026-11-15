package workspacereads

import (
	"log"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var traceOnce sync.Once
var traceActive atomic.Bool
var traceMu sync.Mutex
var traceCounts = map[string]int64{}

// Opt-in, one-off diagnosis: skip startup, count caller chains for 30 seconds,
// then stop permanently. No file contents, query values, IDs or polling.
func traceRead(file string) {
	traceOnce.Do(func() {
		if os.Getenv("WORKSPACE_READ_TRACE") != "1" {
			return
		}
		log.Printf("[WORKSPACE_READ_TRACE] action=armed delay_s=120 duration_s=30")
		time.AfterFunc(2*time.Minute, func() {
			log.Printf("[WORKSPACE_READ_TRACE] action=started duration_s=30")
			traceActive.Store(true)
			time.AfterFunc(30*time.Second, finishReadTrace)
		})
	})
	if !traceActive.Load() {
		return
	}
	// Only the basename and Go symbols are diagnostic data. Never copy a body,
	// authorization header or request query into a trace.
	file, _, _ = strings.Cut(file, "?")
	key := "file=" + path.Base(file) + " caller=" + readTraceChain()
	traceMu.Lock()
	defer traceMu.Unlock()
	if !traceActive.Load() {
		return
	}
	if _, found := traceCounts[key]; !found && len(traceCounts) >= 128 {
		key = "file=other caller=overflow"
	}
	traceCounts[key]++
}

func readTraceChain() string {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	names := []string{}
	for {
		frame, more := frames.Next()
		if frame.Function != "" && !isHelper(frame.Function) {
			names = append(names, shortName(frame.Function))
			if len(names) == 8 {
				break
			}
		}
		if !more {
			break
		}
	}
	return strings.Join(names, "<")
}

func finishReadTrace() {
	traceMu.Lock()
	traceActive.Store(false)
	counts := traceCounts
	traceCounts = map[string]int64{}
	traceMu.Unlock()
	var total int64
	for key, count := range counts {
		total += count
		log.Printf("[WORKSPACE_READ_TRACE] action=count %s reads=%d", key, count)
	}
	log.Printf("[WORKSPACE_READ_TRACE] action=done duration_s=30 reads=%d", total)
}
