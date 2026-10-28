package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	agentevents "github.com/manishiitg/mcpagent/events"
)

type chatSnapshotReference struct {
	ID        string `json:"snapshot_id"`
	Workspace string `json:"workspace_path"`
	Start     int64  `json:"start_sequence"`
	End       int64  `json:"end_sequence"`
}
type chatReviewPoint struct {
	chatSnapshotReference
	First      *chatSnapshotReference `json:"first,omitempty"`
	ReviewedAt time.Time              `json:"reviewed_at"`
}
type chatReviewSummary struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

func reviewSummary(comparison filesnapshots.Comparison) chatReviewSummary {
	out := chatReviewSummary{Files: len(comparison.Changes)}
	for _, change := range comparison.Changes {
		if change.Added != nil {
			out.Added += *change.Added
		}
		if change.Removed != nil {
			out.Removed += *change.Removed
		}
	}
	return out
}

type chatFileReview struct {
	Reviews   []chatReviewPoint       `json:"reviews"`
	Baseline  string                  `json:"baseline_snapshot_id"`
	Latest    string                  `json:"latest_snapshot_id"`
	CanReview bool                    `json:"can_review"`
	Replies   []chatSnapshotReference `json:"replies"`
	First     string                  `json:"first_snapshot_id"`
	Last      string                  `json:"last_snapshot_id"`
	Available bool                    `json:"available"`
	Note      string                  `json:"note,omitempty"`
	// The stored reply_file_changes data by snapshot id, for the reply card.
	replyData map[string]json.RawMessage
}

func (api *StreamingAPI) chatFileReviewHistory(ctx context.Context, store filesnapshots.Store, sessionID, root string) (chatFileReview, error) {
	out := chatFileReview{Replies: []chatSnapshotReference{}, Reviews: []chatReviewPoint{}, Baseline: "start", replyData: map[string]json.RawMessage{}}
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
		out.replyData[ref.ID] = raw
	}
	seen := map[string]bool{}
	for _, ref := range out.Replies {
		seen[ref.ID] = true
	}
	for _, event := range history.Reviews {
		if event.Data == nil {
			continue
		}
		raw, _ := json.Marshal(event.Data.Data)
		var point chatReviewPoint
		if json.Unmarshal(raw, &point) != nil || point.ID == "" || point.Workspace != root || point.End <= point.Start {
			continue
		}
		out.Reviews = append(out.Reviews, point)
		for _, ref := range []*chatSnapshotReference{&point.chatSnapshotReference, point.First} {
			if ref != nil && ref.Workspace == root && !seen[ref.ID] {
				out.Replies = append(out.Replies, *ref)
				seen[ref.ID] = true
			}
		}
	}
	sort.Slice(out.Reviews, func(i, j int) bool { return out.Reviews[i].End < out.Reviews[j].End })
	sort.Slice(out.Replies, func(i, j int) bool { return out.Replies[i].End < out.Replies[j].End })
	// A reply whose capture failed has no saved before/after; it must not be
	// the start of the comparison (PLAT-746-163). A record removed by
	// retention stays listed, so its missing start is still refused.
	usable := out.Replies[:0]
	for _, ref := range out.Replies {
		if !store.Failed(ref.ID) {
			usable = append(usable, ref)
		}
	}
	out.Replies = usable
	if len(out.Replies) > 0 {
		first, last := out.Replies[0], out.Replies[len(out.Replies)-1]
		out.First, out.Last, out.Latest = first.ID, last.ID, last.ID
		out.Available = first.ID == history.FirstSnapshotID && history.FirstUserSequence > first.Start && history.FirstUserSequence <= first.End
	}
	if len(out.Reviews) > 0 {
		out.Baseline = out.Reviews[len(out.Reviews)-1].ID
	}
	if len(out.Replies) == 0 {
		out.Note = "No file changes saved for this chat yet. Changes appear here after the agent edits files."
	}
	return out, nil
}

