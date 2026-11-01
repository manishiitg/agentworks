package voicestt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Status is everything a UI needs to describe the engine on this machine.
// Every voice consumer uses this shape, so no product carries its own
// notion of "installed" or "warm".
type Status struct {
	// Available is a build-time fact: false in a CGO_ENABLED=0 binary, where
	// nothing below can ever become true.
	Available bool `json:"available"`
	// Installed means every model file is on disk. Downloading is the
	// in-flight state before that, with progress in bytes.
	Installed   bool  `json:"installed"`
	Downloading bool  `json:"downloading"`
	GotBytes    int64 `json:"got_bytes"`
	TotalBytes  int64 `json:"total_bytes"`
	// Loading is the ~1-2s window while the recognizer initializes; Ready
	// means a mic session would start instantly.
	Loading bool `json:"loading"`
	Ready   bool `json:"ready"`
	// ActiveStreams counts live dictation sessions right now.
	ActiveStreams int `json:"active_streams"`
	// Error is the last install/load failure, cleared by the next attempt.
	Error string `json:"error,omitempty"`
	// ModelDir is where the files live, for diagnostics.
	ModelDir string `json:"model_dir"`
	SizeMB   int    `json:"size_mb"`
}

// Manager owns the one process-wide Engine and its model files: download
// (with progress), lazy load, warm, unload, remove, plus the counters a UI
// reads. Every server embeds exactly one Manager; sessions borrow its Engine.
//
// Loading is serialized: concurrent callers of Engine() wait on the same
// in-flight load rather than each starting one. A failed load is remembered
// until the next Warm/Engine call, which retries — a transient download error
// must not wedge the feature for the rest of the process's life.
type Manager struct {
	modelDir string

	mu       sync.Mutex
	engine   *Engine
	loading  bool
	loadDone chan struct{}
	lastErr  error
	dl       DownloadProgress
	dlActive bool

	active atomic.Int32
}

// NewManager prepares a Manager for modelDir. Nothing is downloaded or loaded
// until Warm or Engine is called.
func NewManager(modelDir string) *Manager {
	return &Manager{modelDir: modelDir}
}

// ModelDir is where this Manager keeps its model files.
func (m *Manager) ModelDir() string { return m.modelDir }

// Status is a non-blocking snapshot; safe to call from a status endpoint at
// any rate.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Available:     Available,
		Installed:     ModelInstalled(m.modelDir),
		Downloading:   m.dlActive,
		GotBytes:      m.dl.Got,
		TotalBytes:    m.dl.Total,
		Loading:       m.loading && !m.dlActive,
		Ready:         m.engine != nil,
		ActiveStreams: int(m.active.Load()),
		ModelDir:      m.modelDir,
		SizeMB:        ModelSizeMB,
	}
	if m.lastErr != nil {
		st.Error = m.lastErr.Error()
	}
	return st
}

// Ready reports whether a recognizer is loaded right now.
func (m *Manager) Ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engine != nil
}

// Engine returns the loaded recognizer, downloading and loading first if
// needed. Blocks for the download on a first run (~630MB) and ~1-2s for the
// load; ctx bounds the wait.
func (m *Manager) Engine(ctx context.Context) (*Engine, error) {
	if !Available {
		return nil, errors.New("voicestt requires a CGO-enabled build with the sherpa-onnx native runtime")
	}
	m.mu.Lock()
	if m.engine != nil {
		e := m.engine
		m.mu.Unlock()
		return e, nil
	}
	m.startLoadLocked()
	done := m.loadDone
	m.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		if m.lastErr != nil {
			return nil, m.lastErr
		}
		return nil, errors.New("voicestt: engine not loaded")
	}
	return m.engine, nil
}

// startLoadLocked begins a load attempt if none is in flight. Called with
// m.mu held. It flips the loading (and, when files are missing, downloading)
// flags BEFORE returning, so a Status() taken right after Warm() already
// reports the work as started — a UI that polled and saw "idle" for the
// first few hundred milliseconds dropped its progress banner (caught live).
func (m *Manager) startLoadLocked() {
	if m.loading {
		return
	}
	m.loading = true
	m.lastErr = nil
	m.loadDone = make(chan struct{})
	if !ModelInstalled(m.modelDir) {
		m.dlActive = true
		m.dl = DownloadProgress{}
	}
	go m.load()
}

