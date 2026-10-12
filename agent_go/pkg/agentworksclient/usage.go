package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
)

type UsageSettings struct {
	Enabled    bool      `json:"enabled"`
	Generation string    `json:"generation"`
	ChangedAt  time.Time `json:"changed_at"`
}

type UsageStatus struct {
	UsageSettings
	Sessions     int       `json:"tracked_sessions"`
	PendingClear int       `json:"pending_remote_clears"`
	LastSample   time.Time `json:"last_sample,omitempty"`
	Caps         []string  `json:"caps,omitempty"`
}

type usageBinding struct {
	ConfigPath string              `json:"config_path"`
	Last       agentusage.Reading  `json:"last"`
	Pending    *agentusage.Reading `json:"pending,omitempty"`
	Available  bool                `json:"available"`
	Reason     string              `json:"reason,omitempty"`
	Next       time.Time           `json:"next"`
	Clearing   bool                `json:"clearing,omitempty"`
}

type usageLocalState struct {
	Generation string                   `json:"generation"`
	Bindings   map[string]*usageBinding `json:"bindings"`
	Caps       []string                 `json:"caps,omitempty"`
}

func LocalUsageSettings(root string) (UsageSettings, error) {
	var s UsageSettings
	err := readLiveJSON(filepath.Join(root, "usage-settings.json"), &s)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return s, err
}

// The caller must be the person-terminal command. No bridge/MCP operation
// enables or disables this setting; bridge refresh only rereads this file.
func SetLocalUsage(root string, enabled bool) error {
	return withUsageLock(root, func() error {
		cfg, err := LocalUsageSettings(root)
		if err != nil {
			return err
		}
		if cfg.Enabled == enabled {
			return nil
		}
		cfg.Enabled = enabled
		cfg.Generation = uuid.NewString()
		cfg.ChangedAt = time.Now().UTC()
		if err = atomicLiveJSON(filepath.Join(root, "usage-settings.json"), cfg); err != nil {
			return err
		}
		if !enabled {
			state, err := readUsageState(root)
			if err != nil {
				return err
			}
			for _, x := range state.Bindings {
				x.Clearing = true
				x.Pending = nil
				x.Available = false
				x.Last.Native = agentusage.Native{}
				x.Last.Delta = agentusage.Counters{}
			}
			state.Generation = cfg.Generation
			return atomicLiveJSON(filepath.Join(root, "usage-state.json"), state)
		}
		return nil
	})
}

func withUsageLock(root string, fn func() error) error {
	if err := privateLiveDir(root); err != nil {
		return err
	}
	p := filepath.Join(root, "usage.lock")
	if st, e := os.Lstat(p); e == nil && (!st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0) {
		return errors.New("unsafe usage state lock")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	unlock, err := lockFileExclusive(f)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (b *localBridge) saveUsageState(cfg UsageSettings, state usageLocalState) error {
	return withUsageLock(b.root, func() error {
		current, err := LocalUsageSettings(b.root)
		if err != nil {
			return err
		}
		if current.Generation != cfg.Generation || current.Enabled != cfg.Enabled {
			return errors.New("usage setting changed")
		}
		return atomicLiveJSON(filepath.Join(b.root, "usage-state.json"), state)
	})
}

func readUsageState(root string) (usageLocalState, error) {
	s := usageLocalState{Bindings: map[string]*usageBinding{}}
	err := readLiveJSON(filepath.Join(root, "usage-state.json"), &s)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if s.Bindings == nil {
		s.Bindings = map[string]*usageBinding{}
	}
	return s, err
}

func LocalUsageStatus(root string) (UsageStatus, error) {
	cfg, err := LocalUsageSettings(root)
	if err != nil {
		return UsageStatus{}, err
	}
	s, err := readUsageState(root)
	if err != nil {
		return UsageStatus{}, err
	}
	out := UsageStatus{UsageSettings: cfg, Caps: s.Caps}
	for _, x := range s.Bindings {
		if x.Clearing {
			out.PendingClear++
		} else {
			out.Sessions++
		}
		if x.Last.At.After(out.LastSample) {
			out.LastSample = x.Last.At
		}
	}
	return out, nil
}

func (b *localBridge) runUsage(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		b.usagePass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-b.usageWake:
		}
	}
}

func (b *localBridge) usageNow() time.Time {
	if b.options.UsageNow != nil {
		return b.options.UsageNow().UTC()
	}
	return time.Now().UTC()
}

