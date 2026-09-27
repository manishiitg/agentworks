package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// Multi-file "*** Begin Patch" support for diff_patch_workspace_file.
//
// Codex and Cursor usually change several files in one patch (59% of ~3,950
// real patches). The workspace server patches one file per request, so a
// multi-file patch is split into one-file patches here, every file is checked
// with a dry run, and only then are they written, in order. A write that fails
// after others succeeded restores those files from the originals read before
// writing. Nothing is half-applied, unlike applying file by file, which is how
// a half-edited library broke a workflow on RTS (2026-09-27).

const applyPatchBeginMarker = "*** Begin Patch"

// ApplyPatchSection is one file of a "*** Begin Patch" patch, rewrapped as a
// single-file patch.
type ApplyPatchSection struct {
	Path  string
	IsAdd bool
	Patch string
}

// IsApplyPatchFormat reports whether diff is a "*** Begin Patch" patch.
func IsApplyPatchFormat(diff string) bool {
	return strings.HasPrefix(strings.TrimSpace(diff), applyPatchBeginMarker)
}

// SplitApplyPatch splits a "*** Begin Patch" patch into one patch per file.
// Delete File and Move to are refused, as the single-file tool refuses them.
func SplitApplyPatch(diff string) ([]ApplyPatchSection, error) {
	if !IsApplyPatchFormat(diff) {
		return nil, nil
	}
	var sections []ApplyPatchSection
	var body []string
	var current *ApplyPatchSection
	flush := func() {
		if current == nil {
			return
		}
		current.Patch = applyPatchBeginMarker + "\n" + strings.Join(body, "\n") + "\n*** End Patch\n"
		sections = append(sections, *current)
		current, body = nil, nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, applyPatchBeginMarker):
			continue
		case strings.HasPrefix(line, "*** End Patch"):
			flush()
			return sections, nil
		case strings.HasPrefix(line, "*** Update File:"), strings.HasPrefix(line, "*** Add File:"):
			flush()
			isAdd := strings.HasPrefix(line, "*** Add File:")
			path := strings.TrimSpace(line[strings.Index(line, ":")+1:])
			if path == "" {
				return nil, fmt.Errorf("%q names no file", line)
			}
			current = &ApplyPatchSection{Path: path, IsAdd: isAdd}
			body = []string{line}
		case strings.HasPrefix(line, "*** Delete File:"):
			return nil, fmt.Errorf("*** Delete File is not supported by diff_patch_workspace_file; use the workspace delete tool")
		case strings.HasPrefix(line, "*** Move to:"):
			return nil, fmt.Errorf("*** Move to is not supported by diff_patch_workspace_file; use the workspace move tool, then patch the file")
		default:
			if current == nil {
				if strings.TrimSpace(line) != "" {
					return nil, fmt.Errorf("patch has content before an *** Update File or *** Add File header: %q", line)
				}
				continue
			}
			body = append(body, line)
		}
	}
	flush()
	return sections, nil
}

// DiffPatchTargetPaths returns every file a diff_patch_workspace_file call
// writes: its filepath argument, and for a multi-file "*** Begin Patch" (or
// one without filepath) every file the patch names. Every write check (folder
// guard, blocked-write prefixes such as a non-owner's workflow.json) must use
// this, not the filepath argument alone, or a multi-file patch walks past it.
func DiffPatchTargetPaths(args map[string]interface{}) []string {
	var paths []string
	fp, _ := args["filepath"].(string)
	if strings.TrimSpace(fp) != "" {
		paths = append(paths, fp)
	}
	diff, _ := args["diff"].(string)
	if !IsApplyPatchFormat(diff) {
		return paths
	}
	sections, err := SplitApplyPatch(diff)
	if err != nil || (len(sections) <= 1 && len(paths) > 0) {
		return paths
	}
	for _, section := range sections {
		paths = append(paths, section.Path)
	}
	return paths
}

// isMultiFileApplyPatch reports whether a call must take the multi-file path:
// a "*** Begin Patch" naming several files, or naming its file while the
// filepath argument is empty.
func isMultiFileApplyPatch(params DiffPatchWorkspaceFileParams) ([]ApplyPatchSection, bool, error) {
	if !IsApplyPatchFormat(params.Diff) {
		return nil, false, nil
	}
	sections, err := SplitApplyPatch(params.Diff)
	if err != nil {
		return nil, false, err
	}
	if len(sections) > 1 || (len(sections) == 1 && strings.TrimSpace(params.Filepath) == "") {
		return sections, true, nil
	}
	return nil, false, nil
}