// load runs once per attempt, off the caller's goroutine.
func (m *Manager) load() {
	started := time.Now()
	var err error
	defer func() {
		m.mu.Lock()
		m.loading = false
		m.dlActive = false
		m.lastErr = err
		close(m.loadDone)
		m.mu.Unlock()
	}()

	if !ModelInstalled(m.modelDir) {
		log.Printf("[VOICE] downloading speech model (~%dMB) into %s", ModelSizeMB, m.modelDir)
		err = EnsureModelFiles(m.modelDir, func(p DownloadProgress) {
			m.mu.Lock()
			m.dl = p
			m.mu.Unlock()
		})
		m.mu.Lock()
		m.dlActive = false
		m.mu.Unlock()
		if err != nil {
			log.Printf("[VOICE] model download failed: %v", err)
			return
		}
		log.Printf("[VOICE] speech model downloaded in %s", time.Since(started).Round(time.Second))
	}
	var e *Engine
	e, err = NewEngine(m.modelDir)
	if err != nil {
		log.Printf("[VOICE] engine load failed: %v", err)
		return
	}
	m.mu.Lock()
	m.engine = e
	m.mu.Unlock()
	log.Printf("[VOICE] engine ready in %s, model dir=%s", time.Since(started).Round(time.Millisecond), m.modelDir)
}

// Warm starts loading in the background so the first mic click of a session
// does not pay for it. Returns immediately; no-op when unavailable, already
// ready, or already loading.
func (m *Manager) Warm() {
	if !Available {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		return
	}
	m.startLoadLocked()
}

// Unload releases the recognizer's memory. Refused while a dictation is in
// progress, since that would discard the utterance being spoken. Returns
// whether it unloaded.
func (m *Manager) Unload() bool {
	if m.active.Load() > 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active.Load() > 0 || m.loading {
		return false
	}
	if m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	return true
}

// Remove unloads and deletes the model files. Refused mid-dictation.
func (m *Manager) Remove() error {
	if !m.Unload() {
		return errors.New("a dictation session is in progress")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loading {
		return errors.New("the speech model is still being set up")
	}
	return RemoveModelFiles(m.modelDir)
}

// Session is one caller's live dictation against the shared Engine, counted
// in Status.ActiveStreams so Unload/Remove cannot pull the engine out from
// under it.
type Session struct {
	m      *Manager
	engine *Engine
	stream *Stream
	closed bool
}

// NewSession opens a streaming session, loading the engine first if needed.
func (m *Manager) NewSession(ctx context.Context) (*Session, error) {
	e, err := m.Engine(ctx)
	if err != nil {
		return nil, err
	}
	m.active.Add(1)
	return &Session{m: m, engine: e, stream: e.NewStream()}, nil
}

// Accept feeds one chunk of 16kHz mono float32 PCM; see Stream.AcceptWaveform.
// Partials come back punctuated too, so the live preview reads like the
// committed text will.
func (s *Session) Accept(samples []float32) Result {
	r := s.stream.AcceptWaveform(samples)
	r.Text = s.engine.Punctuate(r.Text)
	return r
}

// Finish flushes buffered audio and returns the final, punctuated transcript.
func (s *Session) Finish() Result {
	r := s.stream.Finish()
	r.Text = s.engine.Punctuate(r.Text)
	return r
}

// Close releases the session. Idempotent.
func (s *Session) Close() {
	if s.closed {
		return
	}
	s.closed = true
	s.stream.Close()
	s.m.active.Add(-1)
}

// Transcribe decodes one complete clip of 16kHz mono float32 PCM in a batch —
// what a WhatsApp voice note or an uploaded recording needs. It feeds the
// streaming recognizer the whole clip and flushes, which is how the engine's
// own live test decodes files.
func (m *Manager) Transcribe(ctx context.Context, samples []float32) (string, error) {
	if len(samples) == 0 {
		return "", nil
	}
	s, err := m.NewSession(ctx)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var text string
	// Feed in one-second pieces so an endpoint mid-clip (a long pause in a
	// voice note) commits that phrase instead of it being reset away.
	step := SampleRate
	for i := 0; i < len(samples); i += step {
		end := i + step
		if end > len(samples) {
			end = len(samples)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		r := s.Accept(samples[i:end])
		if r.EndOfUtterance && r.Text != "" {
			text = joinPhrases(text, r.Text)
		}
	}
	final := s.Finish()
	return joinPhrases(text, final.Text), nil
}

func joinPhrases(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return fmt.Sprintf("%s %s", a, b)
	}
}
