package server

import (
	"context"
	"sort"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

type projectCapture struct {
	SnapshotID string    `json:"snapshot_id"`
	At         time.Time `json:"at"`
	Branch     string    `json:"branch,omitempty"`
	chatReviewSummary
	Note string `json:"note,omitempty"`
}

type projectCheckout struct {
	WorkingTree string           `json:"working_tree,omitempty"`
	ID          string           `json:"id"`
	Root        string           `json:"root"`
	Name        string           `json:"name"`
	Captures    []projectCapture `json:"captures"`
}

// Project history contains file snapshots, never another chat's messages,
// session IDs or review points. The caller has already authorized this Crew's
// files; path grants are applied before names, counts and saved IDs are exposed.
func projectFileReviewHistory(ctx context.Context, store filesnapshots.Store, root string, allow func(string) bool, includeCheckouts bool) (chatFileReview, error) {
	out := chatFileReview{Scope: "project", Baseline: "start", Replies: []chatSnapshotReference{}, Reviews: []chatReviewPoint{}, Checkouts: []projectCheckout{}}
	records, err := store.List()
	if err != nil {
		return out, err
	}
	byCheckout := map[string]int{}
	// Store.List is newest first. Each entry is an exact saved comparison, not
	// a folder-wide diff stitched across overlapping turns or Git switches.
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if record.Status != "complete" && record.Status != "partial" {
			continue
		}
		rank := int64(len(records) - i)
		ref := chatSnapshotReference{ID: record.ID, Workspace: root, Start: rank * 2, End: rank*2 + 1}
		if !includeCheckouts {
			out.Replies = append(out.Replies, ref)
			continue
		}
		summary, err := store.SavedSummary(ctx, record.ID, allow)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Note = "Some saved comparisons are unavailable."
			continue
		}
		comparison := filesnapshots.Comparison{Changes: summary.Changes}
		if summary.Incomplete {
			comparison.Note = "Some files could not be captured or compared."
		}
		groups := map[string][]filesnapshots.Change{}
		labels := map[string]*filesnapshots.CheckoutLabel{}
		for _, change := range comparison.Changes {
			id := filesnapshots.LooseCheckout
			if change.Checkout != nil {
				id = change.Checkout.ID
			}
			groups[id] = append(groups[id], change)
			labels[id] = change.Checkout
		}
		if comparison.Note != "" {
			out.Note = "Some project changes could not be captured or compared."
		}
		if len(groups) == 0 {
			continue
		} // Commit/push alone is not a new file edit.
		out.Replies = append(out.Replies, ref)
		for id, changes := range groups {
			index, ok := byCheckout[id]
			if !ok {
				checkout := projectCheckout{ID: id, Name: "Project files", Captures: []projectCapture{}}
				if label := labels[id]; label != nil {
					checkout.Root, checkout.Name = label.Root, label.Name
				}
				for _, group := range filesnapshots.GroupChanges(ctx, store.Root, changes, nil, allow) {
					if group.CheckoutID == id {
						checkout.WorkingTree = group.WorkingTree
						break
					}
				}
				index = len(out.Checkouts)
				byCheckout[id] = index
				out.Checkouts = append(out.Checkouts, checkout)
			}
			summary := reviewSummary(filesnapshots.Comparison{Changes: changes})
			capture := projectCapture{SnapshotID: record.ID, At: record.StartedAt, chatReviewSummary: summary, Note: comparison.Note}
			if label := labels[id]; label != nil {
				capture.Branch = label.Branch
				for _, change := range changes {
					if change.Checkout != nil && change.Checkout.Branch != label.Branch {
						capture.Branch = "Multiple branches"
						break
					}
				}
			}
			out.Checkouts[index].Captures = append(out.Checkouts[index].Captures, capture)
		}
	}
	// Reuse the comparison endpoint's ordered boundaries, without borrowing a
	// runtime session ID or exposing a private source conversation.
	for i, j := 0, len(out.Replies)-1; i < j; i, j = i+1, j-1 {
		out.Replies[i], out.Replies[j] = out.Replies[j], out.Replies[i]
	}
	if len(out.Replies) > 0 {
		out.First = out.Replies[0].ID
		out.Last = out.Replies[len(out.Replies)-1].ID
		out.Latest = out.Last
	}
	sort.Slice(out.Checkouts, func(i, j int) bool { return out.Checkouts[i].Root < out.Checkouts[j].Root })
	out.Available = len(out.Replies) > 0
	if !out.Available && out.Note == "" {
		out.Note = "No saved project changes yet."
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}