type multiPatchFile struct {
	section  ApplyPatchSection
	path     string // resolved workspace-relative path
	original string
	existed  bool
	applied  bool
}

// diffPatchMultiFile checks every file, then writes them all or none.
func (c *Client) diffPatchMultiFile(ctx context.Context, sections []ApplyPatchSection) (DiffPatchResult, error) {
	files := make([]*multiPatchFile, 0, len(sections))
	seen := map[string]bool{}
	for _, section := range sections {
		path := c.resolveLinkedFolderPath(ctx, section.Path)
		path = stripWorkspacePrefix(path)
		path = c.resolveGuardRelativeWorkspacePath(ctx, path)
		if err := c.ValidatePathWithContext(ctx, path, true); err != nil {
			return DiffPatchResult{}, fmt.Errorf("%s: %w (nothing was changed)", section.Path, err)
		}
		if filepath.IsAbs(path) {
			return DiffPatchResult{}, fmt.Errorf("%s is in an attached folder; patch attached-folder files one per call (nothing was changed)", section.Path)
		}
		if seen[path] {
			return DiffPatchResult{}, fmt.Errorf("the patch names %s more than once; combine its changes into one section (nothing was changed)", section.Path)
		}
		seen[path] = true
		files = append(files, &multiPatchFile{section: section, path: path})
	}

	// Originals, for restoring if a later write fails.
	for _, f := range files {
		read, err := c.ReadWorkspaceFile(ctx, ReadWorkspaceFileParams{Filepath: f.path})
		switch {
		case err == nil:
			f.existed, f.original = true, read.Content
		case strings.HasPrefix(err.Error(), "file not found:"):
			f.existed = false
		default:
			return DiffPatchResult{}, fmt.Errorf("%s: cannot read the current file: %w (nothing was changed)", f.section.Path, err)
		}
	}

	// Check every file before writing any.
	for _, f := range files {
		if _, err := c.patchWorkspaceDocument(ctx, f.path, f.section.Patch, true); err != nil {
			return DiffPatchResult{}, fmt.Errorf("%s: %w (nothing was changed)", f.section.Path, err)
		}
	}

	for _, f := range files {
		if _, err := c.patchWorkspaceDocument(ctx, f.path, f.section.Patch, false); err != nil {
			restored, failed := c.restoreMultiPatchFiles(ctx, files)
			return DiffPatchResult{}, fmt.Errorf("%s: %w; restored %d already-patched file(s)%s", f.section.Path, err, restored, failed)
		}
		f.applied = true
		noteReportFileWrite(f.path)
	}

	summary := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		summary = append(summary, map[string]interface{}{"filepath": f.path, "created": !f.existed})
	}
	data, _ := json.Marshal(map[string]interface{}{"applied": true, "files": summary})
	return DiffPatchResult{Data: data}, nil
}

// restoreMultiPatchFiles puts back every file this call already wrote.
func (c *Client) restoreMultiPatchFiles(ctx context.Context, files []*multiPatchFile) (int, string) {
	restored := 0
	var failed []string
	for _, f := range files {
		if !f.applied {
			continue
		}
		var err error
		if f.existed {
			_, err = c.UpdateWorkspaceFile(ctx, UpdateWorkspaceFileParams{Filepath: f.path, Content: f.original})
		} else {
			_, err = c.DeleteWorkspaceFile(ctx, DeleteWorkspaceFileParams{Filepath: f.path})
		}
		if err != nil {
			failed = append(failed, f.path+" ("+err.Error()+")")
			continue
		}
		restored++
	}
	if len(failed) > 0 {
		return restored, "; COULD NOT restore: " + strings.Join(failed, ", ")
	}
	return restored, ""
}

// patchWorkspaceDocument sends one file's patch to the workspace server,
// optionally as a dry run.
func (c *Client) patchWorkspaceDocument(ctx context.Context, path, diff string, dryRun bool) (json.RawMessage, error) {
	body := map[string]interface{}{"diff": diff}
	if dryRun {
		body["dry_run"] = true
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	apiURL := c.BaseURL + "/api/documents/" + encodeWorkspaceDocumentPath(path) + "/diff"
	req, err := http.NewRequestWithContext(ctx, "PATCH", apiURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("workspace API error: %s", apiResp.Error)
	}
	return apiResp.Data, nil
}
