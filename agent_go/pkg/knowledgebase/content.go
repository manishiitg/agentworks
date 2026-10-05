package knowledgebase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func (s *Service) execute(ctx context.Context, p Principal, tool string, a map[string]any) (any, []fileChange, error) {
	switch tool {
	case "manage_knowledgebase_access":
		return s.manage(ctx, p, a)
	case "get_knowledgebase_access":
		v, e := s.access(p, a)
		return v, nil, e
	case "create_knowledgebase_folder":
		return s.createFolder(p, a)
	case "create_knowledgebase":
		return s.createEntry(p, a)
	case "update_knowledgebase":
		return s.updateEntry(p, a)
	case "delete_knowledgebase":
		return s.deleteEntry(p, a)
	case "read_knowledgebase":
		v, e := s.readEntry(p, a)
		return v, nil, e
	case "list_knowledgebase", "list_knowledgebase_folders":
		v, e := s.list(p, tool, a)
		return v, nil, e
	case "search_knowledgebase":
		v, e := s.search(ctx, p, a)
		return v, nil, e
	case "get_knowledgebase_backup_status":
		v, e := s.backupStatus(p, a)
		return v, nil, e
	}
	return nil, nil, badArg("Unsupported tool.")
}
func (s *Service) collision(r folderRegistry, name string) bool {
	if files, e := os.ReadDir(filepath.Join(s.live, filepath.FromSlash(r.Path))); e == nil {
		for _, f := range files {
			if strings.EqualFold(f.Name(), name) {
				return true
			}
		}
	}
	for _, e := range r.Entries {
		if strings.EqualFold(e.Filename, name) {
			return true
		}
	}
	rs, _ := s.registries()
	for _, f := range rs {
		if strings.EqualFold(f.Path, childPath(r.Path, name)) {
			return true
		}
	}
	return false
}
func (s *Service) createFolder(p Principal, a map[string]any) (any, []fileChange, error) {
	r, err := s.resolveFolder(p, a, roleEditor)
	if err != nil {
		return nil, nil, err
	}
	name := stringArg(a, "name")
	if !validName(name, false) {
		return nil, nil, badArg("Invalid folder name.")
	}
	cp := childPath(r.Path, name)
	if err = validatePath(cp, false); err != nil {
		return nil, nil, err
	}
	if s.collision(r, name) {
		return nil, nil, kbErr("NAME_CONFLICT", "A sibling with this name already exists.")
	}
	nr := folderRegistry{ID: "folder_" + uuid.NewString(), Path: cp, Entries: []Entry{}, Deletions: []Deletion{}}
	result := map[string]any{"folder_id": nr.ID, "folder_path": nr.Path, "name": name}

	return result, []fileChange{jsonChange(s.registryPath(cp), nr)}, nil
}
func validateMetadata(e *Entry) error {
	if e.Type != "skill" && e.Type != "note" && e.Type != "fact" && e.Type != "source" {
		return badArg("type must be skill, note, fact, or source.")
	}
	if strings.TrimSpace(e.Title) == "" {
		return badArg("title is required.")
	}
	if utf8.RuneCountInString(e.Title) > 200 || utf8.RuneCountInString(e.Description) > 4000 || len(e.Tags) > 50 {
		return kbErr("LIMIT_EXCEEDED", "Metadata exceeds its limit.")
	}
	seen := make(map[string]bool, len(e.Tags))
	for _, t := range e.Tags {
		if strings.TrimSpace(t) == "" {
			return badArg("Tags must be non-empty.")
		}
		if seen[t] {
			return badArg("Tags must be unique.")
		}
		seen[t] = true
		if utf8.RuneCountInString(t) > 64 {
			return kbErr("LIMIT_EXCEEDED", "A tag exceeds 64 characters.")
		}
	}
	return nil
}

