package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

type chatSnapshotReference struct {
	ID        string `json:"snapshot_id"`
	Workspace string `json:"workspace_path"`
	Start     int64  `json:"start_sequence"`
	End       int64  `json:"end_sequence"`
}
type chatFileReview struct {
	Replies   []chatSnapshotReference `json:"replies"`
	First     string                  `json:"first_snapshot_id"`
	Last      string                  `json:"last_snapshot_id"`
	Available bool                    `json:"available"`
	Note      string                  `json:"note,omitempty"`
}

func (api *StreamingAPI) chatFileReviewHistory(ctx context.Context, sessionID, root string) (chatFileReview, error) {
	out := chatFileReview{Replies: []chatSnapshotReference{}}
	history, err := api.eventStore.ReadChatFileHistory(ctx, sessionID)
	if err != nil {
		return out, err
	}
	for _, event := range history.References {
		if event.Data == nil {
			continue
		}
		raw, err := json.Marshal(event.Data.Data)
		if err != nil {
			return out, err
		}
		var ref chatSnapshotReference
		if json.Unmarshal(raw, &ref) != nil || ref.ID == "" || ref.End <= ref.Start || ref.Workspace != root {
			continue
		}
		out.Replies = append(out.Replies, ref)
	}
	sort.Slice(out.Replies, func(i, j int) bool { return out.Replies[i].End < out.Replies[j].End })
	if len(out.Replies) > 0 {
		first, last := out.Replies[0], out.Replies[len(out.Replies)-1]
		out.First, out.Last = first.ID, last.ID
		out.Available = first.ID == history.FirstSnapshotID && history.FirstUserSequence > first.Start && history.FirstUserSequence <= first.End
	}
	if !out.Available {
		out.Note = "The start of this chat was not captured or is no longer retained. Choose an individual saved reply."
	}
	return out, nil
}

// The file route already authorizes the Crew and current paths. Chat history
// has its own read rule; sharing files alone does not grant a private chat.
func (api *StreamingAPI) serveChatFileReview(w http.ResponseWriter, r *http.Request, root, prefix, op, id, file string, store filesnapshots.Store, allow func(string) bool) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if api.eventStore == nil {
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	if status, message := api.authorizeDurableChatRead(r, sessionID, root); status != 0 {
		externalError(w, status, "not_found", message)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), crewSnapshotTimeout)
	defer cancel()
	history, err := api.chatFileReviewHistory(ctx, sessionID, root)
	if err != nil {
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	if op == "snapshots" {
		externalJSON(w, history)
		return
	}
	first, last := history.First, history.Last
	if id != "" {
		found := false
		for _, ref := range history.Replies {
			if ref.ID == id {
				found = true
			}
		}
		if !found {
			externalError(w, 404, "not_found", "This saved reply does not belong to this chat.")
			return
		}
		first, last = id, id
	} else if !history.Available {
		if op == "snapshot_file" || r.URL.Query().Get("line") != "" {
			externalError(w, 404, "snapshot_unavailable", history.Note)
			return
		}
		externalJSON(w, map[string]any{"history": history, "changes": []any{}})
		return
	}
	// A displayed comparison remains pinned while another reply completes.
	if bound := r.URL.Query().Get("through_snapshot_id"); bound != "" {
		found := false
		for _, ref := range history.Replies {
			if ref.ID == bound {
				found = true
			}
		}
		if !found || (id != "" && bound != id) {
			externalError(w, 404, "not_found", "This saved comparison is unavailable.")
			return
		}
		last = bound
	}
	if prefix != "" && file != "" {
		if !strings.HasPrefix(file, prefix) {
			externalError(w, 404, "not_found", "Choose a file from this comparison.")
			return
		}
		file = strings.TrimPrefix(file, prefix)
	}
	comparison, err := store.CompareBetween(ctx, first, last, file, allow)
	if err != nil {
		if file != "" {
			externalError(w, 404, "snapshot_unavailable", "This saved file comparison is unavailable or you cannot read it.")
			return
		}
		history.Available = false
		history.Note = "A saved boundary is no longer available. Choose another retained reply."
		externalJSON(w, map[string]any{"history": history, "changes": []any{}})
		return
	}
	comparison.Record.ExecutionID = ""
	if op == "snapshot_file" {
		content, err := store.ReadComparedFile(ctx, comparison, file, r.URL.Query().Get("side"))
		if err != nil {
			externalError(w, 404, "not_found", "This saved file is unavailable.")
			return
		}
		writeSavedSnapshotFile(w, r, prefix+file, content)
		return
	}
	if lineText := r.URL.Query().Get("line"); lineText != "" {
		line, err := strconv.Atoi(lineText)
		side := r.URL.Query().Get("side")
		anchor, anchorErr := savedReviewAnchor(ctx, store, comparison, file, side, line)
		if err != nil || anchorErr != nil {
			externalError(w, 400, "invalid_anchor", "Choose a line in the saved comparison again.")
			return
		}
		externalJSON(w, map[string]any{"context": fmt.Sprintf("In %s, %s line %d of the saved file comparison from %s through %s:\n%s", prefix+file, side, line, comparison.Record.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"), comparison.Record.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC"), anchor)})
		return
	}
	total := len(comparison.Changes)
	nextOffset := 0
	if file != "" {
		for _, change := range comparison.Changes {
			if change.Path == file {
				comparison.Changes = []filesnapshots.Change{change}
				break
			}
		}
	} else {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 || offset > total {
			offset = 0
		}
		end := offset + 100
		if end > total {
			end = total
		}
		comparison.Changes = comparison.Changes[offset:end]
		if end < total {
			nextOffset = end
		}
	}
	for i := range comparison.Changes {
		comparison.Changes[i].Path = prefix + comparison.Changes[i].Path
	}
	for i := range comparison.Record.Skipped {
		comparison.Record.Skipped[i] = prefix + comparison.Record.Skipped[i]
	}
	history.Last = last
	fileReplies := []string{}
	incomplete := false
	if file != "" {
		for _, ref := range history.Replies {
			if ref.End > 0 && ref.ID != "" {
				change, err := store.Compare(ctx, ref.ID, "", allow)
				if err != nil {
					incomplete = true
					continue
				}
				for _, changed := range change.Changes {
					if changed.Path == file {
						fileReplies = append(fileReplies, ref.ID)
						break
					}
				}
			}
		}
	}
	externalJSON(w, map[string]any{"history": history, "comparison": comparison, "file_replies": fileReplies, "reply_history_incomplete": incomplete, "total_changes": total, "next_offset": nextOffset})
}

func savedReviewAnchor(ctx context.Context, store filesnapshots.Store, comparison filesnapshots.Comparison, file, side string, line int) (string, error) {
	if line < 1 {
		return "", errors.New("invalid line")
	}
	content, err := store.ReadComparedFile(ctx, comparison, file, side)
	if err != nil || !utf8.Valid(content) || strings.ContainsRune(string(content), 0) {
		return "", errors.New("not a text line")
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if line > len(lines) || len(lines[line-1]) > 8192 {
		return "", errors.New("invalid line")
	}
	return lines[line-1], nil
}
