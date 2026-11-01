package filesnapshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// CompareSaved accepts only the reply IDs the server resolved from one
// authorized chat or project. Spans remain separate comparisons, never stitched across
// checkout/pull transitions. A key selects a saved file version, not a Git ref.
func (s Store) CompareSaved(ctx context.Context, ids []string, file, key string, allow func(string) bool, checkoutID ...string) (Comparison, error) {
	var records []Record
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r, err := s.read(id)
		if err != nil || (r.Status != "complete" && r.Status != "partial") {
			return Comparison{}, errors.New("saved reply is unavailable")
		}
		records = append(records, r)
	}
	if len(records) == 0 {
		return Comparison{Changes: []Change{}}, nil
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].StartedAt.Before(records[j].StartedAt) })
	return s.compareReview(ctx, records, file, key, allow, checkoutID...)
}

func (s Store) compareReview(ctx context.Context, records []Record, file, key string, allow func(string) bool, checkoutID ...string) (Comparison, error) {
	out := Comparison{Record: records[len(records)-1], Changes: []Change{}, versions: map[string]savedVersion{}}
	out.Record.StartedAt = records[0].StartedAt
	out.Record.Skipped = nil
	var incomplete int
	newRepos := map[string]bool{}
	for _, r := range records {
		for p, tree := range r.NewRepos {
			if tree != "" {
				newRepos[p] = true
			}
		}
		add := func(before, after string, label *CheckoutLabel, ordinal int, skipped []string, filter func(string) bool) error {
			if len(checkoutID) > 0 && !matchesCheckout(label, checkoutID[0]) {
				return nil
			}
			part := r
			part.Before, part.After, part.NewRepos, part.GoneRepos = before, after, nil, nil
			part.Omitted = append(append([]string{}, r.Omitted...), skipped...)
			if label != nil && label.Root != "" {
				part.diffPaths = []string{label.Root}
			}
			comparison, err := s.compareRaw(ctx, part, "", filter)
			if err != nil {
				return err
			}
			for _, change := range comparison.Changes {
				change.Key = digest(r.ID + ":" + fmt.Sprint(ordinal) + ":" + change.Path)
				change.Checkout = label
				change.At = r.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
				out.versions[change.Key] = savedVersion{comparison.Record.Before, comparison.Record.After, change.Path}
				out.Changes = append(out.Changes, change)
			}
			return nil
		}
		b, a, err := s.effective(ctx, r)
		if err != nil {
			return out, err
		}
		if len(records) == 1 {
			out.Record.Before, out.Record.After = b, a
		}
		if r.Review == nil {
			// These are the actual older saved copies; missing historical
			// branch metadata is never filled in using today's working tree.
			if err := add(b, a, nil, -1, nil, allow); err != nil {
				return out, err
			}
		} else {
			seenIncomplete := map[string]bool{}
			for _, root := range r.Review.IncompleteRoots {
				if !seenIncomplete[root] && s.authorizedCheckout(ctx, r, root, allow) {
					incomplete++
					seenIncomplete[root] = true
				}
			}
			if err := add(b, a, nil, -1, nil, func(p string) bool { _, repo := savedRoot(p, r.Review.Roots); return !repo && allow(p) }); err != nil {
				return out, err
			}
			for i, span := range r.Review.Spans {
				label := span.Checkout
				if err := add(span.Before, span.After, &label, i, span.Skipped, func(p string) bool {
					root, repo := savedRoot(p, r.Review.Roots)
					return repo && root == label.Root && allow(p)
				}); err != nil {
					return out, err
				}
			}
		}
		// Skipped counts are computed once per reply, independent of spans.
		partial, err := s.compareRaw(ctx, r, "", allow)
		if err != nil {
			return out, err
		}
		out.UncapturedChanges += partial.UncapturedChanges
		out.SkippedCount += partial.SkippedCount
		out.Record.Skipped = append(out.Record.Skipped, partial.Record.Skipped...)
		if r.Status == "partial" {
			out.Record.Status = "partial"
		}
	}
	for p := range newRepos {
		out.NewRepos = append(out.NewRepos, p)
	}
	sort.Strings(out.NewRepos)
	// Compact checkout/branch groups with versions in saved reply order.
	sort.SliceStable(out.Changes, func(i, j int) bool {
		a, b := out.Changes[i], out.Changes[j]
		group := func(c Change) string {
			if c.Checkout != nil {
				return "0:" + c.Checkout.Root + ":" + c.Checkout.Branch
			}
			return "1:"
		}
		if group(a) != group(b) {
			return group(a) < group(b)
		}
		return a.Path < b.Path
	})
	if incomplete > 0 {
		out.Note = fmt.Sprintf("Incomplete: Git history could not be established for %d checkout comparisons.", incomplete)
		out.Record.Status = "partial"
	}
	if file != "" || key != "" {
		var chosen *Change
		for i := len(out.Changes) - 1; i >= 0; i-- {
			c := &out.Changes[i]
			if c.Path == file && (key == "" || c.Key == key) {
				chosen = c
				break
			}
		}
		if chosen == nil {
			return out, errors.New("file not found in this comparison")
		}
		v := out.versions[chosen.Key]
		part := out.Record
		part.Before, part.After, part.NewRepos, part.GoneRepos = v.before, v.after, nil, nil
		part.Skipped, part.Omitted, part.BaselineSkipped = nil, nil, nil
		part.diffPaths = []string{file}
		c, err := s.compareRaw(ctx, part, file, func(p string) bool { return p == file && allow(p) })
		if err != nil {
			return out, err
		}
		out.Diff, out.Truncated = c.Diff, c.Truncated
		out.Changes = []Change{*chosen}
	} else {
		out.Changes = groupSavedFiles(out.Changes)
	}
	return out, nil
}

