package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

// workspaceProxyHandler creates an http.Handler that reverse-proxies to the workspace API.
// It strips the /api/wp prefix so /api/wp/api/documents → WORKSPACE_API_URL/api/documents.
// Auth is enforced by the router's AuthMiddleware (applied to all /api/* routes).
func workspaceProxyHandler() http.Handler {
	wsURL := os.Getenv("WORKSPACE_API_URL")
	if wsURL == "" {
		wsURL = "http://localhost:8080"
	}

	target, err := url.Parse(wsURL)
	if err != nil {
		log.Printf("[WORKSPACE PROXY] Invalid WORKSPACE_API_URL: %v", err)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "workspace proxy misconfigured", http.StatusBadGateway)
		})
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	// The agent server's own CORS middleware answers the browser; the
	// workspace server adds its own permissive headers too, and a response
	// carrying two Access-Control-Allow-Origin values is rejected by every
	// browser. Strip the upstream's so only ours remain.
	proxy.ModifyResponse = func(resp *http.Response) error {
		for name := range resp.Header {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				resp.Header.Del(name)
			}
		}
		return nil
	}
	log.Printf("[WORKSPACE PROXY] Proxying /api/wp/* → %s", wsURL)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A browser may only address its own private tree. The workspace
		// server resolves explicit _users/<id>/... paths verbatim with no
		// identity of its own, so the proxy — the one hop that knows the
		// JWT claims — refuses any cross-user addressing in the URL, in
		// query params, and in body path fields, and refuses any body it
		// cannot fully inspect. Crew Run mode depends on this: reader
		// file access goes through the mediated crew endpoints, never
		// through raw cross-user proxy reads, and no proxied call may
		// write another user's tree.
		if claims := GetUserFromContext(r.Context()); claims != nil {
			status, detail, cleanup := workspaceProxyCrossUserBlock(r, claims.UserID)
			if cleanup != nil {
				defer cleanup()
			}
			if status != 0 {
				log.Printf("[WORKSPACE PROXY] Denied workspace access for %q: %s (status %d)", claims.UserID, detail, status)
				message := detail
				if status == http.StatusForbidden {
					message = "cross-user workspace access denied"
				}
				http.Error(w, message, status)
				return
			}
		}
		// Keep the retired workflow-files path closed, and never expose the
		// internal shared-assets endpoint through the generic proxy.
		if internalPath := path.Clean("/" + workspaceProxyRelativePath(r)); internalPath == "/api/workflow-files" || internalPath == "/api/shared-assets" {
			http.NotFound(w, r)
			return
		}
		// Live browser access must go through workflow ownership and input gating.
		if strings.HasPrefix(workspaceProxyRelativePath(r), "api/browser/live/") {
			http.NotFound(w, r)
			return
		}
		if isWorkflowWorkspaceProxyWrite(r) {
			if !currentUserCanWriteWorkflows(r) {
				writeWorkflowPermissionDenied(w, "write")
				return
			}
			// Inside an existing workflow's folder only its owners may
			// write; a new folder under Workflow/ is creation, covered by
			// the account tier above.
			if folder := workflowFolderFromWorkspaceProxyPath(workspaceProxyRelativePath(r)); folder != "" {
				if level, manifest := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), folder); manifest != nil && level != WorkflowAccessOwner && level != WorkflowAccessWrite {
					writeWorkflowPermissionDenied(w, "owner")
					return
				}
			}
		}
		// The workspace API scopes per-user paths by X-User-ID and has no
		// auth of its own; it must carry the identity this server verified,
		// never whatever the browser put in the header.
		r.Header.Set("X-User-ID", GetUserIDFromContext(r.Context()))
		// The workspace service token is this server's alone; the transport
		// (wsauth, installed at startup) attaches it upstream.
		r.Header.Del(wsauth.HeaderName)
		// A write to a workflow's dashboard files refreshes open Report views.
		if rel := workspaceProxyRelativePath(r); isWorkflowWorkspaceProxyWrite(r) && liveFeedReportPath(rel) {
			defer publishReportChanged(strings.TrimPrefix(strings.TrimPrefix(rel, "api/documents/"), "api/folders/"))
		}
		// Strip /api/wp prefix: /api/wp/api/documents → /api/documents
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/wp")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		r.URL.RawPath = ""
		r.Host = target.Host
		proxy.ServeHTTP(w, r)
	})
}

// workspaceProxyRelativePath is the decoded path below /api/wp, without a
// leading slash: "api/documents/Workflow/<folder>/plan.json".
func workspaceProxyRelativePath(r *http.Request) string {
	path := strings.TrimPrefix(r.URL.Path, "/api/wp")
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	return strings.TrimPrefix(path, "/")
}

