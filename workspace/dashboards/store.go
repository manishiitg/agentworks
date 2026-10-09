// Package dashboards stores authored dashboards as immutable bundles. Publication
// changes one manifest pointer only after every bundle file has been written.
package dashboards

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const MaxBytes = 8 << 20
const Prefix = "db/reports/managed/"

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var revisionID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Request struct {
	Root             string            `json:"root"`
	Action           string            `json:"action"`
	ID               string            `json:"id,omitempty"`
	Title            string            `json:"title,omitempty"`
	Files            map[string]string `json:"files,omitempty"`
	Remove           []string          `json:"remove,omitempty"`
	ExpectedRevision string            `json:"expected_revision,omitempty"`
	Revision         string            `json:"revision,omitempty"`
	DocumentPath     string            `json:"document_path,omitempty"`
	IncludeDraft     bool              `json:"include_draft,omitempty"`
	Guard            *wf.FolderGuard   `json:"guard,omitempty"`
}
type Snapshot struct {
	Title string            `json:"title"`
	Files map[string]string `json:"files"`
}
type Dashboard struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Revision     string   `json:"revision"`
	Published    string   `json:"published_revision,omitempty"`
	History      []string `json:"history,omitempty"`
	DocumentPath string   `json:"document_path"`
	Managed      bool     `json:"managed"`
}
type Result struct {
	Dashboards   []Dashboard `json:"dashboards,omitempty"`
	Dashboard    *Dashboard  `json:"dashboard,omitempty"`
	Snapshot     *Snapshot   `json:"snapshot,omitempty"`
	ResolvedPath string      `json:"resolved_path,omitempty"`
}
type state struct {
	ID        string   `json:"id"`
	Draft     string   `json:"draft"`
	Published string   `json:"published"`
	History   []string `json:"history"`
}

func fail(status int, message string) error { return &wf.FileError{Status: status, Message: message} }
func safeFile(p string) bool {
	clean, err := wf.CleanRelative(p)
	if err != nil || clean != p || p == "." || len(p) > 240 {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(p))
	if strings.HasPrefix(p, "scripts/") {
		return ext == ".py" || ext == ".js" || ext == ".mjs"
	}
	switch ext {
	case ".html", ".css", ".js", ".mjs", ".json", ".svg", ".txt", ".csv", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".woff", ".woff2":
		return true
	}
	return false
}
func StatePath(id string) string { return "db/reports/.dashboard-state/" + id + "/state.json" }
func Document(id, rev string) string {
	if rev == "" {
		return Prefix + id + "/index.html"
	}
	return Prefix + id + "/" + rev + "/index.html"
}
func snapshotPath(id, rev string) string {
	return "db/reports/.dashboard-state/" + id + "/" + rev + ".json"
}
func renderFile(id, rev, p string) string {
	if strings.HasPrefix(p, "scripts/") {
		return "code/reports/managed/" + id + "/" + rev + "/" + strings.TrimPrefix(p, "scripts/")
	}
	return Prefix + id + "/" + rev + "/" + p
}
func readJSON(root *os.Root, p string, out any) error {
	if err := checkParents(root, p); err != nil {
		return err
	}
	info, err := root.Lstat(p)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fail(403, "unsafe dashboard file")
	}
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, MaxBytes+1))
	return d.Decode(out)
}
func safeParents(root *os.Root, p string) error {
	parts := strings.Split(path.Dir(p), "/")
	current := ""
	for _, part := range parts {
		current = path.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			if err = root.Mkdir(current, 0755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(403, "unsafe dashboard directory")
		}
	}
	return nil
}
func atomicWrite(root *os.Root, p string, data []byte) error {
	if err := safeParents(root, p); err != nil {
		return err
	}
	if info, err := root.Lstat(p); err == nil && !info.Mode().IsRegular() {
		return fail(403, "unsafe dashboard file")
	}
	tmp := p + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(tmp, p)
}
func summary(s state, snap Snapshot, draft bool) Dashboard {
	rev := s.Published
	if draft {
		rev = s.Draft
	}
	d := Dashboard{ID: s.ID, Title: snap.Title, Revision: rev, Published: s.Published, DocumentPath: Document(s.ID, ""), Managed: true}
	if draft {
		d.History = s.History
	}
	return d
}
func loadState(root *os.Root, id string) (state, error) {
	var s state
	err := readJSON(root, StatePath(id), &s)
	return s, err
}
func loadSnapshot(root *os.Root, id, rev string) (Snapshot, error) {
	var s Snapshot
	if !revisionID.MatchString(rev) {
		return s, fail(404, "dashboard revision not found")
	}
	err := readJSON(root, snapshotPath(id, rev), &s)
	return s, err
}
func revisionReadable(s state, rev string, draft bool) bool {
	if draft {
		return true
	}
	if rev == s.Published {
		return true
	}
	for _, r := range s.History {
		if r == rev {
			return true
		}
	}
	return false
}

