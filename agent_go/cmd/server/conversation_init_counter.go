package server

import (
	"log"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Each chat open sets up its project's code folder and database (initializeProductConversationWorkspace); a Mac was
// seen doing it about 5 times a second with nobody typing. Once a minute this logs how often, by the code that asked,
// so a loop shows its source: [CONVERSATION_INIT] per_minute=N by="caller:N, ...".
var conversationInits = struct {
	sync.Mutex
	byCaller map[string]int
	started  bool
}{byCaller: map[string]int{}}

func countConversationInit() {
	caller := "unknown"
	pcs := make([]uintptr, 6)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	names := []string{}
	for frame, more := frames.Next(); ; frame, more = frames.Next() {
		name := frame.Function[strings.LastIndex(frame.Function, ".")+1:]
		if name != "" && name != "resolveAgentProfileConversation" && !strings.HasPrefix(name, "func") {
			names = append(names, name)
		}
		if !more || len(names) == 2 {
			break
		}
	}
	if len(names) > 0 {
		caller = strings.Join(names, "<")
	}
	conversationInits.Lock()
	defer conversationInits.Unlock()
	conversationInits.byCaller[caller]++
	if conversationInits.started {
		return
	}
	conversationInits.started = true
	go func() {
		for range time.Tick(time.Minute) {
			conversationInits.Lock()
			counts := conversationInits.byCaller
			conversationInits.byCaller = map[string]int{}
			conversationInits.Unlock()
			if len(counts) == 0 {
				continue
			}
			total, parts := 0, []string{}
			for name, n := range counts {
				total += n
				parts = append(parts, name+":"+strconv.Itoa(n))
			}
			sort.Strings(parts)
			log.Printf("[CONVERSATION_INIT] per_minute=%d by=%q", total, strings.Join(parts, ", "))
		}
	}()
}
