package knowledgebase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/brainapi"
)

func TestBrainBackendSelectionFailsClosed(t *testing.T) {
	cfg := Config{Root: filepath.Join(t.TempDir(), "state"), OrganizationID: "org"}
	for _, mode := range []string{"private", "missing"} {
		if _, err := newService(cfg, mode, nil); err == nil {
			t.Fatalf("%s unexpectedly opened", mode)
		}
		if _, err := os.Stat(cfg.Root); !os.IsNotExist(err) {
			t.Fatalf("rejected factory created state: %v", err)
		}
	}
	for _, factory := range []*brainapi.Factory{
		{Version: brainapi.Version + 1, Open: func(Config) (brainapi.Engine, error) { t.Fatal("opened incompatible factory"); return nil, nil }},
		{Version: brainapi.Version, Catalog: nil, Open: func(Config) (brainapi.Engine, error) { t.Fatal("opened wrong catalog"); return nil, nil }},
	} {
		if _, err := newService(cfg, "private", factory); err == nil {
			t.Fatal("accepted incompatible factory")
		}
	}
	service, err := newService(cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.BackendName() != "builtin" {
		t.Fatal(service.BackendName())
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrainBackendLeaseSwitchPreservesState(t *testing.T) {
	cfg := Config{Root: t.TempDir(), OrganizationID: "org"}
	first, err := newService(cfg, "builtin", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	admin := Principal{IdentityID: "admin", IsAdmin: true}
	if err := first.SyncPlatformIdentities(t.Context(), []Identity{{ID: "admin", Type: "user"}}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"folder_path": "", "filename": "keep.md", "type": "note", "title": "Keep", "content": "original", "request_id": "keep"}
	forged := admin
	forged.RequestTool = "forged_namespace"
	forged.RequestArguments = map[string]any{"request_id": "other"}
	value, err := first.Call(t.Context(), forged, "create_knowledgebase", args)
	if err != nil {
		t.Fatal(err)
	}
	entry := asMap(value)
	factory := &brainapi.Factory{Version: brainapi.Version, Catalog: ToolDefinitions(), Open: func(c Config) (brainapi.Engine, error) { return newBuiltin(c) }}
	if _, err := newService(cfg, "private", factory); err == nil {
		t.Fatal("second writer opened")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := newService(cfg, "private", factory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	duplicate, err := second.Call(t.Context(), admin, "create_knowledgebase", args)
	if err != nil {
		t.Fatal(err)
	}
	if asMap(duplicate)["entry_id"] != entry["entry_id"] {
		t.Fatal("switch changed durable idempotency")
	}
	if _, err := first.Call(t.Context(), admin, "list_knowledgebase", nil); err == nil {
		t.Fatal("closed engine admitted operation")
	}
	// An explicit empty cap list still grants nothing after crossing the interface.
	empty := []Cap{}
	if _, err := second.Call(t.Context(), Principal{IdentityID: "admin", IsAdmin: true, Caps: &empty}, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]}); err == nil {
		t.Fatal("empty caps expanded access")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	rollback, err := newService(cfg, "builtin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Close()
	read, err := rollback.Call(t.Context(), admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})
	if err != nil {
		t.Fatal(err)
	}
	if asMap(read)["content"] != "original" {
		t.Fatalf("rollback lost content: %v", read)
	}
}

type drainingEngine struct {
	brainapi.Engine
	entered, release, finished chan struct{}
}

func (e *drainingEngine) CallTool(context.Context, Principal, string, map[string]any) (any, error) {
	close(e.entered)
	<-e.release
	return "done", nil
}
func (e *drainingEngine) Close() error { close(e.finished); return e.Engine.Close() }

func TestBrainBackendCloseDrainsAndRejectsNewCalls(t *testing.T) {
	cfg := Config{Root: t.TempDir(), OrganizationID: "org"}
	engine := &drainingEngine{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	factory := &brainapi.Factory{Version: brainapi.Version, Catalog: ToolDefinitions(), Open: func(c Config) (brainapi.Engine, error) {
		var err error
		engine.Engine, err = newBuiltin(c)
		return engine, err
	}}
	service, err := newService(cfg, "private", factory)
	if err != nil {
		t.Fatal(err)
	}
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = service.CallTool(context.Background(), Principal{}, "ignored", nil)
	}()
	<-engine.entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	deadline := time.After(3 * time.Second)
	for {
		service.mu.Lock()
		closing := service.closing
		service.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("close did not begin")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := service.Call(context.Background(), Principal{}, "ignored", nil); err == nil {
		t.Fatal("new call entered during drain")
	}
	select {
	case <-engine.finished:
		t.Fatal("closed engine before call drained")
	default:
	}
	close(engine.release)
	<-callDone
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newService(cfg, "builtin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func TestBrainBackendFailedOpenReleasesLeaseAndRejectsSymlink(t *testing.T) {
	cfg := Config{Root: t.TempDir()}
	want := errors.New("open failed")
	factory := &brainapi.Factory{Version: brainapi.Version, Catalog: ToolDefinitions(), Open: func(Config) (brainapi.Engine, error) { return nil, want }}
	if _, err := newService(cfg, "private", factory); !errors.Is(err, want) {
		t.Fatal(err)
	}
	service, err := newService(cfg, "builtin", nil)
	if err != nil {
		t.Fatal(err)
	}
	service.Close()
	path := filepath.Join(cfg.Root, "private", "engine.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := newService(cfg, "builtin", nil); err == nil {
		t.Fatal("followed lease symlink")
	}
	data, err := os.ReadFile(outside)
	if err != nil || strings.TrimSpace(string(data)) != "untouched" {
		t.Fatal("modified symlink target")
	}
}

func TestBrainBackendDrainTimeoutRetainsWriterLease(t *testing.T) {
	cfg := Config{Root: t.TempDir()}
	engine := &drainingEngine{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	factory := &brainapi.Factory{Version: brainapi.Version, Catalog: ToolDefinitions(), Open: func(c Config) (brainapi.Engine, error) {
		var err error
		engine.Engine, err = newBuiltin(c)
		return engine, err
	}}
	service, err := newService(cfg, "private", factory)
	if err != nil {
		t.Fatal(err)
	}
	service.closeTimeout = 10 * time.Millisecond
	go func() { _, _ = service.CallTool(context.Background(), Principal{}, "ignored", nil) }()
	<-engine.entered
	if err := service.Close(); err == nil {
		t.Fatal("hung operation reported a completed drain")
	}
	select {
	case <-engine.finished:
		t.Fatal("closed state while work remained")
	default:
	}
	if _, err := newService(cfg, "builtin", nil); err == nil {
		t.Fatal("released writer lease after timed-out drain")
	}
	close(engine.release)
	select {
	case <-service.done:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not finish")
	}
	reopened, err := newService(cfg, "builtin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

type cancelledEngine struct {
	brainapi.Engine
	entered chan struct{}
}

func (e *cancelledEngine) CallTool(ctx context.Context, _ Principal, _ string, _ map[string]any) (any, error) {
	close(e.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestBrainBackendCloseCancelsInFlightContext(t *testing.T) {
	engine := &cancelledEngine{entered: make(chan struct{})}
	factory := &brainapi.Factory{Version: brainapi.Version, Catalog: ToolDefinitions(), Open: func(c Config) (brainapi.Engine, error) {
		var err error
		engine.Engine, err = newBuiltin(c)
		return engine, err
	}}
	service, err := newService(Config{Root: t.TempDir()}, "private", factory)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := service.CallTool(context.Background(), Principal{}, "ignored", nil); result <- err }()
	<-engine.entered
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