func (b *localBridge) usagePass(ctx context.Context) {
	cfg, err := LocalUsageSettings(b.root)
	if err != nil {
		return
	}
	s, err := readUsageState(b.root)
	if err != nil {
		return
	}
	s.Caps = nil
	changed := s.Generation != cfg.Generation
	if changed || !cfg.Enabled {
		for _, x := range s.Bindings {
			x.Clearing = true
			x.Pending = nil
			// Off discards local numeric state even if the remote clear is
			// unavailable. Only the fenced clearing target remains locally.
			x.Last.Native = agentusage.Native{}
			x.Last.Delta = agentusage.Counters{}
		}
		s.Generation = cfg.Generation
		if b.saveUsageState(cfg, s) != nil {
			return
		}
	}
	for key, x := range s.Bindings {
		if !x.Clearing {
			continue
		}
		client, e := localConfiguredClient(x.ConfigPath)
		if e != nil {
			continue
		}
		r := x.Last
		r.At = b.usageNow().UTC()
		if r.Sequence < 1 {
			r.Sequence = 1
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, e = client.request(call, http.MethodPost, "/api/external/v1/agents/usage", map[string]any{"action": "off", "reading": r}, true)
		cancel()
		if e == nil {
			delete(s.Bindings, key)
		}
	}
	if !cfg.Enabled {
		_ = b.saveUsageState(cfg, s)
		return
	}
	if b.options.UsageRead == nil {
		return
	}
	b.mu.Lock()
	keys := []string{}
	for key, x := range b.sessions {
		if !x.Offline && x.client != nil && x.HostKey != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 64 {
		keys = keys[:64]
		s.Caps = append(s.Caps, "64 attached sessions")
	}
	b.mu.Unlock()
	remaining := int64(128 << 20)
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		latest, e := LocalUsageSettings(b.root)
		if e != nil || !latest.Enabled || latest.Generation != cfg.Generation {
			break
		}
		b.mu.Lock()
		current := b.sessions[key]
		if current == nil || current.Offline || current.client == nil {
			b.mu.Unlock()
			continue
		}
		a, host, client, path := current.Attachment, current.HostKey, current.client, current.ConfigPath
		b.mu.Unlock()
		if b.options.HostAlive == nil || !b.options.HostAlive(a.Provider, host) {
			continue
		}
		stateKey := a.ID
		x := s.Bindings[stateKey]
		if x != nil && x.Clearing {
			continue
		}
		if x == nil {
			x = &usageBinding{ConfigPath: path, Last: agentusage.Reading{AgentID: a.AgentID, AttachmentID: a.ID, Generation: uuid.NewString()}}
			s.Bindings[stateKey] = x
		}
		if x.Pending == nil && b.usageNow().Before(x.Next) {
			continue
		}
		if x.Pending == nil {
			if remaining <= 0 {
				s.Caps = append(s.Caps, "128 MiB per pass")
				break
			}
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			native, e := b.options.UsageRead(readCtx, a, host, remaining)
			cancel()
			remaining -= native.BytesRead
			x.Available = e == nil
			x.Reason = ""
			if e != nil {
				native = agentusage.Native{}
				x.Reason = "unavailable"
				if errors.Is(e, agentusage.ErrLimit) {
					x.Reason = "cap_reached"
					s.Caps = append(s.Caps, "native read/record cap")
				}
				if a.Provider == "cursor" || a.Provider == "agy" {
					x.Reason = "unsupported"
				}
			}
			r := agentusage.Reading{ID: uuid.NewString(), AgentID: a.AgentID, AttachmentID: a.ID, Generation: x.Last.Generation, Sequence: x.Last.Sequence + 1, At: b.usageNow().UTC(), Native: native, Baseline: x.Last.Sequence == 0}
			x.Pending = &r
			// Persist the exact reading before network IO. An uncertain retry
			// reuses it instead of sampling or incrementing a second time.
			if b.saveUsageState(cfg, s) != nil {
				return
			}
		}
		latest, e = LocalUsageSettings(b.root)
		if e != nil || !latest.Enabled || latest.Generation != cfg.Generation {
			break
		}
		b.mu.Lock()
		current = b.sessions[key]
		valid := current != nil && !current.Offline && current.Attachment.ID == a.ID && current.ConfigPath == path && current.HostKey == host
		b.mu.Unlock()
		if !valid {
			continue
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, e = client.request(call, http.MethodPost, "/api/external/v1/agents/usage", map[string]any{"action": "sample", "reading": x.Pending, "available": x.Available, "reason": x.Reason}, true)
		cancel()
		if e == nil {
			x.Last = *x.Pending
			x.Pending = nil
			x.Next = x.Last.At.Add(time.Hour)
		} else {
			var apiErr *APIError
			if errors.As(e, &apiErr) {
				if apiErr.Code == "usage_cap_reached" {
					s.Caps = append(s.Caps, "250000 owner history records")
				}
				if apiErr.Code == "usage_deleted" || apiErr.Code == "usage_baseline_required" {
					x.Last = *x.Pending
					x.Pending = nil
					x.Last.Sequence = 0
					x.Last.Generation = uuid.NewString()
					x.Next = b.usageNow().Add(time.Hour)
				}
			}
		}
		if b.saveUsageState(cfg, s) != nil {
			return
		}
	}
	_ = b.saveUsageState(cfg, s)
}

func (b *localBridge) usageControl(in LocalBridgeRequest) LocalBridgeReply {
	if in.Action == "usage_refresh" {
		select {
		case b.usageWake <- struct{}{}:
		default:
		}
	}
	status, err := LocalUsageStatus(b.root)
	if err != nil {
		return LocalBridgeReply{Error: "Local usage state unavailable"}
	}
	raw, _ := json.Marshal(status)
	return LocalBridgeReply{Result: raw}
}
