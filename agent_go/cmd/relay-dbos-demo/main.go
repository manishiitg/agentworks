// relay-dbos-demo is a loopback-only browser harness for the DBOS prototype.
// Models and remote services are fixtures; Python, DBOS and the Go bridge are real.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

//go:embed index.html
var page string

//go:embed relay.py
var source string

type event struct {
	At      int64  `json:"at"`
	Message string `json:"message"`
}

type session struct {
	id, scenario, status, folder, failure string
	attempt                               int
	allowed                               bool
	calls                                 map[string]int
	events                                []event
}

type demo struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	root   string
	python string
	run    *session
}

func (d *demo) note(s *session, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s.events = append(s.events, event{time.Now().UnixMilli(), message})
}

func (d *demo) launch(s *session, recoverRun bool) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(d.ctx, 45*time.Second)
		defer cancel()
		fault := ""
		if !recoverRun {
			switch s.scenario {
			case "checkpoint":
				fault = "after"
			case "idempotent", "uncertain":
				fault = "during"
			}
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
		cfg := relaypython.Config{
			Client: diskWorkspace{d.root}, SourcePath: filepath.Join(s.folder, "relay.py"),
			RunPath:   filepath.Join(s.folder, "runs", "run-1"),
			Input:     map[string]interface{}{"order_id": "order-8842", "service_replay_safe": s.scenario != "uncertain"},
			Variables: map[string]interface{}{"release": "v1"},
			Env:       map[string]string{"DEMO_FAULT": fault}, Timeout: 40,
			DBOSPrototype: &relaypython.DBOSPrototype{RunID: s.id, ReleaseHash: hash, PythonExecutable: d.python,
				Authorize: func(context.Context) error {
					d.mu.Lock()
					allowed := s.allowed
					d.mu.Unlock()
					if !allowed {
						return errors.New("demo invocation permission revoked")
					}
					// The fixture release contains exactly one source file.
					content, err := os.ReadFile(filepath.Join(d.root, s.folder, "relay.py"))
					if err != nil || fmt.Sprintf("%x", sha256.Sum256(content)) != hash {
						return errors.New("demo release checksum changed")
					}
					return nil
				}},
			CallAgent: func(ctx context.Context, call relaypython.Call, tool relaypython.ToolCaller) (interface{}, error) {
				name := call.Name
				if call.Kind == "mcp" {
					name = "demo:fetch"
				}
				d.mu.Lock()
				s.calls[name]++
				d.mu.Unlock()
				d.note(s, name+" executed through the Go bridge")
				timer := time.NewTimer(1100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timer.C:
				}
				switch name {
				case "extract":
					value, err := tool(ctx, "lookup", map[string]interface{}{"order_id": "order-8842"})
					if err != nil {
						return nil, err
					}
					var order interface{}
					if err := json.Unmarshal([]byte(value), &order); err != nil {
						return nil, err
					}
					return relaypython.AgentResult{Output: order, Provider: "demo-fixture", Model: "deterministic"}, nil
				case "review":
					return map[string]interface{}{"approved": true, "order_id": "order-8842"}, nil
				case "demo:fetch":
					return map[string]interface{}{"delivery": "ready", "source": "MCP fixture"}, nil
				}
				return nil, errors.New("unknown demo call")
			},
		}
		err := relaypython.Run(ctx, cfg)
		d.mu.Lock()
		defer d.mu.Unlock()
		s.status, s.failure = "completed", ""
		message := "Run completed. Inspect the result and execution counts."
		if err != nil {
			s.failure, s.status = err.Error(), "error"
			message = "Run failed; inspect error details."
			if strings.Contains(err.Error(), "requires reconciliation") {
				s.status, message = "blocked", "Recovery stopped: the uncertain action was not repeated."
			} else if strings.Contains(err.Error(), "permission revoked") {
				s.status, message = "denied", "Recovery refused because invocation permission was revoked."
			} else if fault != "" {
				if _, exists := os.Stat(filepath.Join(d.root, cfg.RunPath, "fault.json")); exists == nil {
					s.status, message = "interrupted", "Python process exited. Click Recover same run to relaunch it."
				}
			}
		}
		s.events = append(s.events, event{time.Now().UnixMilli(), message})
	}()
}

func (d *demo) start(scenario string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.run != nil && d.run.status == "running" {
		return errors.New("a demo run is already active")
	}
	s := &session{id: uuid.NewString(), scenario: scenario, status: "running", attempt: 1,
		allowed: true, calls: map[string]int{}, events: []event{}}
	s.folder = "run-" + s.id
	if _, err := (diskWorkspace{d.root}).UpdateWorkspaceFile(d.ctx, workspace.UpdateWorkspaceFileParams{Filepath: filepath.Join(s.folder, "relay.py"), Content: source}); err != nil {
		return err
	}
	s.events = append(s.events, event{time.Now().UnixMilli(), "Started a new v1 invocation with real DBOS checkpoints."})
	d.run = s
	d.launch(s, false)
	return nil
}

func (d *demo) recoverRun() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.run
	if s == nil || (s.status != "interrupted" && s.status != "denied") {
		return errors.New("only an interrupted or admission-denied run can recover")
	}
	s.status, s.failure = "running", ""
	s.attempt++
	s.events = append(s.events, event{time.Now().UnixMilli(), "Recovering the same run ID, input and frozen release."})
	d.launch(s, true)
	return nil
}

