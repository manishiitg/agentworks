package main

// Remote workflow router.
//
// A workflow lives in exactly one place: this (laptop) workspace, or a remote
// workspace server. The laptop's workspace-api is the single choke point every
// consumer already goes through (agent_go clients, raw HTTP helpers, the
// frontend, the browser tool), so it is where placement is enforced:
//
//   - requests whose path is inside a server-placed workflow are forwarded to
//     that server, with the local docs root rewritten to the server's root;
//   - folder listings merge in the server's placed workflows;
//   - agent-browser commands always run here, because the browser is the
//     user's local Chrome.
//
// Placement lives in <docs>/_system/remote-workflows.json. With no file (or no
// placed workflows) the router is a no-op.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const remotePlacementRelPath = "_system/remote-workflows.json"

type remoteServerConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type remotePlacementConfig struct {
	Servers   map[string]remoteServerConfig `json:"servers"`
	Workflows map[string]string             `json:"workflows"` // "Workflow/<name>" -> server id
}

type remoteRouter struct {
	localRoot string
	client    *http.Client

	mu          sync.Mutex
	cfg         remotePlacementConfig
	cfgModTime  time.Time
	cfgCheck    time.Time
	serverRoots map[string]string // server id -> server docs root
}

func newRemoteRouter(localRoot string) *remoteRouter {
	return &remoteRouter{
		localRoot:   filepath.Clean(localRoot),
		client:      &http.Client{Timeout: 10 * time.Minute},
		serverRoots: map[string]string{},
	}
}

// config returns the current placement config, re-reading the file at most
// once a second when its mtime changes.
func (rr *remoteRouter) config() remotePlacementConfig {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if time.Since(rr.cfgCheck) < time.Second {
		return rr.cfg
	}
	rr.cfgCheck = time.Now()
	cfgPath := filepath.Join(rr.localRoot, filepath.FromSlash(remotePlacementRelPath))
	info, err := os.Stat(cfgPath)
	if err != nil {
		rr.cfg, rr.cfgModTime = remotePlacementConfig{}, time.Time{}
		return rr.cfg
	}
	if info.ModTime().Equal(rr.cfgModTime) {
		return rr.cfg
	}
	raw, err := os.ReadFile(cfgPath) // #nosec G304 -- fixed path under the docs root
	if err != nil {
		log.Printf("[REMOTE_ROUTER] read %s: %v", cfgPath, err)
		return rr.cfg
	}
	var cfg remotePlacementConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Printf("[REMOTE_ROUTER] parse %s: %v", cfgPath, err)
		return rr.cfg
	}
	normalized := map[string]string{}
	for wf, id := range cfg.Workflows {
		if clean := cleanRelPath(wf); clean != "" {
			normalized[clean] = id
		}
	}
	cfg.Workflows = normalized
	rr.cfg, rr.cfgModTime = cfg, info.ModTime()
	return cfg
}

func cleanRelPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	clean := path.Clean("/" + filepath.ToSlash(p))
	return strings.Trim(clean, "/")
}

