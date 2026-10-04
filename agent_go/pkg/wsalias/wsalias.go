// Package wsalias translates old spellings of a workspace path into where the folder lives now, at the one place every
// request to the workspace service passes: its HTTP transport (PLAT-442 step 4).
//
// A Crew that moved to the shared root keeps answering to the paths it had ("_users/<owner>/Chats/Work/projects/<f>" and
// the owner's "Chats/Work/projects/<f>"): typed by a person, stored in a chat history, a schedule, a bot destination,
// sent by a browser that has not been reloaded. The workspace service itself knows nothing about the move, and an old
// spelling that reached it would not find the Crew (a read) or would CREATE an empty folder at the old place (a write).
// This transport rewrites the path arguments of a request before it leaves, using a resolver the server installs (the
// server-controlled owner registry's alias list). It rewrites the URL path of the document, folder, version and restore
// routes, the path-valued query parameters, and the path fields of a JSON body; nothing else.
//
// It is a translation, never an authorization: the server's own gates decide who may reach the translated path.
package wsalias

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Resolver maps a workspace path argument, as the workspace service would read it for user (an empty user is the
// default user), to the path it names now. ok is false when the path needs no change.
type Resolver func(userID, path string) (rewritten string, ok bool)

var resolver atomic.Value // Resolver

// SetResolver installs the server's resolver. Until one is installed the transport is a pass-through.
func SetResolver(r Resolver) { resolver.Store(r) }

func current() Resolver {
	r, _ := resolver.Load().(Resolver)
	return r
}

// routePrefixes are the workspace routes whose trailing URL path is a workspace path.
var routePrefixes = []string{"/api/documents/", "/api/folders/", "/api/versions/", "/api/restore/"}

// queryPathKeys are the query parameters that carry a workspace path.
var queryPathKeys = []string{"folder", "pattern", "db_path", "path", "filepath", "file_path", "source_path", "destination_path"}

// bodyPathFields are the JSON fields that carry a workspace path (or a list of them).
var bodyPathFields = map[string]bool{
	"folder": true, "folder_path": true, "filepath": true, "file_path": true, "path": true, "source_path": true,
	"destination_path": true, "source": true, "destination": true, "db_path": true, "workspace_path": true,
	"working_directory": true, "working_dir": true, "read_paths": true, "write_paths": true, "blocked_paths": true,
	"blocked_write_paths": true,
}

// maxRewriteBody bounds the JSON bodies that are inspected; a larger body passes through untouched (the path fields
// of the routes that take one are small).
const maxRewriteBody = 4 << 20

// Transport wraps base (nil means http.DefaultTransport at call time) with the rewrite.
func Transport(base http.RoundTripper) http.RoundTripper {
	return roundTripper{base: base}
}

type roundTripper struct{ base http.RoundTripper }

func (t roundTripper) next() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

func (t roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resolve := current()
	if resolve == nil {
		return t.next().RoundTrip(req)
	}
	user := req.Header.Get("X-User-ID")
	out := req
	clone := func() *http.Request {
		if out == req {
			out = req.Clone(req.Context())
		}
		return out
	}
	// URL path.
	for _, prefix := range routePrefixes {
		after, ok := strings.CutPrefix(req.URL.Path, prefix)
		if !ok {
			continue
		}
		if rewritten, changed := resolve(user, after); changed {
			c := clone()
			c.URL.Path = prefix + rewritten
			c.URL.RawPath = ""
		}
		break
	}
	// Query.
	if req.URL.RawQuery != "" {
		query := req.URL.Query()
		changed := false
		for _, key := range queryPathKeys {
			for i, value := range query[key] {
				if rewritten, ok := resolve(user, value); ok {
					query[key][i] = rewritten
					changed = true
				}
			}
		}
		if changed {
			clone().URL.RawQuery = query.Encode()
		}
	}
	// JSON body.
	if req.Body != nil && req.Body != http.NoBody && req.ContentLength > 0 && req.ContentLength <= maxRewriteBody && jsonish(req.Header.Get("Content-Type")) {
		raw, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body := raw
		if rewritten, changed := rewriteJSON(raw, user, resolve); changed {
			body = rewritten
		}
		c := clone()
		c.Body = io.NopCloser(bytes.NewReader(body))
		c.ContentLength = int64(len(body))
		c.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	return t.next().RoundTrip(out)
}

func jsonish(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return contentType == "" || strings.Contains(contentType, "json") || strings.HasPrefix(contentType, "text/plain")
}

func rewriteJSON(raw []byte, user string, resolve Resolver) ([]byte, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return raw, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return raw, false
	}
	if !rewriteNode(document, user, resolve) {
		return raw, false
	}
	out, err := json.Marshal(document)
	if err != nil {
		return raw, false
	}
	return out, true
}

func rewriteNode(node any, user string, resolve Resolver) bool {
	changed := false
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if !bodyPathFields[key] {
				if rewriteNode(child, user, resolve) {
					changed = true
				}
				continue
			}
			switch held := child.(type) {
			case string:
				if rewritten, ok := resolve(user, held); ok {
					value[key] = rewritten
					changed = true
				}
			case []any:
				for i, entry := range held {
					if text, isText := entry.(string); isText {
						if rewritten, ok := resolve(user, text); ok {
							held[i] = rewritten
							changed = true
						}
					}
				}
			}
		}
	case []any:
		for _, entry := range value {
			if rewriteNode(entry, user, resolve) {
				changed = true
			}
		}
	}
	return changed
}
