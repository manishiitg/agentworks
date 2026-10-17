package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Installed only on the browser proxy, after its original logical-path access
// checks. This maps reads to one source revision; it grants no additional paths.
func publicationProxyRead(r *http.Request) (string, string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", ""
	}
	mapPath := func(raw string) (string, string, string) {
		ref, ok := resolveCrewPath(r.Context(), GetUserIDFromContext(r.Context()), raw)
		if !ok {
			return raw, "", ""
		}
		p, err := readPublishedProject(r.Context(), ref.Root)
		if err != nil || p == nil {
			return raw, "", ""
		}
		actual := publishedReadPath(r.Context(), raw)
		if ref.Rest == "" {
			actual = p.sourceRoot()
		}
		if actual == raw {
			return raw, "", ""
		}
		r.Header.Set("X-Publication-Project", p.Root)
		logical := strings.TrimSuffix(raw, "/"+ref.Rest)
		if ref.Rest == "" {
			logical = raw
		}
		return actual, p.sourceRoot(), logical
	}
	var source, logical string
	for _, prefix := range []string{"/api/wp/api/documents/", "/api/wp/api/folders/"} {
		if raw, ok := strings.CutPrefix(r.URL.Path, prefix); ok {
			actual, s, l := mapPath(raw)
			if s != "" {
				r.URL.Path = prefix + actual
				r.URL.RawPath = ""
				source, logical = s, l
			}
			break
		}
	}
	q := r.URL.Query()
	for _, key := range []string{"folder", "path", "filepath", "file_path"} {
		if raw := q.Get(key); raw != "" {
			actual, s, l := mapPath(raw)
			if s != "" {
				q.Set(key, actual)
				source, logical = s, l
			}
		}
	}
	r.URL.RawQuery = q.Encode()
	return source, logical
}

// A listing names the same logical files the browser asked for. File contents
// are not rewritten. Raw Builder history never enters either source or listing.
func publicationProxyResponse(resp *http.Response) error {
	if resp.Request == nil || resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	source := resp.Request.Header.Get("X-Publication-Source")
	logical := resp.Request.Header.Get("X-Publication-Logical")
	if source == "" || logical == "" {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	var doc any
	if json.Unmarshal(raw, &doc) == nil {
		var walk func(any)
		walk = func(node any) {
			switch value := node.(type) {
			case map[string]any:
				for key, item := range value {
					if key == "filepath" || key == "path" || key == "folder" || key == "folder_path" {
						if path, ok := item.(string); ok {
							if rel, ok := publicationPathBelow(path, source); ok {
								value[key] = logical + rel
							}
						}
					} else if key != "content" {
						walk(item)
					}
				}
			case []any:
				for _, item := range value {
					walk(item)
				}
			}
		}
		walk(doc)
		// Runtime results remain live at the ordinary Crew root. Merge them
		// into folder listings; an uploaded source file keeps its meaning.
		if resp.Request.URL.Path == "/api/documents" && resp.Request.URL.Query().Get("folder") != "" {
			if object, ok := doc.(map[string]any); ok {
				project := resp.Request.Header.Get("X-Publication-Project")
				folder := resp.Request.URL.Query().Get("folder")
				if suffix, ok := publicationPathBelow(folder, source); ok && project != "" {
					request := resp.Request.Clone(resp.Request.Context())
					query := request.URL.Query()
					query.Set("folder", project+suffix)
					request.URL.RawQuery = query.Encode()
					request.Header.Del("X-Publication-Source")
					request.Header.Del("X-Publication-Logical")
					request.Header.Del("X-Publication-Project")
					response, e := workspaceHTTPClient.Do(request)
					if e != nil {
						return e
					}
					var live map[string]any
					decodeErr := json.NewDecoder(response.Body).Decode(&live)
					response.Body.Close()
					if decodeErr != nil {
						return decodeErr
					}
					if entries, ok := live["data"].([]any); ok {
						entries = publicationRuntimeDocuments(entries, project, logical)
						saved, _ := object["data"].([]any)
						object["data"] = publicationMergeDocuments(saved, entries)
					}
				}
			}
		}
		if changed, e := json.Marshal(doc); e == nil {
			raw = changed
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Del("Content-Length")
	return nil
}

func publicationPathBelow(full, root string) (string, bool) {
	for _, prefix := range []string{root, publicationLogicalPath(root)} {
		if full == prefix {
			return "", true
		}
		if strings.HasPrefix(full, prefix+"/") {
			return strings.TrimPrefix(full, prefix), true
		}
	}
	return "", false
}
func publicationLogicalPath(p string) string {
	parts := strings.SplitN(p, "/", 3)
	if len(parts) == 3 && parts[0] == "_users" {
		return parts[2]
	}
	return p
}

func publicationRuntimeDocuments(entries []any, project, logical string) []any {
	out := []any{}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		p, _ := item["filepath"].(string)
		rel, ok := publicationPathBelow(p, project)
		if !ok {
			continue
		}
		if rel != "" && !publishedRuntimeRelative(strings.TrimPrefix(rel, "/")) {
			continue
		}
		item["filepath"] = logical + rel
		if children, ok := item["children"].([]any); ok {
			item["children"] = publicationRuntimeDocuments(children, project, logical)
		}
		out = append(out, item)
	}
	return out
}
func publicationMergeDocuments(source, runtime []any) []any {
	seen := map[string]map[string]any{}
	for _, entry := range source {
		if item, ok := entry.(map[string]any); ok {
			p, _ := item["filepath"].(string)
			seen[p] = item
		}
	}
	for _, entry := range runtime {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		p, _ := item["filepath"].(string)
		if existing, ok := seen[p]; ok {
			if existing["type"] == "folder" {
				children, _ := existing["children"].([]any)
				more, _ := item["children"].([]any)
				existing["children"] = publicationMergeDocuments(children, more)
			}
		} else {
			source = append(source, item)
			seen[p] = item
		}
	}
	return source
}
