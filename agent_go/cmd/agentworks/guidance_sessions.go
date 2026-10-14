package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Retain a handle only after the existing own-session verifier identifies an
// exact native chat. A generic terminal/stdio transport has no implied chat.
// These private files hold opaque receipts, never skill text or credentials.
func (o *options) configureGuidanceSession(client *agentworksclient.Client, serverURL, configPath string) {
	if o.guidanceSessionID != "" {
		client.WithGuidanceSession(o.guidanceSessionID, nil)
		return
	}
	if !o.guidanceChecked {
		o.guidanceChecked = true
		func() {
			root, err := agentworksclient.DefaultLocalBridgeRoot()
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			own, fromAgent, err := ownAgentSession(ctx, root, o)
			if err != nil || !fromAgent || own.session == "" {
				return
			}
			dir := configPath + ".guidance-sessions"
			if err = os.MkdirAll(dir, 0700); err != nil {
				return
			}
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return
			}
			if os.Chmod(dir, 0700) != nil {
				return
			}
			sum := sha256.Sum256([]byte(serverURL + "\x00" + own.provider + "\x00" + own.session))
			o.guidanceCachePath = filepath.Join(dir, hex.EncodeToString(sum[:])+".receipt")
			// Bound local resume state by expiry and count. Deleting a receipt only
			// loses deduplication; the server always rechecks authorization.
			entries, _ := os.ReadDir(dir)
			count := 0
			for _, entry := range entries {
				if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".receipt" {
					info, e := entry.Info()
					if e == nil && time.Since(info.ModTime()) >= 30*24*time.Hour {
						_ = os.Remove(filepath.Join(dir, entry.Name()))
					} else {
						count++
					}
				}
			}
			if count >= 128 {
				o.guidanceCachePath = ""
			}
		}()
	}
	if o.guidanceCachePath == "" {
		return
	}
	handle := ""
	if info, err := os.Lstat(o.guidanceCachePath); err == nil && info.Mode().IsRegular() && info.Size() == 68 {
		if data, err := os.ReadFile(o.guidanceCachePath); err == nil {
			handle = string(data)
		}
	}
	path := o.guidanceCachePath
	client.WithGuidanceSession(handle, func(id string) {
		if len(id) != 68 {
			return
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
		if err != nil {
			return
		}
		defer os.Remove(f.Name())
		_, err = f.WriteString(id)
		closeErr := f.Close()
		if err == nil && closeErr == nil {
			_ = os.Rename(f.Name(), path)
		}
	})
}
