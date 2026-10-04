package wsalias

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	oldLogical  = "Chats/Work/projects/sde-1a2b"
	oldPhysical = "_users/alice/Chats/Work/projects/sde-1a2b"
	moved       = "Crew/sde-1a2b"
)

// testResolver follows the alias of alice's crew; the empty user is "alice" here (a single-user layout).
func testResolver(user, p string) (string, bool) {
	if user == "" {
		user = "alice"
	}
	p = strings.Trim(p, "/")
	for _, old := range []string{oldPhysical, oldLogical} {
		if old == oldLogical && user != "alice" {
			continue
		}
		if p == old || strings.HasPrefix(p, old+"/") {
			return moved + strings.TrimPrefix(p, old), true
		}
	}
	return "", false
}

type capture struct {
	path, rawQuery, body string
	length               int64
}

func (c *capture) RoundTrip(r *http.Request) (*http.Response, error) {
	c.path, c.rawQuery, c.length = r.URL.Path, r.URL.RawQuery, r.ContentLength
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		c.body = string(data)
	}
	return httptest.NewRecorder().Result(), nil
}

func do(t *testing.T, resolver Resolver, user, method, target, contentType, body string) *capture {
	t.Helper()
	SetResolver(resolver)
	t.Cleanup(func() { SetResolver(nil) })
	got := &capture{}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/", reader)
	u, _ := url.Parse("http://workspace")
	req.URL = u
	// httptest.NewRequest escapes nothing; set path and query exactly.
	if i := strings.Index(target, "?"); i >= 0 {
		req.URL.Path, req.URL.RawQuery = target[:i], target[i+1:]
	} else {
		req.URL.Path = target
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if user != "" {
		req.Header.Set("X-User-ID", user)
	}
	if _, err := Transport(got).RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTransportRewritesRoutePaths(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"/api/documents/" + oldPhysical + "/code/x.py", "/api/documents/" + moved + "/code/x.py"},
		{"/api/documents/" + oldLogical + "/product.json", "/api/documents/" + moved + "/product.json"},
		{"/api/folders/" + oldLogical + "/files", "/api/folders/" + moved + "/files"},
		{"/api/versions/" + oldPhysical + "/db/db.sqlite", "/api/versions/" + moved + "/db/db.sqlite"},
		{"/api/restore/" + oldLogical + "/a.md", "/api/restore/" + moved + "/a.md"},
		{"/api/documents/" + moved + "/a.md", "/api/documents/" + moved + "/a.md"},
		{"/api/documents/Workflow/x/plan.json", "/api/documents/Workflow/x/plan.json"},
		{"/api/health", "/api/health"},
	} {
		if got := do(t, testResolver, "alice", http.MethodGet, tc.target, "", ""); got.path != tc.want {
			t.Errorf("%s -> %s, want %s", tc.target, got.path, tc.want)
		}
	}
	// Another user's short spelling is that user's own tree: untouched.
	if got := do(t, testResolver, "bob", http.MethodGet, "/api/documents/"+oldLogical+"/a.md", "", ""); got.path != "/api/documents/"+oldLogical+"/a.md" {
		t.Errorf("bob's own-tree path was rewritten: %s", got.path)
	}
	// ...but bob's physical spelling of alice's old path follows the alias.
	if got := do(t, testResolver, "bob", http.MethodGet, "/api/documents/"+oldPhysical+"/a.md", "", ""); got.path != "/api/documents/"+moved+"/a.md" {
		t.Errorf("a physical old path was not followed: %s", got.path)
	}
}

func TestTransportRewritesQueryParameters(t *testing.T) {
	got := do(t, testResolver, "alice", http.MethodGet, "/api/documents?folder="+url.QueryEscape(oldLogical)+"&max_depth=3&db_path="+url.QueryEscape(oldPhysical+"/db/db.sqlite")+"&other="+url.QueryEscape(oldLogical), "", "")
	q, _ := url.ParseQuery(got.rawQuery)
	if q.Get("folder") != moved || q.Get("db_path") != moved+"/db/db.sqlite" || q.Get("max_depth") != "3" {
		t.Errorf("query = %s", got.rawQuery)
	}
	if q.Get("other") != oldLogical {
		t.Errorf("a non-path parameter was rewritten: %s", got.rawQuery)
	}
}

func TestTransportRewritesJSONBodyPathFields(t *testing.T) {
	body := `{"folder_path":"` + oldLogical + `/new","count":12345678901234567890,"nested":{"db_path":"` + oldPhysical + `/db/db.sqlite"},"folder_guard":{"read_paths":["` + oldLogical + `","Chats/mine"],"write_paths":["` + oldPhysical + `"]},"content":"` + oldLogical + ` is mentioned in text"}`
	got := do(t, testResolver, "alice", http.MethodPost, "/api/folders", "application/json", body)
	for _, want := range []string{`"folder_path":"` + moved + `/new"`, `"db_path":"` + moved + `/db/db.sqlite"`, `"read_paths":["` + moved + `","Chats/mine"]`, `"write_paths":["` + moved + `"]`, `12345678901234567890`, oldLogical + ` is mentioned in text`} {
		if !strings.Contains(got.body, want) {
			t.Errorf("body lacks %s:\n%s", want, got.body)
		}
	}
	if got.length != int64(len(got.body)) {
		t.Errorf("content length %d, body %d", got.length, len(got.body))
	}
	// A body with nothing to rewrite is replayed byte for byte.
	plain := `{"folder_path":"Chats/mine", "x": 1}`
	if got := do(t, testResolver, "alice", http.MethodPost, "/api/folders", "application/json", plain); got.body != plain {
		t.Errorf("an untouched body changed: %s", got.body)
	}
	// Not JSON, or a content type that is not: untouched.
	if got := do(t, testResolver, "alice", http.MethodPost, "/api/upload", "multipart/form-data; boundary=x", "--x\r\nfolder_path="+oldLogical); !strings.Contains(got.body, oldLogical) {
		t.Errorf("a multipart body was touched: %s", got.body)
	}
}

func TestTransportIsAPassThroughWithoutAResolver(t *testing.T) {
	got := do(t, nil, "alice", http.MethodGet, "/api/documents/"+oldLogical+"/a.md?folder="+url.QueryEscape(oldLogical), "", "")
	if got.path != "/api/documents/"+oldLogical+"/a.md" || !strings.Contains(got.rawQuery, "Chats") {
		t.Errorf("rewrote with no resolver: %s ?%s", got.path, got.rawQuery)
	}
}