func readJSON(file string) interface{} {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var value interface{}
	if json.Unmarshal(content, &value) != nil {
		return nil
	}
	return value
}

func (d *demo) snapshot() map[string]interface{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := map[string]interface{}{"status": "idle", "events": []event{}, "calls": map[string]int{}}
	if s := d.run; s != nil {
		runPath := filepath.Join(d.root, s.folder, "runs", "run-1")
		attempts, _ := os.ReadFile(filepath.Join(runPath, "service-attempts.txt"))
		result = map[string]interface{}{"run_id": s.id, "scenario": s.scenario, "status": s.status,
			"attempt": s.attempt, "access_allowed": s.allowed, "error": s.failure,
			"calls": s.calls, "events": s.events, "trace": readJSON(filepath.Join(runPath, "relay_trace.json")),
			"result": readJSON(filepath.Join(runPath, "relay_result.json")),
			"effect": readJSON(filepath.Join(runPath, "service.json")), "service_attempts": strings.Count(string(attempts), "attempt\n")}
		// Copy maps/slices before releasing the lock to the JSON encoder.
		encoded, _ := json.Marshal(result)
		_ = json.Unmarshal(encoded, &result)
	}
	return result
}

func (d *demo) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d.snapshot())
	})
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Scenario string `json:"scenario"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "expected one scenario object", http.StatusBadRequest)
			return
		}
		switch body.Scenario {
		case "normal", "checkpoint", "idempotent", "uncertain":
		default:
			http.Error(w, "unknown scenario", http.StatusBadRequest)
			return
		}
		if err := d.start(body.Scenario); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/recover", func(w http.ResponseWriter, r *http.Request) {
		if err := d.recoverRun(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/access", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.run == nil || d.run.status == "running" {
			http.Error(w, "pause at a crash before changing demo access", http.StatusConflict)
			return
		}
		d.run.allowed = !d.run.allowed
		message := "Invocation permission restored."
		if !d.run.allowed {
			message = "Invocation permission revoked. Try recovering this run."
		}
		d.run.events = append(d.run.events, event{time.Now().UnixMilli(), message})
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Reject cross-site browser requests to the unauthenticated local harness.
		if r.Method != http.MethodGet && (r.Header.Get("X-Relay-Demo") != "1" ||
			(r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host)) {
			http.Error(w, "same-origin demo request required", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type diskWorkspace struct{ root string }

func (d diskWorkspace) file(name string) (string, error) {
	if !filepath.IsLocal(name) {
		return "", errors.New("demo path must be relative")
	}
	return filepath.Join(d.root, name), nil
}

func (d diskWorkspace) ReadWorkspaceFile(_ context.Context, p workspace.ReadWorkspaceFileParams) (workspace.ReadFileResult, error) {
	file, err := d.file(p.Filepath)
	if err != nil {
		return workspace.ReadFileResult{}, err
	}
	content, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return workspace.ReadFileResult{}, errors.New("file not found")
	}
	return workspace.ReadFileResult{Content: string(content)}, err
}

func (d diskWorkspace) UpdateWorkspaceFile(_ context.Context, p workspace.UpdateWorkspaceFileParams) (workspace.UpdateFileResult, error) {
	file, err := d.file(p.Filepath)
	if err != nil {
		return workspace.UpdateFileResult{}, err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return workspace.UpdateFileResult{}, err
	}
	if err = os.WriteFile(file+".write", []byte(p.Content), 0600); err == nil {
		err = os.Rename(file+".write", file)
	}
	return workspace.UpdateFileResult{Success: err == nil}, err
}

func (d diskWorkspace) ExecuteShellCommand(ctx context.Context, p workspace.ExecuteShellCommandParams) (workspace.ShellCommandResult, error) {
	dir, err := d.file(p.WorkingDirectory)
	if err != nil {
		return workspace.ShellCommandResult{}, err
	}
	// exec replaces the shell so context cancellation kills this Python runner.
	cmd := exec.CommandContext(ctx, "sh", "-c", "exec "+p.Command)
	cmd.Dir, cmd.Env = dir, os.Environ()
	for key, value := range p.ExtraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		code = -1
	}
	return workspace.ShellCommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, nil
}

func main() {
	port := flag.Int("port", 18769, "loopback HTTP port")
	python := flag.String("python", os.Getenv("RELAY_DBOS_PYTHON"), "Python interpreter with requirements-dbos.txt installed")
	flag.Parse()
	if *python == "" {
		log.Fatal("set RELAY_DBOS_PYTHON or pass -python")
	}
	if out, err := exec.Command(*python, "-I", "-c", "from importlib.metadata import version; assert version('dbos') == '3.2.0'").CombinedOutput(); err != nil {
		log.Fatalf("DBOS interpreter unavailable: %v: %s", err, out)
	}
	root, err := os.MkdirTemp("", "relays-dbos-browser-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	d := &demo{ctx: ctx, root: root, python: *python}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Printf("Relays DBOS browser demo: http://%s\n", listener.Addr())
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
	cancel()
	d.wg.Wait()
}
