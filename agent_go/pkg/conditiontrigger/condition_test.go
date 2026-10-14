package conditiontrigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type diskStore struct {
	path        string
	failReceipt bool
}

func (s *diskStore) Load(_ context.Context, _ string) (Ledger, error) {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return Ledger{}, nil
	}
	if err != nil {
		return Ledger{}, err
	}
	var l Ledger
	err = json.Unmarshal(b, &l)
	return l, err
}
func (s *diskStore) Save(_ context.Context, _ string, l Ledger) error {
	if s.failReceipt {
		for _, r := range l.Rows {
			if r.Status == "dispatched" {
				s.failReceipt = false
				return errors.New("process stopped after target accepted")
			}
		}
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}

// A real Python detector, persisted checkpoint, HTTP target and restarted engine
// prove duplicate polls and the ambiguous accepted-send boundary together.
func TestConditionPythonPollAndCrashRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	dir := t.TempDir()
	source := `import json,sys
state=json.load(sys.stdin)
print(json.dumps({"events":[{"id":"ticket-123","payload":{"ticket_id":"123"}}],"state":{"last_id":"123"}}))`
	script := filepath.Join(dir, "check.py")
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Script: "code/triggers/check.py", SHA256: Hash(source), Target: Target{Kind: "agent", Message: "Process this ticket"}}
	store := &diskStore{path: filepath.Join(dir, "ledger.json")}
	accepted := map[string]string{}
	effects := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			t.Error("missing stable dispatch key")
		}
		if _, ok := accepted[key]; !ok {
			accepted[key] = "receipt-123"
			effects++
		}
		_, _ = w.Write([]byte(accepted[key]))
	}))
	defer target.Close()
	run := func(ctx context.Context, _ Config, state json.RawMessage) ([]byte, error) {
		if len(state) == 0 {
			state = json.RawMessage(`{}`)
		}
		cmd := exec.CommandContext(ctx, "python3", script)
		cmd.Stdin = strings.NewReader(string(state))
		return cmd.Output()
	}
	dispatch := func(ctx context.Context, _ Config, ev Event, key string) (string, error) {
		if ev.Payload["ticket_id"] != "123" {
			t.Fatal(ev)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, nil)
		req.Header.Set("Idempotency-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		return "receipt-123", nil
	}
	e := &Engine{Store: store, Run: run, Dispatch: dispatch}
	if n, err := e.Poll(ctx, "owner/condition", cfg, now); err != nil || n != 1 {
		t.Fatalf("poll: %d %v", n, err)
	}
	store.failReceipt = true
	if err := e.Drain(ctx, "owner/condition", cfg, now); err == nil {
		t.Fatal("missing crash simulation")
	}
	e = &Engine{Store: store, Run: run, Dispatch: dispatch}
	if n, err := e.Poll(ctx, "owner/condition", cfg, now.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("duplicate poll: %d %v", n, err)
	}
	if err := e.Drain(ctx, "owner/condition", cfg, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	l, err := e.Read(ctx, "owner/condition")
	if err != nil || effects != 1 || len(l.Rows) != 1 || l.Rows[0].Status != "dispatched" || l.Rows[0].Receipt != "receipt-123" || !strings.Contains(string(l.State), `"123"`) {
		t.Fatalf("ledger=%+v effects=%d err=%v", l, effects, err)
	}
	// A new unmatched check changes the cursor without contacting a model/target.
	e.Run = func(context.Context, Config, json.RawMessage) ([]byte, error) {
		return []byte(`{"events":[],"state":{"last_id":"124"}}`), nil
	}
	if n, err := e.Poll(ctx, "owner/condition", cfg, now.Add(2*time.Minute)); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := e.Drain(ctx, "owner/condition", cfg, now); err != nil || effects != 1 {
		t.Fatal(effects, err)
	}
}

func TestConditionCheckpointAndRecoveryBoundaries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := &diskStore{path: filepath.Join(t.TempDir(), "state")}
	c := Config{Script: "code/triggers/check.py", SHA256: Hash("check"), Target: Target{Kind: "agent", Message: "Check"}}
	called := 0
	e := &Engine{Store: s, Run: func(context.Context, Config, json.RawMessage) ([]byte, error) {
		return []byte(`{"events":[{"id":"a","payload":{}}],"state":{}}`), nil
	}, Dispatch: func(context.Context, Config, Event, string) (string, error) { called++; return "receipt", nil }}
	if _, err := e.Poll(ctx, "key", c, now); err != nil {
		t.Fatal(err)
	}
	l, _ := e.Read(ctx, "key")
	l.Rows[0].Status = "dispatching"
	l.Rows[0].AttemptAt = now.Add(-RecoveryWindow)
	if err := s.Save(ctx, "key", l); err != nil {
		t.Fatal(err)
	}
	if err := e.Drain(ctx, "key", c, now); err != nil {
		t.Fatal(err)
	}
	l, _ = e.Read(ctx, "key")
	if called != 0 || l.Rows[0].Status != "needs_reconciliation" {
		t.Fatal(l, called)
	}
	for _, raw := range []string{`{"events":null,"state":{}}`, `{"events":[],"target":"other","state":{}}`, `{"events":[{"id":"a","payload":{}},{"id":"a","payload":{}}],"state":{}}`, `{"events":[],"state":[]}`, `{"events":[],"state":{}} {}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	// Invalid output cannot advance a cursor or enqueue an event.
	e.Run = func(context.Context, Config, json.RawMessage) ([]byte, error) {
		return []byte(`{"events":[],"state":"bad"}`), nil
	}
	before, _ := json.Marshal(l)
	if _, err := e.Poll(ctx, "key", c, now); err == nil {
		t.Fatal("bad checkpoint accepted")
	}
	after, _ := e.Read(ctx, "key")
	b, _ := json.Marshal(after)
	if string(b) != string(before) {
		t.Fatal("checkpoint changed on invalid output")
	}
}

func TestConditionFullBacklogStillDrainsAndRetentionStaysBounded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := &diskStore{path: filepath.Join(t.TempDir(), "ledger")}
	c := Config{Script: "code/triggers/check.py", SHA256: Hash("check"), Target: Target{Kind: "agent", Message: "Check"}}
	l := Ledger{State: json.RawMessage(`{}`)}
	for i := 0; i < MaxPending; i++ {
		l.Rows = append(l.Rows, Row{Event: Event{ID: fmt.Sprint(i), Payload: map[string]interface{}{}}, Revision: c.Revision(), Status: "seen", SeenAt: now})
	}
	if err := s.Save(ctx, "key", l); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Engine{Store: s, Run: func(context.Context, Config, json.RawMessage) ([]byte, error) {
		t.Fatal("backlog must defer Python")
		return nil, nil
	}, Dispatch: func(context.Context, Config, Event, string) (string, error) { calls++; return "receipt", nil }}
	if _, err := e.Poll(ctx, "key", c, now); !errors.Is(err, ErrBacklog) {
		t.Fatal(err)
	}
	if err := e.Drain(ctx, "key", c, now); err != nil {
		t.Fatal(err)
	}
	l, _ = e.Read(ctx, "key")
	if calls != MaxEvents || Pending(l) != MaxPending-MaxEvents {
		t.Fatal(calls, Pending(l))
	}
	// A busy detector doesn't stop accepting new work at the old 4096-row cap.
	l.Rows = nil
	for i := 0; i < MaxRows+20; i++ {
		l.Rows = append(l.Rows, Row{Event: Event{ID: fmt.Sprint(i), Payload: map[string]interface{}{}}, Status: "dispatched", SeenAt: now})
	}
	if err := s.Save(ctx, "key", l); err != nil {
		t.Fatal(err)
	}
	e.Run = func(context.Context, Config, json.RawMessage) ([]byte, error) {
		return []byte(`{"events":[],"state":{}}`), nil
	}
	if _, err := e.Poll(ctx, "key", c, now); err != nil {
		t.Fatal(err)
	}
	l, _ = e.Read(ctx, "key")
	if len(l.Rows) > 200 || len(l.Tombstones) > 8192 {
		t.Fatal(len(l.Rows), len(l.Tombstones))
	}
}
