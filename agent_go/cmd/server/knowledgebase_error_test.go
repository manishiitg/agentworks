package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"golang.org/x/sys/unix"
)

type brainResponseProbe struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *brainResponseProbe) Header() http.Header    { return p.header }
func (p *brainResponseProbe) WriteHeader(status int) { p.status = status }
func (p *brainResponseProbe) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return p.body.Write(b)
}

func TestBrainCancellationAndUnavailableDiagnostics(t *testing.T) {
	api, service := knowledgebaseServerTest(t)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/knowledgebase/folders?folder_path=Shared", nil)
		r.Header.Set("X-Request-ID", "brain-cancellation-proof")
		return r.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "admin", Username: "admin"}))
	}

	// Reproduce the real route waiting on Brain's content lock when its caller
	// goes away. A cancelled request writes no response and no service failure.
	lock, err := os.OpenFile(filepath.Join(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_ROOT"), "private", "content.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := &brainResponseProbe{header: http.Header{}}
	api.handleKnowledgebaseViewer(w, request(ctx))
	_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if w.status != 0 || w.body.Len() != 0 || strings.Contains(logs.String(), "[KB_ERROR]") {
		t.Fatalf("caller cancellation reported a service failure: status=%d body_bytes=%d logs=%s", w.status, w.body.Len(), logs.String())
	}
	t.Log("real Brain folder route + held content lock: caller cancellation returned without response/KB_ERROR")

	// Error text deliberately contains private-looking material. Only fixed
	// classes and the request ID may reach diagnostics or the generic 503 reply.
	const private = "PRIVATE_ERROR_CANARY"
	deadlineCtx, deadlineCancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	for _, tc := range []struct {
		name, class string
		err         error
		ctx         context.Context
	}{
		{"live caller internal cancellation", "canceled", fmt.Errorf(private+": %w", context.Canceled), t.Context()},
		{"caller deadline", "deadline_exceeded", fmt.Errorf(private+": %w", context.DeadlineExceeded), deadlineCtx},
		{"cancelled caller real failure", "internal", errors.New(private), ctx},
		{"permission failure", "permission_denied", &os.PathError{Op: "open", Path: private, Err: os.ErrPermission}, t.Context()},
		{"bad index JSON", "invalid_json", &json.SyntaxError{}, t.Context()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			w := &brainResponseProbe{header: http.Header{}}
			knowledgebaseHTTPError(w, request(tc.ctx), "index_read", tc.err)
			if w.status != 503 || !strings.Contains(logs.String(), `request_id="brain-cancellation-proof" stage=index_read class=`+tc.class) {
				t.Fatalf("missing classified failure: status=%d logs=%s", w.status, logs.String())
			}
			if strings.Contains(logs.String()+w.body.String(), private) {
				t.Fatal("raw error contents escaped")
			}
		})
	}
	logs.Reset()
	domain := &knowledgebase.Error{Code: "FORBIDDEN", Message: "Existing folder access is required."}
	w = &brainResponseProbe{header: http.Header{}}
	knowledgebaseHTTPError(w, request(t.Context()), "access_check", domain)
	if w.status != 403 || logs.Len() != 0 {
		t.Fatal("domain access refusal changed", w.status, logs.String())
	}
	t.Log("live caller cancellation/deadline/real failure remain503; domain403 unchanged; diagnostic canary absent")

	// Real store/index failures retain their source stage through the HTTP boundary.
	blocked := filepath.Join(t.TempDir(), private)
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, storeErr := knowledgebase.New(knowledgebase.Config{Root: blocked, OrganizationID: "test"})
	if err := os.WriteFile(filepath.Join(os.Getenv("AGENTWORKS_KNOWLEDGEBASE_ROOT"), "live", ".kb-registry.json"), []byte("not JSON "+private), 0600); err != nil {
		t.Fatal(err)
	}
	_, indexErr := service.Call(t.Context(), knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}, "list_knowledgebase_folders", map[string]any{"folder_path": ""})
	for _, failure := range []struct {
		stage string
		err   error
	}{{"store_open", storeErr}, {"index_read", indexErr}} {
		if failure.err == nil || knowledgebase.DiagnosticStep(failure.err) != failure.stage {
			t.Fatalf("missing native stage %s: %T", failure.stage, failure.err)
		}
		logs.Reset()
		w := &brainResponseProbe{header: http.Header{}}
		knowledgebaseHTTPError(w, request(t.Context()), "operation", failure.err)
		if w.status != 503 || !strings.Contains(logs.String(), "stage="+failure.stage) || strings.Contains(logs.String()+w.body.String(), private) {
			t.Fatalf("native stage missing or private data exposed: stage=%s status=%d", failure.stage, w.status)
		}
	}
	t.Log("real blocked store open and malformed index retain source stages; raw paths/content absent")
}
