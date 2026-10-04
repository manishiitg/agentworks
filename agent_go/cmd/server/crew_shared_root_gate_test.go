package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// PLAT-442 step 4, the critical safety item. Today the workspace proxy's refusal of a foreign `_users/<id>/...`
// is the ONLY thing keeping a reader out of a Crew's files (a Crew lives in its owner's tree). `Crew/<id>` has no
// `_users` segment, so without its own gate any signed-in user could read and write every Crew. These tests drive
// the gate through every shape of request the proxy vets (URL route, query, JSON body, multipart, odd spellings)
// and require that a Crew at `Crew/<id>` is exactly as closed as the same Crew in its owner's tree.

const (
	gateOwner    = "alice"   // owns Crew/sde-1a2b
	gateOther    = "mallory" // owns Crew/mal-9f00
	gateReader   = "bob"     // has the Crew product, owns neither
	gateNoCrew   = "carol"   // no Crew product
	gateAdmin    = "root"    // administrator, owns neither
	gateCrew     = "Crew/sde-1a2b"
	gateOtherCrw = "Crew/mal-9f00"
)

func setupSharedCrewGate(t *testing.T) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"root","username":"root","role":"admin","products":["work"]},
		{"id":"alice","username":"alice","role":"editor","products":["work"]},
		{"id":"mallory","username":"mallory","role":"editor","products":["work"]},
		{"id":"bob","username":"bob","role":"editor","products":["work"]},
		{"id":"carol","username":"carol","role":"editor","products":["agentworks"]}]}`)
	t.Setenv("WORKSPACE_DOCS_PATH", gateDocsRoot)
	stubCrewLookups(t, map[string]string{gateCrew: gateOwner, gateOtherCrw: gateOther}, nil)
}

// The document root as the workspace service sees it: it strips this prefix off an absolute path argument.
const gateDocsRoot = "/srv/agentworks/docs"

func gateCtx(user string) context.Context {
	return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
}

// gateStatus runs the proxy's gate for one request as user and returns the refusing status (0 = allowed).
func gateStatus(t *testing.T, user string, req *http.Request) int {
	t.Helper()
	req = req.WithContext(gateCtx(user))
	status, _, cleanup := workspaceProxyCrossUserBlock(req, user)
	if cleanup != nil {
		cleanup()
	}
	return status
}

type gateShape struct {
	name string
	// build returns the request addressing workspace path p.
	build func(p string) *http.Request
}

// gateRequest is a request whose URL path is exactly path (spaces, backslashes and dots included, which
// httptest.NewRequest refuses to parse from a request line).
func gateRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, "/", bytes.NewReader(body))
	req.URL.Path = path
	return req
}

func gateJSON(method, route string, fields func(p string) map[string]any) func(p string) *http.Request {
	return func(p string) *http.Request {
		data, _ := json.Marshal(fields(p))
		req := gateRequest(method, "/api/wp/"+route, data)
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

func gateShapes() []gateShape {
	var shapes []gateShape
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		method := method
		for _, route := range []string{"api/documents/", "api/folders/", "api/versions/", "api/restore/"} {
			route := route
			shapes = append(shapes, gateShape{method + " " + route, func(p string) *http.Request {
				if method == http.MethodGet || method == http.MethodDelete {
					return gateRequest(method, "/api/wp/"+route+p, nil)
				}
				return gateJSON(method, route+p, func(string) map[string]any { return map[string]any{"content": "x"} })(p)
			}})
		}
	}
	for _, key := range []string{"folder", "pattern", "db_path", "path", "filepath", "file_path", "source_path", "destination_path"} {
		key := key
		shapes = append(shapes, gateShape{"GET query " + key, func(p string) *http.Request {
			return httptest.NewRequest(http.MethodGet, "/api/wp/api/documents?"+key+"="+url.QueryEscape(p), nil)
		}})
	}
	shapes = append(shapes, gateShape{"GET search folder", func(p string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/wp/api/search?query=x&folder="+url.QueryEscape(p), nil)
	}})
	for _, field := range []string{"folder", "folder_path", "filepath", "file_path", "path", "source_path", "destination_path", "source", "destination", "db_path", "workspace_path", "working_directory", "working_dir"} {
		field := field
		shapes = append(shapes, gateShape{"POST json " + field, gateJSON(http.MethodPost, "api/folders", func(p string) map[string]any { return map[string]any{field: p} })})
		shapes = append(shapes, gateShape{"POST json nested " + field, gateJSON(http.MethodPost, "api/execute-free", func(p string) map[string]any {
			return map[string]any{"outer": map[string]any{"list": []any{map[string]any{field: p}}}}
		})})
	}
	for _, field := range []string{"read_paths", "write_paths", "blocked_paths", "blocked_write_paths"} {
		field := field
		shapes = append(shapes, gateShape{"POST json array " + field, gateJSON(http.MethodPost, "api/folders", func(p string) map[string]any {
			return map[string]any{"folder_guard": map[string]any{field: []any{"Chats/mine", p}}}
		})})
	}
	shapes = append(shapes, gateShape{"POST json as text/plain", func(p string) *http.Request {
		data, _ := json.Marshal(map[string]any{"folder_path": p})
		req := httptest.NewRequest(http.MethodPost, "/api/wp/api/folders", bytes.NewReader(data))
		req.Header.Set("Content-Type", "text/plain")
		return req
	}})
	for _, field := range []string{"folder_path", "workspace_path"} {
		field := field
		shapes = append(shapes, gateShape{"POST multipart " + field, func(p string) *http.Request {
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			_ = w.WriteField(field, p)
			part, _ := w.CreateFormFile("file", "a.txt")
			_, _ = part.Write([]byte("data"))
			_ = w.Close()
			req := httptest.NewRequest(http.MethodPost, "/api/wp/api/upload", &buf)
			req.Header.Set("Content-Type", w.FormDataContentType())
			return req
		}})
	}
	return shapes
}

// Spellings of a path inside a Crew that a client (or an attacker) can send for the same folder.
func gateSpellings(crew string) []string {
	id := strings.TrimPrefix(crew, "Crew/")
	return []string{
		crew,
		crew + "/",
		crew + "/db/db.sqlite",
		crew + "/builder/conversation/session-1.json",
		"/" + crew + "/product.json",
		"Crew//" + id + "/product.json",
		"./" + crew + "/product.json",
		"Chats/../" + crew + "/product.json",
		crew + "/../" + id + "/product.json",
		"Crew\\" + id + "\\product.json",
		"  " + crew + "/product.json",
		strings.ReplaceAll(crew, "/", "\\") + "/db",
		// The workspace service strips its document root off an absolute path.
		gateDocsRoot + "/" + crew + "/product.json",
		gateDocsRoot + "/" + crew,
		gateDocsRoot + "//" + crew + "/../" + id + "/db",
	}
}

// B (a reader), C (no Crew product) and an administrator who is not the owner are refused on every request shape
// and every spelling of a Crew they do not own.
func TestSharedCrewRootGateRefusesEveryNonOwnerOnEveryShape(t *testing.T) {
	setupSharedCrewGate(t)
	for _, user := range []string{gateReader, gateNoCrew, gateAdmin, gateOther} {
		for _, shape := range gateShapes() {
			for _, spelling := range gateSpellings(gateCrew) {
				if got := gateStatus(t, user, shape.build(spelling)); got != http.StatusForbidden {
					t.Errorf("%s: %s %q passed the gate (status %d), want 403", user, shape.name, spelling, got)
				}
			}
		}
	}
}

// The owner reaches their own Crew by every shape, and only their own.
func TestSharedCrewRootGateLetsTheOwnerThroughOnlyToTheirOwn(t *testing.T) {
	setupSharedCrewGate(t)
	for _, shape := range gateShapes() {
		if strings.Contains(shape.name, "target_dir") {
			continue
		}
		for _, spelling := range gateSpellings(gateCrew) {
			if got := gateStatus(t, gateOwner, shape.build(spelling)); got != 0 {
				t.Errorf("owner: %s %q refused (status %d)", shape.name, spelling, got)
			}
		}
		for _, spelling := range gateSpellings(gateOtherCrw) {
			if got := gateStatus(t, gateOwner, shape.build(spelling)); got != http.StatusForbidden {
				t.Errorf("owner reached another owner's Crew: %s %q (status %d)", shape.name, spelling, got)
			}
		}
	}
}

// The bare Crew root is never listable or writable, whoever asks; a Crew nobody owns (not created yet, or a
// manifest without owner_id) is refused, so a Crew cannot be created through the raw proxy.
func TestSharedCrewRootGateBareRootAndOwnerlessCrews(t *testing.T) {
	setupSharedCrewGate(t)
	for _, user := range []string{gateOwner, gateReader, gateAdmin} {
		for _, shape := range gateShapes() {
			for _, spelling := range []string{"Crew", "Crew/", "/Crew", "./Crew", "Crew/."} {
				if got := gateStatus(t, user, shape.build(spelling)); got != http.StatusForbidden {
					t.Errorf("%s: %s on bare root %q passed (status %d)", user, shape.name, spelling, got)
				}
			}
			for _, spelling := range []string{"Crew/not-created-1/product.json", "Crew/not-created-1"} {
				if got := gateStatus(t, user, shape.build(spelling)); got != http.StatusForbidden {
					t.Errorf("%s: %s created/read an ownerless Crew %q (status %d)", user, shape.name, spelling, got)
				}
			}
		}
	}
}

// A whole-workspace search or glob from a non-admin never runs (it would walk every Crew).
func TestSharedCrewRootGateWholeWorkspaceBulkRoutes(t *testing.T) {
	setupSharedCrewGate(t)
	for _, route := range []string{"api/search?query=x", "api/glob?pattern=**/*.md"} {
		for _, user := range []string{gateOwner, gateReader, gateNoCrew} {
			req := httptest.NewRequest(http.MethodGet, "/api/wp/"+route, nil)
			if got := gateStatus(t, user, req); got != http.StatusForbidden {
				t.Errorf("%s: whole-workspace %s passed (status %d)", user, route, got)
			}
		}
	}
}

// Parity: for every shape, the shared Crew is exactly as closed as the same Crew in its owner's tree (physical
// spelling), for the owner and for everyone else. A path field the gate forgets for Crew/ shows up here.
func TestSharedCrewRootGateMatchesTheOwnersTree(t *testing.T) {
	setupSharedCrewGate(t)
	legacy := "_users/" + gateOwner + "/Chats/Work/projects/sde-1a2b/db/db.sqlite"
	shared := gateCrew + "/db/db.sqlite"
	for _, user := range []string{gateOwner, gateReader, gateNoCrew, gateAdmin} {
		for _, shape := range gateShapes() {
			if strings.Contains(shape.name, "target_dir") {
				continue
			}
			l, s := gateStatus(t, user, shape.build(legacy)), gateStatus(t, user, shape.build(shared))
			if (l == 0) != (s == 0) {
				t.Errorf("%s: %s: owner's tree status %d but shared root status %d", user, shape.name, l, s)
			}
		}
	}
}

// The same absolute-path spelling used to bypass the other protected top-level folders (config/, _system/,
// Workflow/<id>): the gate classified the raw string, so "<docs root>/config/users.json" was not "config/".
func TestWorkspaceProxyAbsoluteSpellingsDoNotBypassProtectedFolders(t *testing.T) {
	setupSharedCrewGate(t)
	private, _ := json.Marshal(WorkflowManifest{ID: "private", Label: "Private", Access: &WorkflowAccess{Owners: []string{gateOwner}}})
	host := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/private"): string(private)}})
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	for _, p := range []string{"config/users.json", "_system/costs.jsonl", "Workflow/private/planning/plan.json", "_users/alice/Chats/x.md"} {
		for _, spelling := range []string{gateDocsRoot + "/" + p, "/app/workspace-docs/" + p, strings.ReplaceAll(gateDocsRoot+"/"+p, "/", "\\")} {
			req := gateRequest(http.MethodGet, "/api/wp/api/documents/"+spelling, nil)
			if got := gateStatus(t, gateReader, req); got != http.StatusForbidden {
				t.Errorf("reader reached %q through the spelling %q (status %d)", p, spelling, got)
			}
		}
	}
	// The caller's own files by an absolute spelling are still theirs.
	req := gateRequest(http.MethodGet, "/api/wp/api/documents/"+gateDocsRoot+"/_users/bob/Chats/x.md", nil)
	if got := gateStatus(t, gateReader, req); got != 0 {
		t.Errorf("own file by absolute spelling refused (status %d)", got)
	}
}