// workspaceProxyCrossUserBlock vets a proxied browser request. It returns a
// nonzero HTTP status when the request must not reach the workspace
// server: 403 for confirmed cross-user addressing (URL, query, or body
// path fields, mirroring the workspace server's own resolution, where
// explicit _users/<id>/... paths resolve verbatim and logical paths
// resolve under the stamped X-User-ID), 413 when the body is too large to
// fully inspect, and 400 when the body is unreadable or malformed. Zero
// means allow; allowed requests carry a replayable body with an explicit
// length. The cleanup func is nil unless the body spooled to disk, in
// which case the caller must defer it: it is idempotent with the
// transport's own body close.
func workspaceProxyCrossUserBlock(r *http.Request, callerID string) (status int, detail string, cleanup func()) {
	policy := newWorkspaceProxyPolicy(r, callerID)
	rel := workspaceProxyRelativePath(r)
	if workspaceProxyURLIsOtherUser(rel, policy.own) {
		return http.StatusForbidden, "url path", nil
	}
	if target, ok := workspaceProxyURLTarget(rel); ok {
		if reason := policy.denies("", target); reason != "" {
			return http.StatusForbidden, "url path: " + reason, nil
		}
	}
	query := r.URL.Query()
	for _, key := range []string{"folder", "pattern", "db_path", "path", "filepath", "file_path", "source_path", "destination_path"} {
		for _, value := range query[key] {
			if workspaceProxyPathIsOtherUser(value, policy.own) {
				return http.StatusForbidden, "query param " + key, nil
			}
			if reason := policy.denies(key, value); reason != "" {
				return http.StatusForbidden, "query param " + key + ": " + reason, nil
			}
		}
	}
	// Bulk routes without an explicit folder act on the whole docs root.
	if policy.bulk && query.Get("folder") == "" && r.Method == http.MethodGet {
		if reason := policy.denies("folder", ""); reason != "" {
			return http.StatusForbidden, "whole workspace: " + reason, nil
		}
	}
	return workspaceProxyBodyVerdict(r, policy)
}

// workspaceProxyRoutePathPrefixes are workspace routes whose trailing path
// parameter is a workspace path. The check strips the route before testing
// the workspace path itself, mirroring how the workspace server resolves
// it: a leading _users/<id>/ addresses that user's tree verbatim, while
// logical per-user paths resolve under the stamped X-User-ID.
var workspaceProxyRoutePathPrefixes = []string{
	"api/documents/",
	"api/folders/",
	"api/versions/",
	"api/restore/",
}

func workspaceProxyURLIsOtherUser(rel, own string) bool {
	remainder := strings.Trim(rel, "/")
	for _, prefix := range workspaceProxyRoutePathPrefixes {
		if after, ok := strings.CutPrefix(remainder, prefix); ok {
			remainder = after
			break
		}
	}
	return workspaceProxyPathIsOtherUser(remainder, own)
}

// workspaceProxyPathIsOtherUser matches values that address another
// user's tree: the bare _users root, or _users/<segment>/... with a
// segment that is not the caller's own. A shared crew root, Crew/<id>, is
// the owner's tree too: raw access is the manifest owner's only (readers
// use the mediated /shared-projects endpoints), the bare Crew root is never
// listable, and a crew nobody owns (or that does not exist yet: crews are
// created server-side) is refused.
func workspaceProxyPathIsOtherUser(raw, own string) bool {
	clean := strings.Trim(path.Clean("/"+strings.TrimSpace(raw)), "/")
	if clean == "_users" || clean == crewSharedRootName {
		return true
	}
	if strings.HasPrefix(clean, crewSharedRootName+"/") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ref, ok := resolveCrewPath(ctx, own, clean)
		return !ok || ref.OwnerID == "" || ref.OwnerID != own
	}
	if !strings.HasPrefix(clean, "_users/") {
		return false
	}
	segment := strings.SplitN(strings.TrimPrefix(clean, "_users/"), "/", 2)[0]
	return segment != "" && segment != own
}

// workspaceProxyBodyPathFields are the JSON field names that carry
// workspace paths (folder creation, move/copy, database paths, guard
// paths, shell working directories). Command and content fields are
// deliberately absent: only path-typed fields are inspected, so patch
// text or shell commands mentioning _users/ never false-positive.
var workspaceProxyBodyPathFields = map[string]bool{
	"folder": true, "folder_path": true, "filepath": true, "file_path": true,
	"path": true, "source_path": true, "destination_path": true,
	"source": true, "destination": true, "db_path": true,
	"workspace_path":    true,
	"working_directory": true, "working_dir": true,
	"read_paths": true, "write_paths": true, "blocked_paths": true, "blocked_write_paths": true,
}

