package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DiffPatchWorkspaceFileParams contains parameters for the diff_patch_workspace_file tool
type DiffPatchWorkspaceFileParams struct {
	Filepath string `json:"filepath"`
	Diff     string `json:"diff"`
}

// DiffPatchWorkspaceFile applies a unified diff patch to a file
func (c *Client) DiffPatchWorkspaceFile(ctx context.Context, params DiffPatchWorkspaceFileParams) (DiffPatchResult, error) {
	if params.Diff == "" {
		return DiffPatchResult{}, fmt.Errorf("diff is required")
	}
	// A "*** Begin Patch" naming several files (or naming its file with no
	// filepath argument) is checked in full, then written all or none.
	if sections, multi, err := isMultiFileApplyPatch(params); err != nil {
		return DiffPatchResult{}, err
	} else if multi {
		return c.diffPatchMultiFile(ctx, sections)
	}
	if params.Filepath == "" {
		return DiffPatchResult{}, fmt.Errorf("filepath is required")
	}

	params.Filepath = c.resolveLinkedFolderPath(ctx, params.Filepath)

	// Normalize absolute paths to workspace-relative before building the URL.
	// LLMs often send absolute paths (e.g. "/app/workspace-docs/Workflow/..."
	// or native desktop paths ending in "/workspace-docs/Workflow/...").
	// The workspace HTTP handler does its own validation with the real docs-dir,
	// but the filepath goes into the URL path so it must be relative.
	params.Filepath = stripWorkspacePrefix(params.Filepath)
	params.Filepath = c.resolveGuardRelativeWorkspacePath(ctx, params.Filepath)
	if err := c.ValidatePathWithContext(ctx, params.Filepath, true); err != nil {
		return DiffPatchResult{}, err
	}
	if filepath.IsAbs(params.Filepath) {
		guard := c.resolveEffectiveFolderGuard(ctx)
		if guard == nil || !guard.Enabled {
			return DiffPatchResult{}, fmt.Errorf("external diff requires a session folder grant")
		}
		respBody, err := c.request(ctx, "POST", "/api/external/diff", map[string]interface{}{
			"filepath":     params.Filepath,
			"diff":         params.Diff,
			"folder_guard": guard,
		})
		if err != nil {
			return DiffPatchResult{}, err
		}
		var apiResp APIResponse
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			return DiffPatchResult{}, fmt.Errorf("failed to parse external diff response: %w", err)
		}
		if !apiResp.Success {
			return DiffPatchResult{}, fmt.Errorf("workspace API error: %s", apiResp.Error)
		}
		return DiffPatchResult{Data: apiResp.Data}, nil
	}

	// Build API URL for diff patching
	apiURL := c.BaseURL + "/api/documents/" + encodeWorkspaceDocumentPath(params.Filepath) + "/diff"

	// Prepare request body
	requestBody := map[string]interface{}{
		"diff": params.Diff,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return DiffPatchResult{}, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PATCH", apiURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return DiffPatchResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return DiffPatchResult{}, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := readResponseBody(resp)
	if err != nil {
		return DiffPatchResult{}, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return DiffPatchResult{}, fmt.Errorf("failed to parse API response: %w", err)
	}

	if !apiResp.Success {
		return DiffPatchResult{}, fmt.Errorf("workspace API error: %s", apiResp.Error)
	}
	noteReportFileWrite(params.Filepath)

	return DiffPatchResult{
		Data: apiResp.Data,
	}, nil
}

// resolveGuardRelativeWorkspacePath gives file tools the same project-root
// relative semantics as the coding CLI shell. A project agent runs with its
// cwd at the sole granted project root, so `MEMORY.md` must resolve to that
// root for diff_patch_workspace_file as well. Multiple writable roots are
// intentionally left unresolved because choosing one would be ambiguous.
func (c *Client) resolveGuardRelativeWorkspacePath(ctx context.Context, input string) string {
	clean := filepath.Clean(strings.TrimSpace(input))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return input
	}
	firstPart := strings.SplitN(filepath.ToSlash(clean), "/", 2)[0]
	switch firstPart {
	case "_users", "Chats", "Workflow":
		// These are already workspace-root-qualified. A denied qualified path
		// must stay denied rather than being smuggled below the project root.
		return clean
	}
	guard := c.resolveEffectiveFolderGuard(ctx)
	if guard == nil || !guard.Enabled || len(guard.WritePaths) == 0 {
		return input
	}
	if validatePathAgainstGuard(guard, clean, true) == nil {
		return clean
	}
	candidates := make(map[string]struct{})
	for _, root := range guard.WritePaths {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "." || isExactFolderGuardFilePath(root) {
			continue
		}
		candidate := filepath.Join(root, clean)
		if validatePathAgainstGuard(guard, candidate, true) == nil {
			candidates[candidate] = struct{}{}
		}
	}
	if len(candidates) != 1 {
		return input
	}
	for candidate := range candidates {
		return candidate
	}
	return input
}

func (c *Client) resolveLinkedFolderPath(ctx context.Context, input string) string {
	const prefix = "linked://"
	if !strings.HasPrefix(input, prefix) {
		return input
	}
	remainder := strings.TrimPrefix(input, prefix)
	parts := strings.SplitN(remainder, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return input
	}
	aliasKey := linkedFolderEnvKey(parts[0])
	if aliasKey == "" {
		return input
	}
	sessionID := c.sessionIDFromContext(ctx)
	session := GetSessionShellConfig(sessionID)
	root := ""
	if session != nil {
		root = strings.TrimSpace(session.Env["WORKFLOW_FOLDER_"+aliasKey])
		if root == "" {
			// Work product attached folders use the WORK_FOLDER_ prefix.
			root = strings.TrimSpace(session.Env["WORK_FOLDER_"+aliasKey])
		}
	}
	relative := filepath.Clean(filepath.FromSlash(parts[1]))
	if root == "" || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return input
	}
	return filepath.Join(root, relative)
}

func linkedFolderEnvKey(alias string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToUpper(strings.TrimSpace(alias)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
		} else if !underscore && b.Len() > 0 {
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func encodeWorkspaceDocumentPath(path string) string {
	pathSegments := strings.Split(path, "/")
	encodedSegments := make([]string, len(pathSegments))
	for i, segment := range pathSegments {
		encodedSegments[i] = url.PathEscape(segment)
	}
	return strings.Join(encodedSegments, "/")
}

// stripWorkspacePrefix converts absolute workspace paths to relative.
// Checks WORKSPACE_DOCS_PATH env first (desktop/Mac), discovered local
// workspace-docs roots next, then Docker defaults.
func stripWorkspacePrefix(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	cleanPath := filepath.Clean(p)
	for _, root := range workspaceDocsRootsForPatch() {
		root = filepath.Clean(root)
		if cleanPath == root {
			return "."
		}
		prefix := root + string(filepath.Separator)
		if strings.HasPrefix(cleanPath, prefix) {
			return strings.TrimPrefix(cleanPath, prefix)
		}
	}
	return p
}

func workspaceDocsRootsForPatch() []string {
	var roots []string
	if envRoot := os.Getenv("WORKSPACE_DOCS_PATH"); envRoot != "" {
		roots = append(roots, envRoot)
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			roots = append(roots, filepath.Join(dir, "workspace-docs"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	roots = append(roots, "/app/workspace-docs", "/workspace-docs")
	return deduplicatePathStrings(roots)
}

func deduplicatePathStrings(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "." || p == "" {
			continue
		}
		if _, exists := seen[p]; exists {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
