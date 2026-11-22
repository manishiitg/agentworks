// Package instructiondiff sends a coding agent only the changed lines of its
// instructions, so a change does not put a whole new copy in the conversation.
package instructiondiff

import "strings"

// Note explains a diff to the agent.
const Note = `Your AgentWorks instructions changed. Below are only the changed lines, as a diff: "-" lines are removed, "+" lines are added, other lines are unchanged context. Apply them to the instructions you already have; everything else stays as it was.`

// Lines is the changed lines from old to current with two lines
// of context, as a unified diff without line numbers. ok is false when the diff
// is not much smaller than current, so the whole set is sent instead.
func Lines(old, current string) (string, bool) {
	a, b := strings.Split(old, "\n"), strings.Split(current, "\n")
	// Trim the common head and tail so the table covers only the changed middle.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(midA)*len(midB) > 4_000_000 {
		return "", false
	}
	// ops: ' ' kept, '-' removed, '+' added, over the whole of a and b.
	type op struct {
		kind byte
		line string
	}
	ops := make([]op, 0, len(a)+len(b))
	for _, line := range a[:head] {
		ops = append(ops, op{' ', line})
	}
	n, m := len(midA), len(midB)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if midA[i] == midB[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && midA[i] == midB[j]:
			ops = append(ops, op{' ', midA[i]})
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', midB[j]})
			j++
		default:
			ops = append(ops, op{'-', midA[i]})
			i++
		}
	}
	for _, line := range a[len(a)-tail:] {
		ops = append(ops, op{' ', line})
	}
	const context = 2
	show := make([]bool, len(ops))
	changed := false
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		changed = true
		for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
			show[k] = true
		}
	}
	if !changed {
		return "", false
	}
	var out strings.Builder
	for i, o := range ops {
		if !show[i] {
			continue
		}
		if i == 0 || !show[i-1] {
			out.WriteString("@@\n")
		}
		out.WriteByte(o.kind)
		out.WriteString(o.line)
		out.WriteByte('\n')
	}
	diff := strings.TrimRight(out.String(), "\n")
	return diff, len(diff) <= len(current)/2
}
