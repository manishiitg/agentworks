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

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type chatStartMessage struct {
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}
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
	Scope       string                  `json:"scope,omitempty"`
	Checkouts   []projectCheckout       `json:"checkouts,omitempty"`
	RecentRepos []string                `json:"recent_repos,omitempty"`
	Reviews     []chatReviewPoint       `json:"reviews"`
	Baseline    string                  `json:"baseline_snapshot_id"`
	Latest      string                  `json:"latest_snapshot_id"`
	CanReview   bool                    `json:"can_review"`
	Replies     []chatSnapshotReference `json:"replies"`
	First       string                  `json:"first_snapshot_id"`
	Last        string                  `json:"last_snapshot_id"`
	Available   bool                    `json:"available"`
	// The person's message the comparison starts from, when the chat's
	// earliest replies are no longer saved.
	StartMessage *chatStartMessage `json:"start_message,omitempty"`
	Note         string            `json:"note,omitempty"`
	// The stored reply_file_changes data by snapshot id, for the reply card.
	replyData map[string]json.RawMessage
}

// Saved labels show the person's message, not model-only reconnect/session notices.
// Read projection only: keep original journal bytes and source turn boundaries intact.
func savedChatUserText(text string) string {
	text = strings.TrimSpace(stripSessionMode(text))
	for strings.HasPrefix(text, continuityNoticeOpen) {
		marker := continuityNoticeClose + sessionModeSplit
		index := strings.LastIndex(text, marker)
		if index < 0 {
			return ""
		}
		text = strings.TrimSpace(stripSessionMode(text[index+len(marker):]))
	}
	return cleanChatHistoryQuery(text)
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
	sort.Slice(out.Replies, func(i, j int) bool { return out.Replies[i].End < out.Replies[j].End })
	// A reply whose capture failed, or that retention removed, has no saved
	// before/after; the comparison starts at the oldest reply still saved and
	// names the message it came from (owner, 2026-10-29; PLAT-746-163).
	usable := out.Replies[:0]
	for _, ref := range out.Replies {
		if store.Saved(ref.ID) {
			usable = append(usable, ref)
		}
	}
	out.Replies = usable
	if len(out.Replies) > 0 {
		first, last := out.Replies[0], out.Replies[len(out.Replies)-1]
		out.First, out.Last, out.Latest = first.ID, last.ID, last.ID
		out.Available = first.ID == history.FirstSnapshotID && history.FirstUserSequence > first.Start && history.FirstUserSequence <= first.End
		if !out.Available {
			if message, err := api.eventStore.ChatUserMessageBetween(ctx, sessionID, first.Start, first.End); err == nil {
				text := []rune(strings.Join(strings.Fields(savedChatUserText(message.Text)), " "))
				if len(text) > 80 {
					text = append(text[:79], '…')
				}
				out.StartMessage = &chatStartMessage{Text: string(text), At: message.At}
			}
		}
	}
	if len(out.Replies) == 0 {
		out.Note = "No file changes saved for this chat yet. Changes appear here after the agent edits files."
	}
	return out, nil
}

// recentRepositories uses the chat's own saved reply groups, newest first.
// File grants are rechecked before even a checkout name is returned. It does
// not infer a working folder from titles, messages, or another chat's activity.
func (h chatFileReview) recentRepositories(prefix string, allow func(string) bool) []string {
	out := []string{}
	seen := map[string]bool{}
	for i := len(h.Replies) - 1; i >= 0; i-- {
		groups, _ := h.summaryGroups(h.Replies[i].ID)
		for _, group := range groups {
			if group.Kind != "repo" {
				continue
			}
			root := strings.TrimPrefix(group.Root, prefix)
			if seen[root] {
				continue
			}
			for _, file := range group.Files {
				name := strings.TrimPrefix(file.Path, prefix)
				if root != "" && !strings.HasPrefix(name, root+"/") {
					continue
				}
				if allow(name) {
					out = append(out, root)
					seen[root] = true
					break
				}
			}
		}
	}
	return out
}