// workspaceProxyMultipartPathFields are the multipart form fields that
// carry workspace paths (file upload destination, backup import target).
// They mirror the workspace request models exactly: FileUploadRequest
// binds only folder_path, the backup import binds only workspace_path,
// and both endpoints require an actual file part, so no other field —
// and no file part — can smuggle a path. A future path-bearing form
// field must be added here with its model.
var workspaceProxyMultipartPathFields = map[string]bool{
	"folder_path": true, "workspace_path": true,
}

// workspaceProxyBodyMemoryCap bounds the in-memory prefix of a proxied
// request body; larger bodies spill to a temp file. workspaceProxyBodyMaxCap
// is the hard rejection limit past it. Both are vars so tests can force
// the spill and rejection paths with small bodies; production values
// cover the workspace server's own traffic (10MB upload files, large
// restores and document saves) while bounding per-request disk use.
var workspaceProxyBodyMemoryCap = int64(8 << 20)
var workspaceProxyBodyMaxCap = int64(256 << 20)

// workspaceProxySpooledBody holds a fully buffered request body: memory
// for small bodies, a temp file past the memory cap. Readers are always
// fresh at offset zero; close removes the temp file and is idempotent,
// so the transport's body close and the handler's deferred cleanup can
// both run.
type workspaceProxySpooledBody struct {
	mem  []byte
	file *os.File
	size int64
}

func (s *workspaceProxySpooledBody) open() io.Reader {
	if s.file != nil {
		_, _ = s.file.Seek(0, io.SeekStart)
		return s.file
	}
	return bytes.NewReader(s.mem)
}

func (s *workspaceProxySpooledBody) close() {
	if s.file == nil {
		return
	}
	name := s.file.Name()
	_ = s.file.Close()
	_ = os.Remove(name)
	s.file = nil
}

// workspaceProxyReplayBody replays a spooled body upstream and releases
// its temp file on close.
type workspaceProxyReplayBody struct {
	io.Reader
	spooled *workspaceProxySpooledBody
}

func (b *workspaceProxyReplayBody) Close() error {
	b.spooled.close()
	return nil
}

// workspaceProxySpoolBody buffers the full request body for inspection:
// memory up to the memory cap, a temp file past it, rejection past the
// hard cap. Unknown (chunked) lengths stream through the same writer, so
// framing never exempts a body from inspection. Bodies past the hard cap
// and unreadable streams fail closed with a rejection status.
func workspaceProxySpoolBody(r *http.Request) (*workspaceProxySpooledBody, int, string) {
	if r.ContentLength > workspaceProxyBodyMaxCap {
		return nil, http.StatusRequestEntityTooLarge, "request body too large to inspect"
	}
	spooled := &workspaceProxySpooledBody{}
	if r.Body == nil || r.ContentLength == 0 {
		return spooled, 0, ""
	}
	spill := &workspaceProxySpillWriter{mem: &bytes.Buffer{}, memCap: workspaceProxyBodyMemoryCap, maxCap: workspaceProxyBodyMaxCap}
	_, err := io.Copy(spill, r.Body)
	_ = r.Body.Close()
	if err != nil {
		spill.abort()
		if errors.Is(err, errWorkspaceProxyBodyTooLarge) {
			return nil, http.StatusRequestEntityTooLarge, "request body too large to inspect"
		}
		return nil, http.StatusBadRequest, "request body could not be read"
	}
	if spill.file != nil {
		spooled.file = spill.file
	} else {
		spooled.mem = spill.mem.Bytes()
	}
	spooled.size = spill.total
	return spooled, 0, ""
}

var errWorkspaceProxyBodyTooLarge = errors.New("request body exceeds inspection cap")

// workspaceProxySpillWriter buffers a body in memory up to memCap, then
// spills to a temp file, and fails writes past maxCap.
type workspaceProxySpillWriter struct {
	mem    *bytes.Buffer
	file   *os.File
	total  int64
	memCap int64
	maxCap int64
}