// A file has one compact row/count per checkout and branch. Its saved diffs
// remain separate children, including across pulls; no bytes are joined.
func groupSavedFiles(changes []Change) []Change {
	out := []Change{}
	index := map[string]int{}
	for _, c := range changes {
		key := c.Path
		if c.Checkout != nil {
			key = c.Checkout.ID + ":" + c.Checkout.Branch + ":" + c.Path
		}
		if i, ok := index[key]; ok {
			g := &out[i]
			if len(g.Edits) == 0 {
				g.Edits = []Change{*g}
			}
			g.Edits = append(g.Edits, c)
			if g.Status != c.Status {
				g.Status = "M"
			}
			if c.Added != nil {
				n := *c.Added
				if g.Added != nil {
					n += *g.Added
				}
				g.Added = &n
			}
			if c.Removed != nil {
				n := *c.Removed
				if g.Removed != nil {
					n += *g.Removed
				}
				g.Removed = &n
			}
			g.Binary = g.Binary || c.Binary
		} else {
			index[key] = len(out)
			out = append(out, c)
		}
	}
	return out
}

// WithPrefix formats local Crew paths once for lists, child diffs and groups.
func (c Change) WithPrefix(prefix string) Change {
	c.Path = prefix + c.Path
	if c.Checkout != nil && prefix != "" {
		label := *c.Checkout
		label.Root = strings.TrimSuffix(prefix, "/")
		if c.Checkout.Root != "" {
			label.Root += "/" + c.Checkout.Root
		}
		c.Checkout = &label
	}
	if c.Edits != nil {
		edits := make([]Change, len(c.Edits))
		for i, edit := range c.Edits {
			edits[i] = edit.WithPrefix(prefix)
		}
		c.Edits = edits
	}
	return c
}

func (s Store) reviewVersion(comparison Comparison, file, side string) (string, error) {
	for _, c := range comparison.Changes {
		if c.Path == file {
			if version, ok := comparison.versions[c.Key]; ok {
				if side == "before" {
					return version.before, nil
				}
				return version.after, nil
			}
		}
	}
	return "", errors.New("saved version unavailable")
}

func (r Record) foreignCheckout(file string) bool {
	if r.Review != nil {
		for _, p := range r.Review.ForeignRoots {
			if inCheckout(file, p) {
				return true
			}
		}
	}
	return false
}

func historicalBranch(g Group, label *CheckoutLabel) bool {
	return label != nil && (g.Branch != label.Branch || (g.GitHubRepo != "" && g.GitHubRepo != label.GitHubRepo))
}

func (s Store) authorizedCheckout(ctx context.Context, r Record, root string, allow func(string) bool) bool {
	for _, tree := range []string{r.Before, r.After} {
		args := []string{"--literal-pathspecs", "ls-tree", "-r", "--name-only", "-z", tree, "--"}
		if root != "" {
			args = append(args, root)
		}
		raw, err := s.command(ctx, args...).Output()
		if err != nil {
			continue
		}
		for _, name := range bytes.Split(raw, []byte{0}) {
			p := string(name)
			if p != "" && allow(p) {
				if owner, ok := savedRoot(p, r.Review.Roots); ok && owner == root {
					return true
				}
			}
		}
	}
	return false
}

// LooseCheckout identifies files outside Git without conflating them with a
// checkout whose repository happens to be the project root.
const LooseCheckout = "project-files"

func matchesCheckout(label *CheckoutLabel, id string) bool {
	if id == "" {
		return true
	}
	if label == nil {
		return id == LooseCheckout
	}
	return label.ID == id
}