// relFromAny turns a request path (relative, or absolute under the local
// root) into a clean docs-relative path. ok=false for absolute paths outside
// the local root.
func (rr *remoteRouter) relFromAny(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return "", true
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(rr.localRoot, filepath.Clean(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		p = rel
	}
	return cleanRelPath(p), true
}

// placement returns the (workflow, server id) that owns rel, if any.
func (rr *remoteRouter) placement(cfg remotePlacementConfig, rel string) (string, string) {
	if rel == "" {
		return "", ""
	}
	for wf, id := range cfg.Workflows {
		if rel == wf || strings.HasPrefix(rel, wf+"/") {
			return wf, id
		}
	}
	return "", ""
}

func (rr *remoteRouter) placementForAny(cfg remotePlacementConfig, p string) (string, string) {
	rel, ok := rr.relFromAny(p)
	if !ok {
		return "", ""
	}
	return rr.placement(cfg, rel)
}

func (rr *remoteRouter) serverRoot(ctx context.Context, id string, srv remoteServerConfig) (string, error) {
	rr.mu.Lock()
	root, ok := rr.serverRoots[id]
	rr.mu.Unlock()
	if ok {
		return root, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(srv.URL, "/")+"/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := rr.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote workspace server %q unreachable: %w", id, err)
	}
	defer resp.Body.Close()
	var health struct {
		DocsDir string `json:"docs_dir"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health); err != nil || strings.TrimSpace(health.DocsDir) == "" {
		return "", fmt.Errorf("remote workspace server %q did not report its docs root", id)
	}
	root = filepath.Clean(health.DocsDir)
	rr.mu.Lock()
	rr.serverRoots[id] = root
	rr.mu.Unlock()
	return root, nil
}

var agentBrowserCommandRE = regexp.MustCompile(`(^|[\s;&|(])agent-browser(\s|$)`)

// bridgeCallCommandRE matches code-execution tool calls: shell commands that
// reach the laptop's agent_go tool API through the injected MCP_* variables.
var bridgeCallCommandRE = regexp.MustCompile(`\$\{?MCP_(API_URL|API_TOKEN|CUSTOM|VIRTUAL|MCP|AUTH)\b`)

// mustRunLocally reports whether a command has to run on this machine no
// matter which workflow it belongs to: the browser is the user's Chrome, and
// tool-API calls target this machine's agent_go.
func mustRunLocally(command string, extraEnv map[string]string) bool {
	if agentBrowserCommandRE.MatchString(command) || bridgeCallCommandRE.MatchString(command) {
		return true
	}
	for _, key := range []string{"MCP_API_URL", "MCP_CUSTOM", "MCP_VIRTUAL", "MCP_MCP"} {
		if v := strings.TrimSpace(extraEnv[key]); strings.HasPrefix(v, "http") && strings.Contains(command, v) {
			return true
		}
	}
	return false
}

// middleware routes requests for server-placed workflows. It must be
// registered before the API routes.
func (rr *remoteRouter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqPath := c.Request.URL.Path
		if !strings.HasPrefix(reqPath, "/api/") {
			c.Next()
			return
		}
		if strings.HasPrefix(reqPath, "/api/remote/") && rr.handleRemoteAPI(c) {
			return
		}
		cfg := rr.config()
		if len(cfg.Workflows) == 0 {
			c.Next()
			return
		}
		decision, err := rr.decide(c, cfg)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
			return
		}
		switch decision.kind {
		case routeForward:
			rr.forward(c, cfg, decision.serverID)
		case routeMergeListing:
			rr.mergeListing(c, cfg)
		default:
			c.Next()
		}
	}
}

type routeKind int

const (
	routeLocal routeKind = iota
	routeForward
	routeMergeListing
)

type routeDecision struct {
	kind     routeKind
	serverID string
}

func forwardTo(id string) routeDecision { return routeDecision{kind: routeForward, serverID: id} }

// peekBody reads the request body and puts it back.
func peekBody(c *gin.Context) []byte {
	if c.Request.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(c.Request.Body)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

func (rr *remoteRouter) decide(c *gin.Context, cfg remotePlacementConfig) (routeDecision, error) {
	p := c.Request.URL.Path
	method := c.Request.Method
	byPath := func(candidate string) routeDecision {
		if _, id := rr.placementForAny(cfg, candidate); id != "" {
			return forwardTo(id)
		}
		return routeDecision{}
	}

	switch {
	case strings.HasPrefix(p, "/api/documents/"):
		rel, _ := url.PathUnescape(strings.TrimPrefix(p, "/api/documents/"))
		return byPath(rel), nil

	case p == "/api/documents" && method == http.MethodGet,
		p == "/api/glob", p == "/api/search":
		folder := c.Query("folder")
		if d := byPath(folder); d.kind == routeForward {
			return d, nil
		}
		if p == "/api/documents" && rr.isAncestorOfPlacement(cfg, folder) {
			return routeDecision{kind: routeMergeListing}, nil
		}
		return routeDecision{}, nil

	case p == "/api/documents" && method == http.MethodPost:
		var body struct {
			FilePath string `json:"filepath"`
		}
		_ = json.Unmarshal(peekBody(c), &body)
		return byPath(body.FilePath), nil

	case strings.HasPrefix(p, "/api/versions/"), strings.HasPrefix(p, "/api/restore/"):
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "/api/versions/"), "/api/restore/")
		rel, _ = url.PathUnescape(rel)
		return byPath(rel), nil

	case strings.HasPrefix(p, "/api/folders/") && method == http.MethodDelete:
		rel, _ := url.PathUnescape(strings.TrimPrefix(p, "/api/folders/"))
		return byPath(rel), nil

	case p == "/api/folders" && method == http.MethodPost:
		var body struct {
			FolderPath string `json:"folder_path"`
		}
		_ = json.Unmarshal(peekBody(c), &body)
		return byPath(body.FolderPath), nil

	case p == "/api/folders/copy":
		var body struct {
			SourcePath      string `json:"source_path"`
			DestinationPath string `json:"destination_path"`
		}
		_ = json.Unmarshal(peekBody(c), &body)
		_, src := rr.placementForAny(cfg, body.SourcePath)
		_, dst := rr.placementForAny(cfg, body.DestinationPath)
		if src != dst {
			return routeDecision{}, fmt.Errorf("copying between a local and a server workflow is not supported; move the workflow instead")
		}
		if src != "" {
			return forwardTo(src), nil
		}
		return routeDecision{}, nil

	case p == "/api/upload":
		return byPath(uploadFolderPath(c)), nil

	case p == "/api/query", p == "/api/mutate", p == "/api/report-field",
		p == "/api/db/initialize", p == "/api/db/backup-snapshot", p == "/api/db/tables":
		if dbPath := c.Query("db_path"); dbPath != "" {
			return byPath(dbPath), nil
		}
		var body map[string]interface{}
		_ = json.Unmarshal(peekBody(c), &body)
		for _, key := range []string{"db_path", "workspace_path", "filepath", "file_path", "path"} {
			if s, ok := body[key].(string); ok && s != "" {
				if d := byPath(s); d.kind == routeForward {
					return d, nil
				}
			}
		}
		return routeDecision{}, nil

	case p == "/api/execute":
		return rr.decideExecute(c, cfg)
	}
	return routeDecision{}, nil
}

func (rr *remoteRouter) isAncestorOfPlacement(cfg remotePlacementConfig, folder string) bool {
	rel, ok := rr.relFromAny(folder)
	if !ok {
		return false
	}
	for wf := range cfg.Workflows {
		if rel == "" || strings.HasPrefix(wf, rel+"/") {
			return true
		}
	}
	return false
}

func uploadFolderPath(c *gin.Context) string {
	if v := c.Query("folder_path"); v != "" {
		return v
	}
	mediaType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return ""
	}
	body := peekBody(c)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			return ""
		}
		if part.FormName() == "folder_path" {
			value, _ := io.ReadAll(io.LimitReader(part, 4096))
			return string(value)
		}
	}
}

// decideExecute routes shell commands. Browser and tool-API commands always
// run locally (see mustRunLocally). Anything else runs where its workflow lives,
// judged by working directory, then folder guard, then paths in the command.
func (rr *remoteRouter) decideExecute(c *gin.Context, cfg remotePlacementConfig) (routeDecision, error) {
	var req struct {
		Command          string            `json:"command"`
		WorkingDirectory string            `json:"working_directory"`
		ExtraEnv         map[string]string `json:"extra_env"`
		FolderGuard      *struct {
			ReadPaths  []string `json:"read_paths"`
			WritePaths []string `json:"write_paths"`
		} `json:"folder_guard"`
	}
	body := peekBody(c)
	_ = json.Unmarshal(body, &req)

	if mustRunLocally(req.Command, req.ExtraEnv) {
		// Local execution of a command that belongs to a server workflow:
		// that workflow has no local folder, so its paths are remapped to a
		// local scratch area instead of recreating the folder here.
		if rewritten, changed := rr.remapPlacedPathsToScratch(cfg, body); changed {
			c.Request.Body = io.NopCloser(bytes.NewReader(rewritten))
			c.Request.ContentLength = int64(len(rewritten))
		}
		return routeDecision{}, nil
	}

	servers := map[string]bool{}
	note := func(p string) {
		if _, id := rr.placementForAny(cfg, p); id != "" {
			servers[id] = true
		}
	}
	note(req.WorkingDirectory)
	if len(servers) == 0 && req.FolderGuard != nil {
		for _, p := range req.FolderGuard.WritePaths {
			note(p)
		}
	}
	if len(servers) == 0 {
		for wf, id := range cfg.Workflows {
			if strings.Contains(req.Command, wf) {
				servers[id] = true
			}
		}
	}
	switch len(servers) {
	case 0:
		return routeDecision{}, nil
	case 1:
		for id := range servers {
			return forwardTo(id), nil
		}
	}
	return routeDecision{}, fmt.Errorf("this command touches workflows on more than one workspace server; run it per workflow")
}

const remoteScratchRelPath = "_system/remote-scratch"

// scratchFor maps a path inside a server-placed workflow to the local scratch
// area, keeping its relative or absolute form. It also returns the absolute
// scratch path. ok=false when p is not inside a placed workflow.
func (rr *remoteRouter) scratchFor(cfg remotePlacementConfig, p string) (mapped, abs string, ok bool) {
	rel, okRel := rr.relFromAny(p)
	if !okRel {
		return p, "", false
	}
	_, id := rr.placement(cfg, rel)
	if id == "" {
		return p, "", false
	}
	scratchRel := path.Join(remoteScratchRelPath, id, strings.TrimPrefix(rel, "Workflow/"))
	abs = filepath.Join(rr.localRoot, filepath.FromSlash(scratchRel))
	if filepath.IsAbs(strings.TrimSpace(p)) {
		return abs, abs, true
	}
	return scratchRel, abs, true
}

func (rr *remoteRouter) remapPlacedPathsToScratch(cfg remotePlacementConfig, body []byte) ([]byte, bool) {
	var raw map[string]interface{}
	if json.Unmarshal(body, &raw) != nil {
		return body, false
	}
	changed := false
	if wd, ok := raw["working_directory"].(string); ok {
		if mapped, abs, placed := rr.scratchFor(cfg, wd); placed {
			raw["working_directory"] = mapped
			changed = true
			_ = os.MkdirAll(abs, 0o755) // the working directory must exist
		}
	}
	if fg, ok := raw["folder_guard"].(map[string]interface{}); ok {
		for _, key := range []string{"read_paths", "write_paths", "blocked_paths", "blocked_write_paths"} {
			list, ok := fg[key].([]interface{})
			if !ok {
				continue
			}
			for i, item := range list {
				if s, ok := item.(string); ok {
					if mapped, _, placed := rr.scratchFor(cfg, s); placed {
						list[i] = mapped
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return body, false
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return body, false
	}
	return out, true
}

// rewriteRoots swaps one docs root for another inside a text body.
func rewriteRoots(body []byte, from, to string) []byte {
	if from == "" || to == "" || from == to {
		return body
	}
	return bytes.ReplaceAll(body, []byte(from), []byte(to))
}

func isTextual(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "" || strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "x-ndjson")
}

var hopHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Accept-Encoding": true, "Connection": true,
	"X-Workspace-Token": true, "Authorization": true,
}

// callServer sends one request to a remote server with root rewriting.
func (rr *remoteRouter) callServer(ctx context.Context, id string, srv remoteServerConfig, method, pathAndQuery string, header http.Header, body []byte) (*http.Response, []byte, error) {
	serverRoot, err := rr.serverRoot(ctx, id, srv)
	if err != nil {
		return nil, nil, err
	}
	if isTextual(header.Get("Content-Type")) {
		body = rewriteRoots(body, rr.localRoot, serverRoot)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(srv.URL, "/")+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range header {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if srv.Token != "" {
		req.Header.Set("X-Workspace-Token", srv.Token)
		req.Header.Set("Authorization", "Bearer "+srv.Token)
	}
	resp, err := rr.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("remote workspace server %q: %w", id, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if isTextual(resp.Header.Get("Content-Type")) {
		respBody = rewriteRoots(respBody, serverRoot, rr.localRoot)
	}
	return resp, respBody, nil
}

func (rr *remoteRouter) forward(c *gin.Context, cfg remotePlacementConfig, id string) {
	srv, ok := cfg.Servers[id]
	if !ok || strings.TrimSpace(srv.URL) == "" {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"success": false, "error": fmt.Sprintf("workflow is placed on unknown workspace server %q", id)})
		return
	}
	body := peekBody(c)
	pathAndQuery := c.Request.URL.EscapedPath()
	if c.Request.URL.RawQuery != "" {
		pathAndQuery += "?" + c.Request.URL.RawQuery
	}
	resp, respBody, err := rr.callServer(c.Request.Context(), id, srv, c.Request.Method, pathAndQuery, c.Request.Header, body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	for k, vs := range resp.Header {
		canonical := http.CanonicalHeaderKey(k)
		if strings.HasPrefix(canonical, "Access-Control-") {
			continue // this server's CORS middleware already set them
		}
		switch canonical {
		case "Content-Length", "Content-Encoding", "Transfer-Encoding", "Connection":
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.Header().Set("X-Workspace-Placement", id)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
	c.Abort()
}

// ---- listing merge ----

type listingNode map[string]interface{}

func nodePath(n listingNode) string {
	s, _ := n["filepath"].(string)
	return cleanRelPath(s)
}

type captureWriter struct {
	gin.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *captureWriter) Write(b []byte) (int, error)       { return w.buf.Write(b) }
func (w *captureWriter) WriteString(s string) (int, error) { return w.buf.WriteString(s) }
func (w *captureWriter) WriteHeader(code int)              { w.status = code }
func (w *captureWriter) WriteHeaderNow()                   {}
func (w *captureWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *captureWriter) Written() bool { return w.buf.Len() > 0 || w.status != 0 }
func (w *captureWriter) Size() int     { return w.buf.Len() }

// mergeListing runs the local listing, then grafts in each server's listing
// restricted to that server's placed workflows. Local nodes inside a placed
// workflow are dropped: the server copy is the only copy.
func (rr *remoteRouter) mergeListing(c *gin.Context, cfg remotePlacementConfig) {
	original := c.Writer
	capture := &captureWriter{ResponseWriter: original}
	c.Writer = capture
	c.Next()
	c.Writer = original

	var local map[string]interface{}
	if capture.Status() != http.StatusOK || json.Unmarshal(capture.buf.Bytes(), &local) != nil {
		original.WriteHeader(capture.Status())
		_, _ = original.Write(capture.buf.Bytes())
		return
	}
	localNodes := toNodes(local["data"])
	placedBy := map[string][]string{}
	for wf, id := range cfg.Workflows {
		placedBy[id] = append(placedBy[id], wf)
	}
	isPlaced := func(p string) bool {
		_, id := rr.placement(cfg, p)
		return id != ""
	}
	merged := filterNodes(localNodes, func(p string) bool { return !isPlaced(p) })

	ids := make([]string, 0, len(placedBy))
	for id := range placedBy {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		srv, ok := cfg.Servers[id]
		if !ok {
			continue
		}
		pathAndQuery := "/api/documents"
		if c.Request.URL.RawQuery != "" {
			pathAndQuery += "?" + c.Request.URL.RawQuery
		}
		_, body, err := rr.callServer(c.Request.Context(), id, srv, http.MethodGet, pathAndQuery, c.Request.Header, nil)
		if err != nil {
			log.Printf("[REMOTE_ROUTER] listing from %s failed: %v", id, err)
			continue
		}
		var remote map[string]interface{}
		if json.Unmarshal(body, &remote) != nil {
			continue
		}
		mine := func(p string) bool {
			for _, wf := range placedBy[id] {
				if p == wf || strings.HasPrefix(p, wf+"/") || strings.HasPrefix(wf, p+"/") {
					return true
				}
			}
			return false
		}
		merged = mergeNodes(merged, filterNodes(toNodes(remote["data"]), mine))
	}
	local["data"] = merged
	out, err := json.Marshal(local)
	if err != nil {
		original.WriteHeader(capture.Status())
		_, _ = original.Write(capture.buf.Bytes())
		return
	}
	original.Header().Set("Content-Type", "application/json; charset=utf-8")
	original.WriteHeader(http.StatusOK)
	_, _ = original.Write(out)
}

func toNodes(v interface{}) []listingNode {
	arr, _ := v.([]interface{})
	out := make([]listingNode, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, listingNode(m))
		}
	}
	return out
}

func filterNodes(nodes []listingNode, keep func(string) bool) []listingNode {
	out := make([]listingNode, 0, len(nodes))
	for _, n := range nodes {
		if !keep(nodePath(n)) {
			continue
		}
		if children, ok := n["children"]; ok && children != nil {
			n["children"] = nodesToAny(filterNodes(toNodes(children), keep))
		}
		out = append(out, n)
	}
	return out
}

// mergeNodes unions two node lists by filepath; b wins for leaf conflicts and
// children are merged recursively.
func mergeNodes(a, b []listingNode) []listingNode {
	index := map[string]int{}
	out := make([]listingNode, 0, len(a)+len(b))
	for _, n := range a {
		index[nodePath(n)] = len(out)
		out = append(out, n)
	}
	for _, n := range b {
		p := nodePath(n)
		if i, ok := index[p]; ok {
			existing := out[i]
			_, ac := existing["children"]
			_, bc := n["children"]
			if ac || bc {
				existing["children"] = nodesToAny(mergeNodes(toNodes(existing["children"]), toNodes(n["children"])))
				continue
			}
			out[i] = n
			continue
		}
		index[p] = len(out)
		out = append(out, n)
	}
	return out
}

func nodesToAny(nodes []listingNode) []interface{} {
	out := make([]interface{}, len(nodes))
	for i, n := range nodes {
		out[i] = map[string]interface{}(n)
	}
	return out
}