// The file route already authorizes the Crew and current paths. Chat history
// has its own read rule; sharing files alone does not grant a private chat.
func (api *StreamingAPI) serveChatFileReview(w http.ResponseWriter, r *http.Request, roots crewroot.Roots, prefix, op, id, file string, store filesnapshots.Store, allow func(string) bool) {
	root := roots.Virtual
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
	history, err := api.chatFileReviewHistory(ctx, store, sessionID, root)
	if err != nil {
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	history.CanReview = api.crewSessionCanControl(r, sessionID)
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:run") {
		history.CanReview = false
	}
	if op == "snapshot_review" {
		api.markChatFilesReviewed(w, r, sessionID, store, history, id)
		return
	}
	if op != "snapshots" && op != "snapshot_compare" && op != "snapshot_file" && op != "snapshot_groups" && op != "reply_card" {
		externalError(w, 400, "invalid_action", "Choose a saved file comparison.")
		return
	}
	if op == "snapshots" {
		externalJSON(w, history)
		return
	}
	first, last, side := history.First, history.Last, "before"
	refs := map[string]chatSnapshotReference{}
	for _, ref := range history.Replies {
		refs[ref.ID] = ref
	}
	baseline := history.Baseline
	if pin := r.URL.Query().Get("baseline_snapshot_id"); pin != "" {
		baseline = pin
	}
	if reviewID := r.URL.Query().Get("review_id"); reviewID != "" {
		found := false
		for i, point := range history.Reviews {
			if point.ID == reviewID {
				last = point.ID
				baseline = "start"
				if i > 0 {
					baseline = history.Reviews[i-1].ID
				}
				found = true
				break
			}
		}
		if !found {
			externalError(w, 404, "not_found", "This review point is unavailable.")
			return
		}
	}
	if baseline != "start" {
		found := false
		for _, point := range history.Reviews {
			if point.ID == baseline {
				found = true
				break
			}
		}
		if !found {
			externalError(w, 404, "not_found", "This review point does not belong to this chat.")
			return
		}
		first, side = baseline, "after"
	}
	if id != "" {
		if _, found := refs[id]; !found {
			externalError(w, 404, "not_found", "This saved reply does not belong to this chat.")
			return
		}
		first, last, side = id, id, "before"
	} else if baseline == "start" && len(history.Replies) == 0 {
		if op == "snapshot_file" || r.URL.Query().Get("line") != "" {
			externalError(w, 404, "snapshot_unavailable", history.Note)
			return
		}
		externalJSON(w, map[string]any{"history": history, "changes": []any{}})
		return
	}
	// The card's file list comes from the reply event; without a GitHub
	// lookup its actions need no comparison.
	summaryGroups, hasSummary := history.summaryGroups(id)
	lookUpPullRequests := r.URL.Query().Get("pull_requests") == "true"
	if op == "reply_card" && hasSummary && !lookUpPullRequests {
		externalJSON(w, api.replyCard(r, root, summaryGroups))
		return
	}
	if bound := r.URL.Query().Get("through_snapshot_id"); bound != "" {
		if _, found := refs[bound]; !found || (id != "" && bound != id) {
			externalError(w, 404, "not_found", "This saved comparison is unavailable.")
			return
		}
		last = bound
	}
	if refs[first].End > refs[last].End {
		externalError(w, 400, "invalid_boundary", "Refresh this comparison before reviewing it.")
		return
	}
	history.Baseline = baseline
	if prefix != "" && file != "" {
		if !strings.HasPrefix(file, prefix) {
			externalError(w, 404, "not_found", "Choose a file from this comparison.")
			return
		}
		file = strings.TrimPrefix(file, prefix)
	}
	comparison, err := store.CompareBoundaries(ctx, first, last, side, file, allow)
	if err != nil {
		if file != "" {
			externalError(w, 404, "snapshot_unavailable", "This saved file comparison is unavailable or you cannot read it.")
			return
		}
		history.Available = false
		history.Note = "The earliest saved changes of this chat were removed to save space. Open a recent reply's changes from the chat instead."
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
	if op == "snapshot_groups" {
		comparison.Record.Skipped = nil
		externalJSON(w, map[string]any{"snapshot_id": id, "groups": replyFileGroups(ctx, roots, prefix, comparison, allow, true)})
		return
	}
	if op == "reply_card" {
		groups := replyFileGroups(ctx, roots, prefix, comparison, allow, lookUpPullRequests)
		if hasSummary {
			// The file list stays as captured; the lookup adds only PR status and next step.
			for i := range summaryGroups {
				summaryGroups[i].PR, summaryGroups[i].NextStep = nil, ""
				for _, found := range groups {
					if found.Kind == summaryGroups[i].Kind && found.Root == summaryGroups[i].Root {
						summaryGroups[i].PR, summaryGroups[i].NextStep = found.PR, found.NextStep
					}
				}
			}
			groups = summaryGroups
		}
		externalJSON(w, api.replyCard(r, root, groups))
		return
	}
	summary := reviewSummary(comparison)
	var sinceReview *chatReviewSummary
	if id != "" && id == history.Latest {
		start, startSide := history.First, "before"
		if len(history.Reviews) > 0 {
			start, startSide = history.Reviews[len(history.Reviews)-1].ID, "after"
		}
		if history.Available || startSide == "after" {
			if net, err := store.CompareBoundaries(ctx, start, last, startSide, "", allow); err == nil {
				value := reviewSummary(net)
				sinceReview = &value
			}
		}
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
	externalJSON(w, map[string]any{"history": history, "comparison": comparison, "summary": summary, "since_review": sinceReview, "total_changes": total, "next_offset": nextOffset})

}

// Review is chat activity: use the existing chat control rule, never Crew settings authority.
func (api *StreamingAPI) markChatFilesReviewed(w http.ResponseWriter, r *http.Request, sessionID string, store filesnapshots.Store, history chatFileReview, id string) {
	if r.Method != http.MethodPost {
		externalError(w, 405, "method_not_allowed", "Use Mark as reviewed in this chat.")
		return
	}
	if !history.CanReview {
		externalError(w, 403, "forbidden", "You can view this chat; mark changes in a chat you can write in.")
		return
	}
	var ref *chatSnapshotReference
	for i := range history.Replies {
		if history.Replies[i].ID == id {
			ref = &history.Replies[i]
			break
		}
	}
	if ref == nil {
		externalError(w, 404, "not_found", "Choose a saved reply in this chat.")
		return
	}
	if len(history.Reviews) > 0 {
		last := history.Reviews[len(history.Reviews)-1]
		if last.ID == id {
			externalJSON(w, map[string]any{"review": last})
			return
		}
		if ref.End < last.End {
			externalError(w, 409, "already_reviewed", "A later reply is already reviewed. Refresh the changes.")
			return
		}
	}
	point := chatReviewPoint{chatSnapshotReference: *ref, ReviewedAt: time.Now().UTC()}
	if history.Available {
		for i := range history.Replies {
			if history.Replies[i].ID == history.First {
				first := history.Replies[i]
				point.First = &first
				break
			}
		}
	}
	if err := store.KeepReviewBoundaries(r.Context(), id); err != nil {
		externalError(w, 404, "snapshot_unavailable", "This saved reply is no longer available. Refresh the changes.")
		return
	}
	if point.First != nil {
		if err := store.KeepReviewBoundaries(r.Context(), point.First.ID); err != nil {
			// A pruned initial boundary must not prevent starting a new review point.
			point.First = nil
		}
	}
	raw, _ := json.Marshal(point)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	data := storeevents.NewGenericEventData("file_reviewed", fields)
	event := agentevents.NewAgentEvent(data)
	event.SessionID = sessionID
	if err := api.eventStore.AddEventChecked(sessionID, storeevents.Event{ID: "file-reviewed:" + id, Type: "file_reviewed", Timestamp: point.ReviewedAt, SessionID: sessionID, Data: event}); err != nil {
		externalError(w, 500, "review_not_saved", "Could not save the review point. Try again.")
		return
	}
	log.Printf("[FILE_REVIEW] session=%s snapshot=%s end_sequence=%d status=saved", sessionID, id, ref.End)
	externalJSON(w, map[string]any{"review": point})
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