func (w *workspaceProxySpillWriter) Write(p []byte) (int, error) {
	if w.total+int64(len(p)) > w.maxCap {
		return 0, errWorkspaceProxyBodyTooLarge
	}
	if w.file == nil && w.total+int64(len(p)) > w.memCap {
		f, err := os.CreateTemp("", "workspace-proxy-body-*")
		if err != nil {
			return 0, err
		}
		if _, err := f.Write(w.mem.Bytes()); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return 0, err
		}
		w.file = f
		w.mem = nil
	}
	var (
		n   int
		err error
	)
	if w.file != nil {
		n, err = w.file.Write(p)
	} else {
		n, err = w.mem.Write(p)
	}
	w.total += int64(n)
	return n, err
}

// abort drops a partially spooled body after a failed read.
func (w *workspaceProxySpillWriter) abort() {
	if w.file == nil {
		return
	}
	_ = w.file.Close()
	_ = os.Remove(w.file.Name())
	w.file = nil
}

// workspaceProxyBodyVerdict inspects a request body for cross-user path
// fields. JSON bodies are decoded (first value, mirroring the workspace
// server's gin binding, which also ignores trailing bytes); multipart
// bodies are walked part by part in full, so field order — including the
// client's own file-first uploads — never hides a path field. Anything
// unreadable or malformed is rejected, matching the 400 the workspace
// server would answer anyway. Bodies without field structure pass through
// to the URL and query checks: no proxied endpoint binds paths from raw
// bodies (JSON endpoints use ShouldBindJSON; the two multipart endpoints
// require file parts).
func workspaceProxyBodyVerdict(r *http.Request, policy workspaceProxyPolicy) (status int, detail string, cleanup func()) {
	spooled, status, detail := workspaceProxySpoolBody(r)
	if status != 0 {
		return status, detail, nil
	}
	replay := func() (int, string, func()) {
		// The replay body closes the spool, so the transport's own body
		// close already cleans up; the deferred cleanup is the backstop
		// for verdicts that never reach the transport.
		r.Body = &workspaceProxyReplayBody{Reader: spooled.open(), spooled: spooled}
		r.ContentLength = spooled.size
		if spooled.file == nil {
			return 0, "", nil
		}
		return 0, "", spooled.close
	}
	if spooled.size == 0 {
		r.Body = http.NoBody
		r.ContentLength = 0
		return 0, "", nil
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "json") {
		var decoded any
		if err := json.NewDecoder(spooled.open()).Decode(&decoded); err != nil {
			spooled.close()
			return http.StatusBadRequest, "request body is not valid JSON", nil
		}
		if workspaceProxyJSONAddressesOtherUser(decoded, policy) {
			spooled.close()
			return http.StatusForbidden, "request body path", nil
		}
		return replay()
	}
	if strings.HasPrefix(contentType, "multipart/") {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") || params["boundary"] == "" {
			spooled.close()
			return http.StatusBadRequest, "request body is not valid multipart", nil
		}
		partReader := multipart.NewReader(spooled.open(), params["boundary"])
		for {
			part, err := partReader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				spooled.close()
				return http.StatusBadRequest, "request body is not valid multipart", nil
			}
			if part.FileName() == "" && workspaceProxyMultipartPathFields[part.FormName()] {
				value, err := io.ReadAll(part)
				if err != nil {
					spooled.close()
					return http.StatusBadRequest, "request body could not be read", nil
				}
				if policy.deniesPath(part.FormName(), string(value)) {
					spooled.close()
					return http.StatusForbidden, "multipart form path", nil
				}
				continue
			}
			// File content and unrelated fields stream past; only the
			// verdict matters, since replay re-reads the spool.
			if _, err := io.Copy(io.Discard, part); err != nil {
				spooled.close()
				return http.StatusBadRequest, "request body could not be read", nil
			}
		}
		return replay()
	}
	return replay()
}

func workspaceProxyJSONAddressesOtherUser(node any, policy workspaceProxyPolicy) bool {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if !workspaceProxyBodyPathFields[key] {
				if workspaceProxyJSONAddressesOtherUser(child, policy) {
					return true
				}
				continue
			}
			switch held := child.(type) {
			case string:
				if policy.deniesPath(key, held) {
					return true
				}
			case []any:
				for _, entry := range held {
					if text, ok := entry.(string); ok && policy.deniesPath(key, text) {
						return true
					}
				}
			}
		}
	case []any:
		for _, entry := range value {
			if workspaceProxyJSONAddressesOtherUser(entry, policy) {
				return true
			}
		}
	}
	return false
}

func isWorkflowWorkspaceProxyWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	path := workspaceProxyRelativePath(r)

	return strings.HasPrefix(path, "api/documents/Workflow/") ||
		path == "api/documents/Workflow" ||
		strings.HasPrefix(path, "api/folders/Workflow/") ||
		path == "api/folders/Workflow"
}
