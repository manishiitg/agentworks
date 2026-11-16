package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
)

var errNativeCopyNotShared = errors.New("native copy is not shared")

type NativeCopyCursor struct {
	Project  string `json:"project"`
	Agent    string `json:"agent"`
	Revision uint64 `json:"revision"`
	Offset   int64  `json:"offset"`
	TailHash string `json:"tail_hash"`
}

// One turn-end job, one bounded delta; only the server's committed receipt
// advances the durable cursor. A lost receipt resumes from server status.
func (c *Client) SyncNativeCopy(ctx context.Context, a AgentAttachment, old NativeCopyCursor) (NativeCopyCursor, error) {
	raw, err := c.request(ctx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "offline_copy_status", "agent_name": a.AgentName, "attachment_id": a.ID, "provider": a.Provider, "session": a.SessionID}, true)
	if err != nil {
		return old, err
	}
	var status struct {
		NativeCopyCursor
		Shared bool `json:"shared"`
	}
	if err = json.Unmarshal(raw, &status); err != nil {
		return old, err
	}
	if !status.Shared {
		return old, errNativeCopyNotShared
	}
	cur := status.NativeCopyCursor
	if cur.Project == "" || cur.Agent == "" || cur.Offset < 0 {
		return old, fmt.Errorf("invalid copy status")
	}
	if old.Project != "" && (old.Project != cur.Project || old.Agent != cur.Agent || old.Offset > cur.Offset || old.Revision > cur.Revision) {
		return old, fmt.Errorf("copy cursor changed")
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=native_capture_started revision=%d", cur.Project, cur.Agent, a.SessionID, cur.Revision)
	delta, tail, sourceSize, err := ReadNativeJournalDelta(a, cur.Offset, cur.TailHash)
	if err != nil {
		return old, err
	}
	if len(delta) == 0 {
		return cur, nil
	}
	content := map[string][]byte{}
	file, err := chunks.Split(bytes.NewReader(delta), int64(len(delta)), false, func(id string, data []byte) error { content[id] = bytes.Clone(data); return nil })
	if err != nil {
		return old, err
	}
	base := "/api/crew-teams/" + url.PathEscape(cur.Project) + "/agents/" + url.PathEscape(cur.Agent) + "/offline-copy/"
	body := map[string]any{"provider": a.Provider, "session": a.SessionID, "attachment": a.ID, "base_revision": cur.Revision, "offset": cur.Offset, "previous_tail_hash": cur.TailHash, "delta": file, "source_size": sourceSize}
	raw, err = c.request(ctx, http.MethodPost, base+"plan", body, true)
	if err != nil {
		return old, err
	}
	var plan struct {
		Missing []string `json:"missing"`
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		return old, err
	}
	query := url.Values{"provider": {a.Provider}, "session": {a.SessionID}, "attachment": {a.ID}}
	var frames bytes.Buffer
	flush := func() error {
		if frames.Len() == 0 {
			return nil
		}
		_, e := c.send(ctx, http.MethodPut, base+"chunks?"+query.Encode(), "application/octet-stream", frames.Bytes(), true)
		frames.Reset()
		return e
	}
	for _, id := range plan.Missing {
		data, ok := content[id]
		if !ok {
			return old, fmt.Errorf("copy plan requested an unknown chunk")
		}
		if frames.Len()+len(data)+36 > chunks.BatchBytes {
			if err = flush(); err != nil {
				return old, err
			}
		}
		if err = chunks.WriteFrame(&frames, id, data); err != nil {
			return old, err
		}
	}
	if err = flush(); err != nil {
		return old, err
	}
	raw, err = c.request(ctx, http.MethodPost, base+"commit", body, true)
	if err != nil {
		return old, err
	}
	var ack NativeCopyCursor
	if err = json.Unmarshal(raw, &ack); err != nil {
		return old, err
	}
	if ack.Offset != cur.Offset+int64(len(delta)) || ack.TailHash != tail || ack.Revision <= cur.Revision {
		return old, fmt.Errorf("copy acknowledgement mismatch")
	}
	ack.Project, ack.Agent = cur.Project, cur.Agent
	return ack, nil
}

// Called by a proved turn-end hook or successful exact-session join, under b.mu.
// Network/journal I/O never
// holds that lock or the hook open. Overlapping hook events coalesce, no timer.
func (b *localBridge) queueNativeCopy(ctx context.Context, s *localSession) {
	if s.nativeCopyRunning {
		s.nativeCopyAgain = true
		return
	}
	s.nativeCopyRunning = true
	a, client, old := s.Attachment, s.client, s.NativeCopyCursor
	b.workers.Add(1)
	go func() {
		defer b.workers.Done()
		call, cancel := context.WithTimeout(ctx, 20*time.Second)
		next, err := client.SyncNativeCopy(call, a, old)
		if err == nil && next.Project != "" && next.Agent != "" {
			if handbackErr := b.processNativeHandback(call, s, a, client, next); handbackErr != nil {
				log.Printf("[AGENT_OFFLINE_COPY] agent=%s attachment=%s event=handback_pending", a.AgentID, a.ID)
			}
		}
		cancel()
		b.mu.Lock()
		defer b.mu.Unlock()
		s.nativeCopyRunning = false
		if b.closed || s.Offline || s.Attachment.ID != a.ID {
			return
		}
		outcome := "synced"
		if errors.Is(err, errNativeCopyNotShared) {
			outcome = "skipped"
		} else if err != nil {
			outcome = "refused"
		} else {
			s.NativeCopyCursor = next
			_ = b.save()
		}
		log.Printf("[AGENT_OFFLINE_COPY] agent=%s session=%s attachment=%s event=%s revision=%d offset=%d", a.AgentID, a.SessionID, a.ID, outcome, s.NativeCopyCursor.Revision, s.NativeCopyCursor.Offset)
		if s.nativeCopyAgain {
			s.nativeCopyAgain = false
			b.queueNativeCopy(ctx, s)
		}
	}()
}
