package server

// Line merging for Team sync (owner, 5 Nov): two people editing different lines of the same text file both keep their
// edits. A change sent from an older base is merged three ways (base = the sender's starting version, ours = the
// team's current version, theirs = the incoming one). Where both changed the same lines, the incoming (later) side
// wins that part and a clash is recorded; the team's version stays in history either way.

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

const (
	// crewTeamMergeMaxBytes bounds each of the three versions a merge reads.
	crewTeamMergeMaxBytes = 2 << 20
	// crewTeamMergeMaxEdits bounds the edit distance a line diff explores (its memory grows with the square).
	crewTeamMergeMaxEdits = 2000
)

// crewTeamMergeText reports whether data can be merged by lines: valid UTF-8 without NUL, within the size limit.
func crewTeamMergeText(data []byte) bool {
	return len(data) <= crewTeamMergeMaxBytes && bytes.IndexByte(data, 0) < 0 && utf8.Valid(data)
}

// crewTeamSplitLines splits text into lines, each keeping its "\n".
func crewTeamSplitLines(s string) []string {
	lines := []string{}
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// crewTeamMerge3 merges ours and theirs, both edited from base, line by line. Each overlapping change takes theirs;
// conflicts counts them. ok is false when the files are too different to diff within the limit.
func crewTeamMerge3(base, ours, theirs string) (merged string, conflicts int, ok bool) {
	ids := map[string]int{}
	intern := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, seen := ids[l]
			if !seen {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	bl, ol, tl := crewTeamSplitLines(base), crewTeamSplitLines(ours), crewTeamSplitLines(theirs)
	bi, oi, ti := intern(bl), intern(ol), intern(tl)
	mo, okO := crewTeamLineMatches(bi, oi)
	mt, okT := crewTeamLineMatches(bi, ti)
	if !okO || !okT {
		return "", 0, false
	}
	same := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	var out strings.Builder
	emit := func(lines []string) {
		for _, l := range lines {
			out.WriteString(l)
		}
	}
	n := len(bl)
	i, a, b := 0, 0, 0
	for i < n || a < len(ol) || b < len(tl) {
		if i < n && mo[i] == a && mt[i] == b {
			out.WriteString(bl[i])
			i, a, b = i+1, a+1, b+1
			continue
		}
		// The next base line both sides still hold is where they are in step again.
		next, na, nb := n, len(ol), len(tl)
		for j := i; j < n; j++ {
			if mo[j] >= 0 && mt[j] >= 0 {
				next, na, nb = j, mo[j], mt[j]
				break
			}
		}
		baseHunk, oursHunk, theirsHunk := bi[i:next], oi[a:na], ti[b:nb]
		switch {
		case same(oursHunk, baseHunk):
			emit(tl[b:nb])
		case same(theirsHunk, baseHunk), same(oursHunk, theirsHunk):
			emit(ol[a:na])
		default:
			conflicts++
			emit(tl[b:nb])
		}
		i, a, b = next, na, nb
	}
	return out.String(), conflicts, true
}

// crewTeamLineMatches maps each line of a to the line of b it is matched with in a longest common subsequence (-1 for
// none), with a Myers diff after trimming the common start and end. ok is false past crewTeamMergeMaxEdits.
func crewTeamLineMatches(a, b []int) ([]int, bool) {
	match := make([]int, len(a))
	for i := range match {
		match[i] = -1
	}
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		match[pre] = pre
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		match[len(a)-1-suf] = len(b) - 1 - suf
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	n, m := len(ma), len(mb)
	if n == 0 || m == 0 {
		return match, true
	}
	limit := min(n+m, crewTeamMergeMaxEdits)
	off := n + m + 1
	v := make([]int, 2*(n+m)+3)
	trace := [][]int{}
	for d := 0; ; d++ {
		if d > limit {
			return nil, false
		}
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && ma[x] == mb[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				// Walk back through the saved rounds, recording the diagonal (matching) moves.
				for dd := d; dd >= 0; dd-- {
					snap := trace[dd]
					at := func(k int) int { return snap[k+dd+1] }
					k := x - y
					prevK := k - 1
					if k == -dd || k != dd && at(k-1) < at(k+1) {
						prevK = k + 1
					}
					prevX := at(prevK)
					prevY := prevX - prevK
					for x > prevX && y > prevY {
						x, y = x-1, y-1
						match[pre+x] = pre + y
					}
					if dd > 0 {
						x, y = prevX, prevY
					}
				}
				return match, true
			}
		}
	}
}

// crewTeamMergeVersion tries the line merge for a change sent from base over the team's current version. It returns
// the merged version (its chunks stored) and the overlapping-change count; ok is false when any of the three is not
// text, too big, unavailable, or too different, and the caller keeps the whole-file rule (later wins).
func crewTeamMergeVersion(store string, state crewTeamFileState, rel, base string, current crewTeamVersion, incoming crewTeamFileVersion) (crewTeamFileVersion, int, bool) {
	if base == "" || current.File == nil {
		return crewTeamFileVersion{}, 0, false
	}
	var baseVersion *crewTeamFileVersion
	for i := len(state.Events) - 1; i >= 0; i-- {
		if e := state.Events[i]; e.Path == rel && e.File != nil && e.File.Hash == base {
			baseVersion = &crewTeamFileVersion{Hash: e.File.Hash, Size: e.File.Size, Exec: e.File.Executable, Chunks: e.Chunks}
			break
		}
	}
	if baseVersion == nil {
		return crewTeamFileVersion{}, 0, false
	}
	read := func(v crewTeamFileVersion) ([]byte, bool) {
		if v.Size > crewTeamMergeMaxBytes || !v.valid() {
			return nil, false
		}
		var buf bytes.Buffer
		if crewTeamAssemble(store, v, &buf) != nil || !crewTeamMergeText(buf.Bytes()) {
			return nil, false
		}
		return buf.Bytes(), true
	}
	ours := crewTeamFileVersion{Hash: current.File.Hash, Size: current.File.Size, Exec: current.File.Executable, Chunks: current.Chunks}
	b, okB := read(*baseVersion)
	o, okO := read(ours)
	t, okT := read(incoming)
	if !okB || !okO || !okT {
		return crewTeamFileVersion{}, 0, false
	}
	merged, conflicts, ok := crewTeamMerge3(string(b), string(o), string(t))
	if !ok {
		return crewTeamFileVersion{}, 0, false
	}
	v, err := crewTeamChunkReader(strings.NewReader(merged), int64(len(merged)), incoming.Exec, func(id string, data []byte) error { return crewTeamPutChunk(store, id, data) })
	if err != nil {
		return crewTeamFileVersion{}, 0, false
	}
	return v, conflicts, true
}
