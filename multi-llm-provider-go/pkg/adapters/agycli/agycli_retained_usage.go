package agycli

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AGY's bounded Run sums the usage of the assistant steps it ran (agyMarkUsageReported records the newest step it
// counted). A follow-up typed into the running terminal adds steps nobody reads; this registry keeps the newest step
// already reported per conversation so the retained-turn reader reports each step once.
var agyReportedUsage = struct {
	sync.Mutex
	idx map[string]int
}{idx: map[string]int{}}

func agyMarkUsageReported(conversationID string, idx int) {
	agyReportedUsage.Lock()
	defer agyReportedUsage.Unlock()
	if old, ok := agyReportedUsage.idx[conversationID]; !ok || idx > old {
		agyReportedUsage.idx[conversationID] = idx
	}
}

// ReadRetainedTurnUsage returns the usage of each assistant step after the newest of the pre-send baseline of the
// latest live input and the newest step already reported (oldest first), read from a snapshot of the conversation
// database. Read-only; it marks what it returns.
func ReadRetainedTurnUsage(ownerSessionID string, _ time.Time) []llmtypes.RetainedTurnUsage {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	session, ok := activeAgyInteractiveSession(ownerSessionID)
	if !ok || session == nil {
		log.Printf("[RETAINED_USAGE] agy owner=%s skipped: no terminal registered for this owner", ownerSessionID)
		return nil
	}
	session.durableMu.Lock()
	if len(session.pendingDurable) == 0 {
		session.durableMu.Unlock()
		log.Printf("[RETAINED_USAGE] agy owner=%s skipped: no live input recorded", ownerSessionID)
		return nil
	}
	receipt := session.pendingDurable[len(session.pendingDurable)-1]
	session.durableMu.Unlock()
	conversationID := receipt.conversationID
	if conversationID == "" {
		conversationID = agyDiscoverConversationID(session.createdAt, receipt.message, session.transcriptHome)
	}
	if conversationID == "" {
		log.Printf("[RETAINED_USAGE] agy owner=%s skipped: conversation not resolved", ownerSessionID)
		return nil
	}
	since := receipt.baselineIdx
	agyReportedUsage.Lock()
	if reported, ok := agyReportedUsage.idx[conversationID]; ok && reported > since {
		since = reported
	}
	agyReportedUsage.Unlock()
	var out []llmtypes.RetainedTurnUsage
	last := since
	for _, step := range agyUsageSteps(conversationID, since, session.transcriptHome) {
		if step.input+step.output == 0 {
			continue
		}
		usage := llmtypes.Usage{InputTokens: step.input, OutputTokens: step.output, TotalTokens: step.input + step.output}
		info := llmtypes.GenerationInfo{InputTokens: intPtrAgy(step.input), OutputTokens: intPtrAgy(step.output), TotalTokens: intPtrAgy(usage.TotalTokens),
			Additional: map[string]interface{}{"provider": "agy-cli"}}
		if step.thinking > 0 {
			thinking := step.thinking
			usage.ThoughtsTokens = &thinking
			info.ReasoningTokens = &thinking
		}
		agyAttachCost(&info, session.model, usage)
		id := fmt.Sprintf("%s:%d", conversationID, step.idx)
		log.Printf("[RETAINED_USAGE] agy owner=%s step=%s input=%d output=%d reported", ownerSessionID, id, step.input, step.output)
		out = append(out, llmtypes.RetainedTurnUsage{ID: id, Usage: usage, Info: info})
		if step.idx > last {
			last = step.idx
		}
	}
	if last > since {
		agyMarkUsageReported(conversationID, last)
	} else {
		log.Printf("[RETAINED_USAGE] agy owner=%s no new assistant-step usage after step %d", ownerSessionID, since)
	}
	return out
}

func intPtrAgy(v int) *int { return &v }