// The file route already authorizes the Crew and current paths. Chat history
// has its own read rule; sharing files alone does not grant a private chat.
func (api *StreamingAPI) serveChatFileReview(w http.ResponseWriter, r *http.Request, roots crewroot.Roots, prefix, op, id, file string, store filesnapshots.Store, allow func(string) bool) {
	root := roots.Virtual
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	project := r.URL.Query().Get("history_scope") == "project"
	if project && !crewProjectOwnedByCaller(GetUserIDFromContext(r.Context()), root) {
		externalError(w, 403, "forbidden", "Open project history in a Crew you own.")
		return
	}
	if !project && api.eventStore == nil {
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	if !project {
		if status, message := api.authorizeDurableChatRead(r, sessionID, root); status != 0 {
			externalError(w, status, "not_found", message)
			return
		}
	}
	outsideGit := r.URL.Query().Get("group_kind") == "crew_files"
	if kind := r.URL.Query().Get("group_kind"); kind != "" && (!outsideGit || (op != "snapshot_compare" && op != "snapshot_file")) {
		externalError(w, 400, "invalid_group", "Choose a saved file group.")
		return
	}
	compareAllow := allow
	if outsideGit {
		compareAllow = filesnapshots.OutsideGitPaths(roots.Files, allow)
	}
	ctx, cancel := context.WithTimeout(r.Context(), crewSnapshotTimeout)
	defer cancel()
	var history chatFileReview
	var err error
	if project {
		history, err = projectFileReviewHistory(ctx, store, root, allow, op == "snapshots" || id == "")
	} else {
		history, err = api.chatFileReviewHistory(ctx, store, sessionID, root)
	}
	if err != nil {
		if project {
			externalError(w, http.StatusServiceUnavailable, "history_unavailable", "Project history could not finish loading. Refresh to retry.")
			return
		}
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	if file == "" && (op == "snapshot_compare" || op == "snapshots") {
		history.RecentRepos = history.recentRepositories(prefix, allow)
		if len(history.RecentRepos) > 0 {
			log.Printf("[FILE_REVIEW] session=%s recent_repos=%d preferred_repo=%q", sessionID, len(history.RecentRepos), history.RecentRepos[0])
		}
	}
	if project {
		history.CanReview = true
	} else {
		history.CanReview = api.crewSessionCanControl(r, sessionID)
	}
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:run") {
		history.CanReview = false
	}
	if op != "snapshots" && op != "snapshot_compare" && op != "snapshot_file" && op != "snapshot_groups" && op != "reply_card" && op != "chat_status" && op != "snapshot_turn" {
		externalError(w, 400, "invalid_action", "Choose a saved file comparison.")
		return
	}
	if op == "snapshots" {
		if project {
			api.decorateProjectHistory(r, root, &history)
		}
		externalJSON(w, history)
		return
	}
	if project && id == "" {
		checkoutID := r.URL.Query().Get("checkout_id")
		for _, checkout := range history.Checkouts {
			if checkoutID == "" || checkout.ID == checkoutID {
				if len(checkout.Captures) > 0 {
					id = checkout.Captures[0].SnapshotID
					break
				}
			}
		}
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
	if baseline != "start" || r.URL.Query().Get("review_id") != "" {
		externalError(w, 400, "invalid_boundary", "Choose a saved reply comparison.")
		return
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
	// Saved spans decide files and context; current grants still apply even
	// when this request does not need a GitHub lookup.
	lookUpPullRequests := r.URL.Query().Get("pull_requests") == "true"
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
	compare := func(first, last, side, file, key string) (filesnapshots.Comparison, error) {
		ids := []string{}
		for _, ref := range history.Replies {
			if ref.End >= refs[first].End && ref.End <= refs[last].End && !(side == "after" && ref.ID == first) {
				ids = append(ids, ref.ID)
			}
		}
		checkoutID := r.URL.Query().Get("checkout_id")
		person := GetUserIDFromContext(r.Context())
		return compareSavedOnce(ctx, sessionID, []string{person, root, prefix, strconv.FormatBool(outsideGit), checkoutID, file, key, strings.Join(ids, ",")}, func() (filesnapshots.Comparison, error) {
			return store.CompareSaved(ctx, ids, file, key, compareAllow, checkoutID)
		})
	}
	comparison, err := compare(first, last, side, file, r.URL.Query().Get("change_key"))
	if err != nil {
		log.Printf("[FILE_REVIEW] phase=compare session=%s first=%s last=%s error=%v", sessionID, first, last, err)
		if file != "" {
			externalError(w, 404, "snapshot_unavailable", "This saved file comparison is unavailable or you cannot read it.")
			return
		}
		history.Available = false
		history.Note = "Could not compare this chat's saved changes. Refresh and try again."
		externalJSON(w, map[string]any{"history": history, "changes": []any{}})
		return
	}
	comparison.Record.ExecutionID = ""
	if op == "snapshot_turn" {
		if !project || id == "" || len(comparison.Changes) == 0 {
			externalError(w, 404, "source_unavailable", "Choose a saved project change you can read.")
			return
		}
		api.serveProjectSnapshotTurn(w, r, root, id)
		return
	}
	if outsideGit {
		kept := comparison.Changes[:0]
		for _, change := range comparison.Changes {
			if change.Checkout == nil {
				kept = append(kept, change)
			}
		}
		comparison.Changes = kept
		if file != "" && len(comparison.Changes) == 0 {
			externalError(w, 404, "not_found", "Choose a file outside Git from this comparison.")
			return
		}
		log.Printf("[FILE_REVIEW] outside_git session=%s files=%d", sessionID, len(comparison.Changes))
	}
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
		externalJSON(w, map[string]any{"snapshot_id": id, "groups": replyFileGroups(ctx, roots, prefix, comparison, allow, true, nil)})
		return
	}
	if op == "chat_status" {
		// Where the chat's work stands (PLAT-746): per checkout what is not
		// committed, the commits made in this chat and its pull request; other
		// files as saved. Git decides, so committed work leaves "not committed".
		replies := append([]chatSnapshotReference(nil), history.Replies...)
		sort.Slice(replies, func(i, j int) bool { return replies[i].Start < replies[j].Start })
		ids := make([]string, 0, len(replies))
		for _, ref := range replies {
			ids = append(ids, ref.ID)
		}
		comparison.Record.Skipped = nil
		groups := replyFileGroups(ctx, roots, prefix, comparison, allow, true, store.ChatStarts(ids))
		canEdit := api.replyCardCanEdit(r, root)
		log.Printf("[FILE_REVIEW] chat_status session=%s groups=%d replies=%d can_edit=%t can_review=%t", sessionID, len(groups), len(ids), canEdit, history.CanReview)
		for _, group := range groups {
			if group.Kind != "repo" || group.GitHubRepo == "" {
				continue
			}
			state := "none"
			if group.PR != nil {
				state = group.PR.State
			}
			log.Printf("[FILE_REVIEW] phase=github_status session=%s repo=%q branch=%q pr_state=%s can_edit=%t can_review=%t", sessionID, group.Root, group.Branch, state, canEdit, history.CanReview)
		}
		externalJSON(w, map[string]any{"history": history, "groups": groups, "can_edit": canEdit})
		return
	}
	if op == "reply_card" {
		groups := replyFileGroups(ctx, roots, prefix, comparison, allow, lookUpPullRequests, nil)
		card := api.replyCard(r, root, groups)
		card["note"] = comparison.Note
		externalJSON(w, card)
		return
	}
	summary := reviewSummary(comparison)
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
		comparison.Changes[i] = comparison.Changes[i].WithPrefix(prefix)
	}
	for i := range comparison.Record.Skipped {
		comparison.Record.Skipped[i] = prefix + comparison.Record.Skipped[i]
	}
	history.Last = last
	if project {
		log.Printf("[FILE_REVIEW] scope=project checkout=%q snapshot=%s files=%d", r.URL.Query().Get("checkout_id"), last, total)
	}
	externalJSON(w, map[string]any{"history": history, "comparison": comparison, "summary": summary, "total_changes": total, "next_offset": nextOffset})

}

// Review is chat activity: use the existing chat control rule, never Crew settings authority.
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