func Execute(ctx context.Context, docs string, req Request) (Result, error) {
	var out Result
	clean, err := wf.CleanRelative(req.Root)
	if err != nil || clean != req.Root || clean == "." {
		return out, fail(400, "canonical project root required")
	}
	release, err := wf.LockWorkspace(ctx, docs)
	if err != nil {
		return out, err
	}
	defer release()
	docsRoot, err := os.OpenRoot(docs)
	if err != nil {
		return out, err
	}
	defer docsRoot.Close()
	// Reject links in every project-root component, not merely escapes from docs.
	current := ""
	for _, part := range strings.Split(clean, "/") {
		current = path.Join(current, part)
		info, e := docsRoot.Lstat(current)
		if e != nil {
			return out, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return out, fail(403, "unsafe project root")
		}
	}
	root, err := docsRoot.OpenRoot(clean)
	if err != nil {
		return out, err
	}
	defer root.Close()
	if req.Action == "list" {
		dir, e := root.Open("db/reports/.dashboard-state")
		if e == nil {
			entries, e := dir.ReadDir(-1)
			dir.Close()
			if e != nil && !errors.Is(e, io.EOF) {
				return out, e
			}
			for _, entry := range entries {
				if !entry.IsDir() || !identifier.MatchString(entry.Name()) {
					continue
				}
				s, e := loadState(root, entry.Name())
				if e != nil {
					return out, e
				}
				rev := s.Published
				if req.IncludeDraft {
					rev = s.Draft
				}
				if rev == "" {
					continue
				}
				snap, e := loadSnapshot(root, s.ID, rev)
				if e != nil {
					return out, e
				}
				if req.Guard == nil || req.Guard.Allows(Document(s.ID, rev), false) {
					out.Dashboards = append(out.Dashboards, summary(s, snap, req.IncludeDraft))
				}
			}
		}
		if e != nil && !os.IsNotExist(e) {
			return out, e
		}
		// Existing authored documents are discoverable without moving or rewriting them.
		var walk func(string, int) error
		walk = func(p string, depth int) error {
			dir, e := root.Open(p)
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			defer dir.Close()
			entries, e := dir.ReadDir(-1)
			if e != nil && !errors.Is(e, io.EOF) {
				return e
			}
			for _, v := range entries {
				if strings.HasPrefix(v.Name(), ".") || v.Name() == "managed" || v.Name() == "preview" || v.Type()&os.ModeSymlink != 0 {
					continue
				}
				f := path.Join(p, v.Name())
				if v.IsDir() {
					if depth > 0 {
						if e = walk(f, depth-1); e != nil {
							return e
						}
					}
				} else if strings.HasSuffix(strings.ToLower(f), ".html") {
					if req.Guard != nil && !req.Guard.Allows(f, false) {
						continue
					}
					out.Dashboards = append(out.Dashboards, Dashboard{ID: f, Title: strings.TrimSuffix(v.Name(), ".html"), DocumentPath: f})
				}
			}
			return nil
		}
		if err = walk("db/reports", 3); err != nil {
			return out, err
		}
		sort.Slice(out.Dashboards, func(i, j int) bool { return out.Dashboards[i].ID < out.Dashboards[j].ID })
		return out, nil
	}
	if req.Action == "resolve" {
		p := req.DocumentPath
		if clean, e := wf.CleanRelative(p); e != nil || clean != p {
			return out, fail(400, "canonical document path required")
		}
		if req.Guard != nil && !req.Guard.Allows(p, false) {
			return out, fail(403, "outside connection read grants")
		}
		prefix := Prefix
		if strings.HasPrefix(p, "code/reports/managed/") {
			prefix = "code/reports/managed/"
		}
		if !strings.HasPrefix(p, prefix) {
			out.ResolvedPath = p
			return out, nil
		}
		parts := strings.Split(strings.TrimPrefix(p, prefix), "/")
		if len(parts) < 2 || !identifier.MatchString(parts[0]) {
			return out, fail(400, "invalid dashboard path")
		}
		req.ID = parts[0]
		s, e := loadState(root, req.ID)
		if e != nil {
			return out, e
		}
		if revisionID.MatchString(parts[1]) {
			if !revisionReadable(s, parts[1], req.IncludeDraft) {
				return out, fail(403, "draft requires project edit access")
			}
			out.ResolvedPath = p
			return out, nil
		}
		if s.Published == "" {
			return out, fail(404, "dashboard is not published")
		}
		out.ResolvedPath = prefix + req.ID + "/" + s.Published + "/" + strings.Join(parts[1:], "/")
		return out, nil
	}
	if !identifier.MatchString(req.ID) {
		return out, fail(400, "dashboard id must be a lowercase slug of at most 64 characters")
	}
	s, err := loadState(root, req.ID)
	if req.Action == "create" {
		if err == nil {
			return out, fail(409, "dashboard already exists")
		}
		if !os.IsNotExist(err) {
			return out, err
		}
		s = state{ID: req.ID}
		err = nil
	}
	if err != nil {
		return out, err
	}
	if req.Action == "get" {
		rev := s.Published
		if req.IncludeDraft {
			rev = s.Draft
		}
		if req.Revision != "" {
			rev = req.Revision
		}
		if !revisionReadable(s, rev, req.IncludeDraft) {
			return out, fail(403, "draft requires project edit access")
		}
		snap, e := loadSnapshot(root, req.ID, rev)
		if e != nil {
			return out, e
		}
		if req.Guard != nil {
			for p := range snap.Files {
				if !req.Guard.Allows(renderFile(req.ID, rev, p), false) {
					delete(snap.Files, p)
				}
			}
		}
		d := summary(s, snap, req.IncludeDraft)
		d.Revision = rev
		out.Dashboard = &d
		out.Snapshot = &snap
		out.ResolvedPath = Document(req.ID, rev)
		return out, nil
	}
	if !req.IncludeDraft {
		return out, fail(403, "project edit access required")
	}
	if req.Action != "create" && req.ExpectedRevision != s.Draft {
		return out, fail(409, "dashboard revision conflict; reread before editing")
	}
	if req.Action == "publish" || req.Action == "restore" {
		rev := s.Draft
		if req.Action == "restore" {
			rev = req.Revision
			if !revisionReadable(s, rev, false) {
				return out, fail(400, "restore requires a previously published revision")
			}
		}
		snap, e := loadSnapshot(root, req.ID, rev)
		if e != nil {
			return out, e
		}
		for p := range snap.Files {
			if req.Guard != nil && !req.Guard.Allows(renderFile(req.ID, rev, p), true) {
				return out, fail(403, "publication outside connection write grants")
			}
		}
		s.Published = rev
		if req.Action == "restore" {
			s.Draft = rev
		}
		if !contains(s.History, rev) {
			s.History = append(s.History, rev)
		}
		data, e := json.Marshal(s)
		if e != nil {
			return out, e
		}
		if e = atomicWrite(root, StatePath(req.ID), data); e != nil {
			return out, e
		}
		d := summary(s, snap, true)
		out.Dashboard = &d
		out.Snapshot = &snap
		out.ResolvedPath = Document(req.ID, rev)
		return out, nil
	}
	if req.Action != "create" && req.Action != "update" {
		return out, fail(400, "unknown dashboard action")
	}
	snap := Snapshot{Title: req.Title, Files: map[string]string{}}
	if s.Draft != "" {
		snap, err = loadSnapshot(root, req.ID, s.Draft)
		if err != nil {
			return out, err
		}
		if req.Title != "" {
			snap.Title = req.Title
		}
	}
	if len(snap.Title) > 200 || strings.TrimSpace(snap.Title) == "" {
		return out, fail(400, "dashboard title required (at most 200 characters)")
	}
	for p, content := range req.Files {
		if !safeFile(p) || !utf8.ValidString(content) {
			return out, fail(400, "files must be UTF-8 HTML/assets or scripts under scripts/")
		}
		snap.Files[p] = content
	}
	for _, p := range req.Remove {
		if !safeFile(p) {
			return out, fail(400, "invalid removed file")
		}
		delete(snap.Files, p)
	}
	if _, ok := snap.Files["index.html"]; !ok {
		return out, fail(400, "index.html required")
	}
	if len(snap.Files) > 100 {
		return out, fail(413, "at most 100 files per dashboard")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return out, err
	}
	if len(data) > MaxBytes {
		return out, fail(413, "dashboard exceeds 8 MiB")
	}
	hash := sha256.Sum256(data)
	rev := hex.EncodeToString(hash[:])
	// A token folder grant cannot be widened by using a dashboard instead of write_file.
	for p := range snap.Files {
		if req.Guard != nil && !req.Guard.Allows(renderFile(req.ID, rev, p), true) {
			return out, fail(403, "dashboard file outside connection write grants")
		}
	}
	for p, content := range snap.Files {
		content = strings.ReplaceAll(content, "{{dashboard_assets}}", Prefix+req.ID+"/"+rev)
		content = strings.ReplaceAll(content, "{{dashboard_scripts}}", "code/reports/managed/"+req.ID+"/"+rev)
		bytes, decodeErr := assetBytes(p, content)
		if decodeErr != nil {
			return out, decodeErr
		}
		if err = atomicWrite(root, renderFile(req.ID, rev, p), bytes); err != nil {
			return out, err
		}
	}
	if err = atomicWrite(root, snapshotPath(req.ID, rev), data); err != nil {
		return out, err
	}
	s.Draft = rev
	encoded, err := json.Marshal(s)
	if err != nil {
		return out, err
	}
	if err = atomicWrite(root, StatePath(req.ID), encoded); err != nil {
		return out, err
	}
	d := summary(s, snap, true)
	out.Dashboard = &d
	out.Snapshot = &snap
	out.ResolvedPath = Document(req.ID, rev)
	return out, nil
}
func contains(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

func assetBytes(p, content string) ([]byte, error) {
	media := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".woff": "font/woff", ".woff2": "font/woff2"}[strings.ToLower(path.Ext(p))]
	if media == "" {
		return []byte(content), nil
	}
	prefix := "data:" + media + ";base64,"
	if !strings.HasPrefix(content, prefix) {
		return nil, fail(400, "binary assets require matching base64 data URLs")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(content, prefix))
	if err != nil {
		return nil, fail(400, "invalid base64 asset")
	}
	return data, nil
}

func checkParents(root *os.Root, p string) error {
	current := ""
	for _, part := range strings.Split(path.Dir(p), "/") {
		current = path.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(403, "unsafe dashboard directory")
		}
	}
	return nil
}