// All content and patch contexts use the same UTF-8, LF representation. Control
// characters other than ordinary text whitespace indicate binary input.
func normalizeText(text string, limit int, label string) (string, error) {
	if len(text) > limit {
		return "", kbErr("LIMIT_EXCEEDED", label+" exceeds its byte limit.")
	}
	if !utf8.ValidString(text) {
		return "", badArg("%s must be valid UTF-8 text.", label)
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return "", badArg("%s must not contain binary control characters or NUL bytes.", label)
		}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n"), nil
}
func tagsArg(v any) ([]string, error) {
	switch ts := v.(type) {
	case []string:
		return ts, nil
	case []any:
		out := make([]string, len(ts))
		for i, t := range ts {
			str, ok := t.(string)
			if !ok {
				return nil, badArg("tags must contain strings.")
			}
			out[i] = str
		}
		return out, nil
	default:
		return nil, badArg("tags must be an array of strings.")
	}
}
func (s *Service) createEntry(p Principal, a map[string]any) (any, []fileChange, error) {
	r, err := s.resolveFolder(p, a, roleEditor)
	if err != nil {
		return nil, nil, err
	}
	name := stringArg(a, "filename")
	if !validName(name, true) {
		return nil, nil, badArg("Invalid Markdown filename.")
	}
	ep := childPath(r.Path, name)
	if err = validatePath(ep, true); err != nil {
		return nil, nil, err
	}
	if s.collision(r, name) {
		return nil, nil, kbErr("NAME_CONFLICT", "A sibling with this name already exists.")
	}
	for _, d := range r.Deletions {
		if strings.EqualFold(d.Path, ep) && !s.deletionReusable(d) {
			return nil, nil, kbErr("PATH_PENDING_DELETION_BACKUP", "The deleted path is reserved until its backup is confirmed.")
		}
	}
	content, ok := a["content"].(string)
	if !ok {
		return nil, nil, badArg("content must be a string.")
	}
	content, err = normalizeText(content, 10*1024*1024, "Content")
	if err != nil {
		return nil, nil, err
	}
	tags := []string{}
	if v, has := a["tags"]; has {
		tags, err = tagsArg(v)
		if err != nil {
			return nil, nil, err
		}
	}
	description := ""
	if v, has := a["description"]; has {
		var ok bool
		description, ok = v.(string)
		if !ok {
			return nil, nil, badArg("description must be a string.")
		}
	}
	now := stamp()
	e := Entry{ID: "entry_" + uuid.NewString(), FolderID: r.ID, FolderPath: r.Path, Path: ep, Filename: name, Type: stringArg(a, "type"), Title: stringArg(a, "title"), Description: description, Tags: tags, Sequence: 1, ContentSequence: 1, Fingerprint: digest([]byte(content)), CreatedAt: now, UpdatedAt: now, CreatedBy: p.IdentityID, UpdatedBy: p.IdentityID}
	if err = validateMetadata(&e); err != nil {
		return nil, nil, err
	}
	e.Version = entryVersion(e)
	r.Entries = append(r.Entries, e)
	result := entryResult(e, true)
	return result, []fileChange{{Path: filepath.Join(s.live, filepath.FromSlash(e.Path)), Data: []byte(content)}, jsonChange(s.registryPath(r.Path), r)}, nil
}
func publicEntry(e Entry) map[string]any {
	m := asMap(e)
	delete(m, "sequence")
	delete(m, "content_sequence")
	delete(m, "fingerprint")
	return m
}
func entryResult(e Entry, changed bool) map[string]any {
	m := publicEntry(e)
	m["changed"] = changed
	m["entry"] = publicEntry(e)
	return m
}
func (s *Service) entryContent(e Entry) ([]byte, error) {
	p := filepath.Join(s.live, filepath.FromSlash(e.Path))
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, kbErr("STORAGE_UNAVAILABLE", "Content is not a regular file.")
	}
	b, err := os.ReadFile(p)
	if err == nil && digest(b) != e.Fingerprint {
		return nil, kbErr("STORAGE_UNAVAILABLE", "Content integrity check failed.")
	}
	return b, err
}
func (s *Service) updateEntry(p Principal, a map[string]any) (any, []fileChange, error) {
	e, r, err := s.resolveEntry(p, a, roleEditor)
	if err != nil {
		return nil, nil, err
	}
	if stringArg(a, "expected_version") != e.Version {
		return nil, nil, versionConflict(e)
	}
	_, hasDiff := a["diff"]
	_, hasContent := a["content"]
	if hasDiff && hasContent {
		return nil, nil, badArg("Choose diff or content, never both.")
	}
	old, err := s.entryContent(e)
	if err != nil {
		return nil, nil, err
	}
	content := string(old)
	if hasContent {
		var ok bool
		content, ok = a["content"].(string)
		if !ok {
			return nil, nil, badArg("content must be a string.")
		}
	}
	if hasDiff {
		d, ok := a["diff"].(string)
		if !ok {
			return nil, nil, badArg("diff must be a string.")
		}
		d, err = normalizeText(d, 2*1024*1024, "Diff")
		if err != nil {
			return nil, nil, err
		}
		if err = validateDiffTarget(d, e); err != nil {
			return nil, nil, err
		}
		content, err = handlers.ApplyDiffPatchDirect(content, d)
		if err != nil {
			return nil, nil, kbErr("PATCH_FAILED", "The patch could not be applied completely.")
		}
	}
	content, err = normalizeText(content, 10*1024*1024, "Content")
	if err != nil {
		return nil, nil, err
	}
	before := e
	meta, hasMeta := a["metadata"]
	if hasMeta {
		m, ok := meta.(map[string]any)
		if !ok {
			return nil, nil, badArg("metadata must be an object.")
		}
		if len(m) == 0 && !hasContent && !hasDiff {
			return nil, nil, badArg("Supply a content update or non-empty metadata.")
		}
		for k, v := range m {
			switch k {
			case "title", "description", "type":
				str, ok := v.(string)
				if !ok {
					return nil, nil, badArg("Metadata fields cannot be null and must have their declared types.")
				}
				switch k {
				case "title":
					e.Title = str
				case "description":
					e.Description = str
				case "type":
					e.Type = str
				}
			case "tags":
				e.Tags, err = tagsArg(v)
				if err != nil {
					return nil, nil, err
				}
			default:
				return nil, nil, badArg("Unknown metadata field.")
			}
		}
	}
	if !hasDiff && !hasContent && !hasMeta {
		return nil, nil, badArg("Supply a content update or non-empty metadata.")
	}
	if err = validateMetadata(&e); err != nil {
		return nil, nil, err
	}
	newHash := digest([]byte(content))
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(e)
	changed := newHash != before.Fingerprint || string(beforeJSON) != string(afterJSON)
	if !changed {
		return entryResult(e, false), nil, nil
	}
	e.Sequence++
	if newHash != e.Fingerprint {
		e.ContentSequence = e.Sequence
	}
	e.Fingerprint = newHash
	e.Version = entryVersion(e)
	e.UpdatedAt = stamp()
	e.UpdatedBy = p.IdentityID
	for i := range r.Entries {
		if r.Entries[i].ID == e.ID {
			r.Entries[i] = e
		}
	}
	changes := []fileChange{jsonChange(s.registryPath(r.Path), r)}
	if newHash != before.Fingerprint {
		changes = append([]fileChange{{Path: filepath.Join(s.live, filepath.FromSlash(e.Path)), Data: []byte(content)}}, changes...)
	}
	return entryResult(e, true), changes, nil
}
func versionConflict(e Entry) *Error {
	return &Error{Code: "VERSION_CONFLICT", Message: "The entry changed; read its current version and retry.", Details: map[string]any{"current_version": e.Version}}
}

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?:.*)$`)

func validateDiffTarget(diff string, e Entry) error {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") {
		return kbErr("PATCH_FAILED", "A single-file unified diff with headers and hunks is required.")
	}
	for _, line := range lines[:2] {
		name := strings.TrimSpace(line[4:])
		if i := strings.IndexByte(name, '\t'); i >= 0 {
			name = name[:i]
		}
		if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
			name = name[2:]
		}
		if name != e.Path && name != e.Filename {
			return kbErr("PATCH_FAILED", "The patch must target only the resolved entry.")
		}
	}
	at := 2
	hunks := 0
	for at < len(lines) {
		m := hunkHeader.FindStringSubmatch(lines[at])
		if m == nil {
			return kbErr("PATCH_FAILED", "The unified diff has an invalid hunk.")
		}
		oldCount, newCount := 1, 1
		if m[2] != "" {
			oldCount, _ = strconv.Atoi(m[2])
		}
		if m[4] != "" {
			newCount, _ = strconv.Atoi(m[4])
		}
		at++
		oldSeen, newSeen := 0, 0
		for at < len(lines) && !strings.HasPrefix(lines[at], "@@ ") {
			line := lines[at]
			if line == `\ No newline at end of file` {
				at++
				continue
			}
			if line == "" {
				return kbErr("PATCH_FAILED", "Every hunk line needs a diff prefix.")
			}
			switch line[0] {
			case ' ':
				oldSeen++
				newSeen++
			case '-':
				oldSeen++
			case '+':
				newSeen++
			default:
				return kbErr("PATCH_FAILED", "The unified diff has an invalid hunk line.")
			}
			at++
		}
		if oldSeen != oldCount || newSeen != newCount {
			return kbErr("PATCH_FAILED", "The hunk counts do not match its content.")
		}
		hunks++
	}
	if hunks == 0 {
		return kbErr("PATCH_FAILED", "The diff has no hunks.")
	}
	return nil
}
func (s *Service) deleteEntry(p Principal, a map[string]any) (any, []fileChange, error) {
	e, r, err := s.resolveEntry(p, a, roleEditor)
	if err != nil {
		return nil, nil, err
	}
	if stringArg(a, "expected_version") != e.Version {
		return nil, nil, versionConflict(e)
	}
	d := Deletion{ID: "deletion_" + uuid.NewString(), EntryID: e.ID, FolderID: r.ID, FolderPath: r.Path, Path: e.Path, Sequence: e.Sequence + 1, Actor: p.IdentityID, CreatedAt: stamp()}
	entries := make([]Entry, 0, len(r.Entries)-1)
	for _, v := range r.Entries {
		if v.ID != e.ID {
			entries = append(entries, v)
		}
	}
	r.Entries = entries
	r.Deletions = append(r.Deletions, d)
	result := asMap(d)
	result["deleted"] = true
	return result, []fileChange{{Path: filepath.Join(s.live, filepath.FromSlash(e.Path)), Delete: true}, jsonChange(s.registryPath(r.Path), r)}, nil
}

func numeric(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	case json.Number:
		i, e := n.Int64()
		return int(i), e == nil
	}
	return 0, false
}
func (s *Service) readEntry(p Principal, a map[string]any) (any, error) {
	e, _, err := s.resolveEntry(p, a, roleReader)
	if err != nil {
		return nil, err
	}
	b, err := s.entryContent(e)
	if err != nil {
		return nil, err
	}
	content := string(b)
	lines := contentLines(content)
	start, end := 1, len(lines)
	_, hs := a["start_line"]
	_, he := a["end_line"]
	section, hsect := a["section"]
	if hsect && (hs || he) {
		return nil, badArg("Section and line selectors cannot be mixed.")
	}
	if hs || he {
		var ok bool
		start, ok = numeric(a["start_line"])
		if !hs || !he || !ok {
			return nil, badArg("Both integer line bounds are required.")
		}
		end, ok = numeric(a["end_line"])
		if !ok || start < 1 || end < start {
			return nil, badArg("Invalid line range.")
		}
		if start > len(lines) {
			return nil, kbErr("RANGE_OUT_OF_BOUNDS", "The start line is beyond EOF.")
		}
		if end > len(lines) {
			end = len(lines)
		}
		content = strings.Join(lines[start-1:end], "\n")
		if end < len(lines) || strings.HasSuffix(string(b), "\n") {
			content += "\n"
		}
	}
	if hsect {
		m, ok := section.(map[string]any)
		if !ok {
			return nil, badArg("section must be an object.")
		}
		start, end, err = headingBounds(b, m)
		if err != nil {
			return nil, err
		}
		content = strings.Join(lines[start-1:end], "\n")
		if end < len(lines) || strings.HasSuffix(string(b), "\n") {
			content += "\n"
		}
	}
	result := publicEntry(e)
	result["entry"] = publicEntry(e)
	result["content"] = content
	result["total_lines"] = len(lines)
	result["start_line"] = start
	if len(lines) == 0 {
		result["start_line"] = 0
	}
	result["end_line"] = end
	return result, nil
}
func contentLines(content string) []string {
	if content == "" {
		return []string{}
	}
	content = strings.TrimSuffix(content, "\n")
	return strings.Split(content, "\n")
}
func headingBounds(b []byte, selector map[string]any) (int, int, error) {
	heading, ok := selector["heading"].(string)
	if !ok {
		return 0, 0, badArg("section.heading must be a string.")
	}
	for k := range selector {
		if k != "heading" && k != "occurrence" {
			return 0, 0, badArg("Unknown section field.")
		}
	}
	type found struct {
		name         string
		level, start int
	}
	var hs []found
	doc := goldmark.DefaultParser().Parse(text.NewReader(b))
	ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if enter && n.Kind() == ast.KindHeading {
			h := n.(*ast.Heading)
			seg := h.Lines().At(0)
			line := strings.Count(string(b[:seg.Start]), "\n") + 1
			hs = append(hs, found{name: string(h.Text(b)), level: h.Level, start: line})
		}
		return ast.WalkContinue, nil
	})
	matches := []int{}
	for i, h := range hs {
		if h.name == heading {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return 0, 0, kbErr("SECTION_NOT_FOUND", "Heading section not found.")
	}
	occ := 1
	if v, present := selector["occurrence"]; present {
		var ok bool
		occ, ok = numeric(v)
		if !ok || occ < 1 {
			return 0, 0, badArg("occurrence must be a positive integer.")
		}
	} else if len(matches) > 1 {
		return 0, 0, kbErr("AMBIGUOUS_SECTION", "Specify occurrence for a repeated heading.")
	}
	if occ > len(matches) {
		return 0, 0, kbErr("SECTION_NOT_FOUND", "Heading section not found.")
	}
	idx := matches[occ-1]
	end := len(contentLines(string(b)))
	for i := idx + 1; i < len(hs); i++ {
		if hs[i].level <= hs[idx].level {
			end = hs[i].start - 1
			break
		}
	}
	return hs[idx].start, end, nil
}

type cursorRecord struct {
	Offset      int    `json:"offset"`
	Fingerprint string `json:"fingerprint"`
	Generation  int    `json:"generation"`
}

func (s *Service) paginate(p Principal, tool string, a map[string]any, items []any) (map[string]any, error) {
	limit := 50
	if v, present := a["limit"]; present {
		var ok bool
		limit, ok = numeric(v)
		if !ok || limit < 1 || limit > 100 {
			return nil, badArg("limit must be between 1 and 100.")
		}
	}
	copyArgs := map[string]any{}
	for k, v := range a {
		if k != "cursor" && k != "limit" {
			copyArgs[k] = v
		}
	}
	bind, _ := json.Marshal(map[string]any{"identity": p.IdentityID, "admin": p.IsAdmin, "caps": p.Caps, "tool": tool, "args": copyArgs})
	fingerprint := digest(bind)
	var generation int
	s.db.QueryRow(`SELECT value FROM security WHERE key='generation'`).Scan(&generation)
	offset := 0
	if cur := stringArg(a, "cursor"); cur != "" {
		b, e := base64.RawURLEncoding.DecodeString(cur)
		var c cursorRecord
		if e != nil || json.Unmarshal(b, &c) != nil || c.Fingerprint != fingerprint || c.Generation != generation || c.Offset < 0 {
			return nil, kbErr("CURSOR_INVALID", "The cursor is invalid or access has changed.")
		}
		offset = c.Offset
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := map[string]any{"items": items[offset:end], "next_cursor": nil}
	if end < len(items) {
		b, _ := json.Marshal(cursorRecord{Offset: end, Fingerprint: fingerprint, Generation: generation})
		result["next_cursor"] = base64.RawURLEncoding.EncodeToString(b)
	}
	return result, nil
}
func (s *Service) listingScope(p Principal, a map[string]any) (folderRegistry, []folderRegistry, error) {
	if _, fp := a["folder_path"]; fp {
		if _, ip := a["folder_id"]; ip {
			return folderRegistry{}, nil, badArg("Specify one folder locator.")
		}
	}
	r, err := s.findFolder(stringArg(a, "folder_path"), stringArg(a, "folder_id"))
	if err != nil {
		return r, nil, err
	}
	rs, err := s.registries()
	if err != nil {
		return r, nil, err
	}
	if s.effective(p, r.Path) < roleReader {
		visible := false
		for _, child := range rs {
			if within(child.Path, r.Path) && s.effective(p, child.Path) >= roleReader {
				visible = true
				break
			}
		}
		if !visible {
			return r, nil, kbErr("NOT_FOUND", "Resource not found.")
		}
	}
	return r, rs, nil
}
func (s *Service) list(p Principal, tool string, a map[string]any) (any, error) {
	r, rs, err := s.listingScope(p, a)
	if err != nil {
		return nil, err
	}
	depth := 1
	if v, ok := a["depth"]; ok {
		var valid bool
		depth, valid = numeric(v)
		if !valid || depth < 0 || depth > 1024 {
			return nil, badArg("depth must be a nonnegative integer.")
		}
	}
	items := []any{}
	folderItems := []any{}
	entryItems := []any{}
	glob := stringArg(a, "glob")
	if glob != "" {
		if _, e := filepath.Match(glob, "probe.md"); e != nil {
			return nil, badArg("Invalid glob.")
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
	for _, f := range rs {
		if !within(f.Path, r.Path) {
			continue
		}
		rel := strings.TrimPrefix(f.Path, r.Path)
		rel = strings.TrimPrefix(rel, "/")
		fd := 0
		if rel != "" {
			fd = strings.Count(rel, "/") + 1
		}
		if fd > depth {
			continue
		}
		role := s.effective(p, f.Path)
		visible := role >= roleReader
		if !visible {
			for _, child := range rs {
				if within(child.Path, f.Path) && s.effective(p, child.Path) >= roleReader {
					visible = true
					break
				}
			}
		}
		if !visible {
			continue
		}
		if f.Path != r.Path {
			fm := map[string]any{"kind": "folder", "folder_id": f.ID, "folder_path": f.Path, "path": f.Path, "name": filepath.Base(f.Path), "effective_role": roleName(role), "breadcrumb_only": role < roleReader}
			folderItems = append(folderItems, fm)
			items = append(items, fm)
		}
		if tool == "list_knowledgebase_folders" || role < roleReader {
			continue
		}
		sort.Slice(f.Entries, func(i, j int) bool { return f.Entries[i].Path < f.Entries[j].Path })
		for _, e := range f.Entries {
			if stringArg(a, "type") != "" && e.Type != stringArg(a, "type") {
				continue
			}
			if tag := stringArg(a, "tag"); tag != "" && !contains(e.Tags, tag) {
				continue
			}
			if glob != "" {
				match, _ := filepath.Match(glob, e.Filename)
				if !match {
					continue
				}
			}
			m := publicEntry(e)
			m["kind"] = "entry"
			items = append(items, m)
			entryItems = append(entryItems, m)
		}
	}
	out, err := s.paginate(p, tool, a, items)
	if err != nil {
		return nil, err
	}
	out["folder_id"] = r.ID
	out["folder_path"] = r.Path
	out["effective_role"] = roleName(s.effective(p, r.Path))
	folderItems = []any{}
	entryItems = []any{}
	for _, item := range out["items"].([]any) {
		m := asMap(item)
		if stringArg(m, "kind") == "folder" {
			folderItems = append(folderItems, item)
		} else {
			entryItems = append(entryItems, item)
		}
	}
	out["folders"] = folderItems
	out["entries"] = entryItems
	return out, nil
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func (s *Service) search(ctx context.Context, p Principal, a map[string]any) (any, error) {
	query := stringArg(a, "query")
	if query == "" || len(query) > 4000 {
		return nil, badArg("query must contain 1–4000 bytes.")
	}
	r, rs, err := s.listingScope(p, a)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, f := range rs {
		if !within(f.Path, r.Path) || s.effective(p, f.Path) < roleReader {
			continue
		}
		for _, e := range f.Entries {
			if stringArg(a, "type") != "" && e.Type != stringArg(a, "type") {
				continue
			}
			if tag := stringArg(a, "tag"); tag != "" && !contains(e.Tags, tag) {
				continue
			}
			b, e2 := s.entryContent(e)
			if e2 != nil {
				return nil, e2
			}
			matches, matchErr := searchLines(ctx, filepath.Join(s.live, filepath.FromSlash(e.Path)), b, query)
			if matchErr != nil {
				return nil, matchErr
			}
			for _, match := range matches {
				snippet := match.Text
				if len(snippet) > 500 {
					snippet = snippet[:500]
				}
				m := publicEntry(e)
				m["line_number"] = match.Line
				m["snippet"] = snippet
				items = append(items, m)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := asMap(items[i]), asMap(items[j])
		if stringArg(a, "path") == stringArg(b, "path") {
			ai, _ := numeric(a["line_number"])
			bi, _ := numeric(b["line_number"])
			return ai < bi
		}
		return stringArg(a, "path") < stringArg(b, "path")
	})
	out, err := s.paginate(p, "search_knowledgebase", a, items)
	if err != nil {
		return nil, err
	}
	out["matches"] = out["items"]
	return out, nil
}

type lineMatch struct {
	Line int
	Text string
}

// Invoke ripgrep only with already-authorized regular file paths. Private
// registries and sibling scopes are never given to the search process.
func searchLines(ctx context.Context, file string, content []byte, query string) ([]lineMatch, error) {
	if rg, e := exec.LookPath("rg"); e == nil {
		cmd := exec.CommandContext(ctx, rg, "--json", "--no-config", "--fixed-strings", "--ignore-case", "--text", "--line-number", "--", query, file)
		b, e := cmd.Output()
		if e != nil {
			var ee *exec.ExitError
			if !errors.As(e, &ee) || ee.ExitCode() != 1 {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, kbErr("SEARCH_UNAVAILABLE", "Knowledge-base search is temporarily unavailable.")
			}
		}
		out := []lineMatch{}
		decoder := json.NewDecoder(bytes.NewReader(b))
		for {
			var record struct {
				Type string `json:"type"`
				Data struct {
					Line  int `json:"line_number"`
					Lines struct {
						Text string `json:"text"`
					} `json:"lines"`
				} `json:"data"`
			}
			e := decoder.Decode(&record)
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, kbErr("SEARCH_UNAVAILABLE", "Search returned an invalid result.")
			}
			if record.Type == "match" {
				out = append(out, lineMatch{Line: record.Data.Line, Text: strings.TrimSuffix(record.Data.Lines.Text, "\n")})
			}
		}
		return out, nil
	}
	out := []lineMatch{}
	for i, line := range contentLines(string(content)) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
			out = append(out, lineMatch{Line: i + 1, Text: line})
		}
	}
	return out, nil
}
