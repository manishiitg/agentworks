package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// The approved MCP owns network authority. The native Monitor reads metadata
// from a private pipe without network permissions or the account credential.
type MuseMonitorSubscription struct {
	Source       string `json:"source"`
	SessionID    string `json:"session_id"`
	AttachmentID string `json:"attachment_id"`
	HostKey      string `json:"host_key"`
	DataHome     string `json:"muse_data_home"`
}
type museMonitorBroker struct {
	ctx           context.Context
	root, host    string
	current       func() (string, error)
	mu            sync.Mutex
	subscriptions map[string]*museMonitorPipe
}
type museMonitorPipe struct {
	path   string
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *museMonitorBroker) prepare(out LocalBridgeReply) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := out.Attachment.SessionID + ":" + out.Attachment.ID
	for k, p := range b.subscriptions {
		if k != key {
			p.cancel()
			delete(b.subscriptions, k)
		}
	}
	if p := b.subscriptions[key]; p != nil && p.ctx.Err() == nil {
		return p.path, nil
	}
	root, err := filepath.EvalSymlinks(b.root)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "muse-monitor")
	if err = privateLiveDir(dir); err != nil {
		return "", err
	}
	cleanupMuseMonitorFiles(dir)
	path := filepath.Join(dir, uuid.NewString()+".json")
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		data = filepath.Join(home, ".local", "share")
	}
	record := MuseMonitorSubscription{Source: "agentworks-muse-watch", SessionID: out.Attachment.SessionID, AttachmentID: out.Attachment.ID, HostKey: b.host, DataHome: data}
	// Hold lifetime evidence before publishing the JSON. A cleaner can never
	// mistake a newly published, not-yet-locked subscription for a crashed one.
	holder, err := holdMuseMonitorBroker(path)
	if err != nil {
		removeMuseMonitorFiles(path)
		return "", err
	}
	if err = createMuseMonitorFiles(path, record); err != nil {
		holder.Close()
		removeMuseMonitorFiles(path)
		return "", err
	}
	ctx, cancel := context.WithCancel(b.ctx)
	p := &museMonitorPipe{path: path, ctx: ctx, cancel: cancel}
	if b.subscriptions == nil {
		b.subscriptions = map[string]*museMonitorPipe{}
	}
	b.subscriptions[key] = p
	go func() {
		defer cancel()
		defer removeMuseMonitorFiles(path)
		defer holder.Close()
		writer, e := openMuseMonitorWriter(ctx, path)
		if e != nil || ctx.Err() != nil {
			if writer != nil {
				writer.Close()
			}
			return
		}
		defer writer.Close()
		go waitMuseMonitorReader(path, cancel)
		go func() { <-ctx.Done(); _ = writer.Close() }()
		// Recheck actual native UUID/process birth after the reader opens.
		id, e := b.current()
		if e != nil || id != record.SessionID {
			return
		}
		request := LocalBridgeRequest{Provider: "muse", SessionID: record.SessionID, HostKey: record.HostKey, AttachmentID: record.AttachmentID}
		encoder := json.NewEncoder(writer)
		e = WatchMuseBridge(ctx, b.root, request, func(h MuseWatchHint) error { return encoder.Encode(h) })
		if e != nil && ctx.Err() == nil {
			_ = encoder.Encode(MuseWatchHint{Kind: "error", Source: record.Source, SessionID: record.SessionID, AttachmentID: record.AttachmentID, Error: "AgentWorks Monitor stream stopped; report this error and use lifecycle hooks until the connection is restored"})
		}
	}()
	return path, nil
}

func ReadMuseMonitorSubscription(path string) (MuseMonitorSubscription, error) {
	var record MuseMonitorSubscription
	if filepath.Base(filepath.Dir(path)) != "muse-monitor" || filepath.Ext(path) != ".json" {
		return record, errors.New("invalid Muse subscription path")
	}
	if _, err := uuid.Parse(filepath.Base(path[:len(path)-len(".json")])); err != nil {
		return record, errors.New("invalid Muse subscription ID")
	}
	file, err := openMuseMonitorPrivate(path, false)
	if err != nil {
		return record, err
	}
	defer file.Close()
	if err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&record); err != nil {
		return record, err
	}
	if record.Source != "agentworks-muse-watch" || record.AttachmentID == "" || record.HostKey == "" || record.DataHome == "" {
		return record, errors.New("invalid Muse subscription metadata")
	}
	if _, err = uuid.Parse(record.SessionID); err != nil {
		return record, err
	}
	return record, nil
}
