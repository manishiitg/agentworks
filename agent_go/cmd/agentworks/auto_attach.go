package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A brand-new chat in a folder where the person attached a chat before comes back
// as that same agent, once the earlier chat is gone. The memory is one small
// private file per provider and folder: the name, purpose and login of attachments
// the person made themselves. It is dropped by detach, by /agentworks auto off, and
// whenever the server closed the lease.
type autoAttachRecord struct {
	Name       string `json:"name"`
	Purpose    string `json:"purpose,omitempty"`
	ConfigPath string `json:"config_path"`
}

type autoAttachFolder struct {
	Off     bool               `json:"off,omitempty"`
	Records []autoAttachRecord `json:"records,omitempty"`
}

var autoAttachMu sync.Mutex

func autoAttachDisabled() bool { return os.Getenv("AGENTWORKS_AUTO_ATTACH") == "0" }

func autoAttachPath(root string) string { return filepath.Join(root, "auto-attach.json") }

func autoAttachKey(provider, cwd string) string {
	if cwd == "" {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return provider + "|" + filepath.Clean(cwd)
}

// autoAttachUpdate loads, changes and saves the memory under one lock.
func autoAttachUpdate(root, key string, change func(*autoAttachFolder)) {
	if key == "" {
		return
	}
	autoAttachMu.Lock()
	defer autoAttachMu.Unlock()
	all := map[string]*autoAttachFolder{}
	if raw, err := os.ReadFile(autoAttachPath(root)); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	folder := all[key]
	if folder == nil {
		folder = &autoAttachFolder{}
		all[key] = folder
	}
	change(folder)
	if !folder.Off && len(folder.Records) == 0 {
		delete(all, key)
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return
	}
	tmp := autoAttachPath(root) + ".tmp"
	if os.WriteFile(tmp, raw, 0600) == nil {
		_ = os.Rename(tmp, autoAttachPath(root))
	}
}

func autoAttachRead(root, key string) autoAttachFolder {
	autoAttachMu.Lock()
	defer autoAttachMu.Unlock()
	all := map[string]*autoAttachFolder{}
	if raw, err := os.ReadFile(autoAttachPath(root)); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	if f := all[key]; f != nil && key != "" {
		return *f
	}
	return autoAttachFolder{}
}

// rememberAutoAttach records an attachment the person just made.
func rememberAutoAttach(root, provider, cwd string, a agentworksclient.AgentAttachment, configPath string) {
	if a.AgentName == "" || configPath == "" {
		return
	}
	autoAttachUpdate(root, autoAttachKey(provider, cwd), func(f *autoAttachFolder) {
		kept := f.Records[:0]
		for _, r := range f.Records {
			if r.Name != a.AgentName {
				kept = append(kept, r)
			}
		}
		f.Records = append(kept, autoAttachRecord{Name: a.AgentName, Purpose: a.Purpose, ConfigPath: configPath})
		if len(f.Records) > 5 {
			f.Records = f.Records[len(f.Records)-5:]
		}
	})
}

func forgetAutoAttach(root, provider, cwd, name string) {
	autoAttachUpdate(root, autoAttachKey(provider, cwd), func(f *autoAttachFolder) {
		kept := f.Records[:0]
		for _, r := range f.Records {
			if name != "" && r.Name != name {
				kept = append(kept, r)
			}
		}
		f.Records = kept
	})
}

func setAutoAttach(root, provider, cwd string, on bool) string {
	if cwd == "" {
		return "AgentWorks does not know this chat's folder."
	}
	autoAttachUpdate(root, autoAttachKey(provider, cwd), func(f *autoAttachFolder) { f.Off = !on })
	if on {
		return "Automatic reconnect is on for this folder: a new chat here reconnects as the agent you attached before, once that chat is closed."
	}
	return "Automatic reconnect is off for this folder. Type /agentworks attach in a new chat to connect it."
}

// autoAttachOnStart runs for a chat the bridge does not know. It returns one plain
// line for the person ("" when nothing applies); the chat is attached only when
// exactly one earlier agent of this folder exists, no live chat holds its name and
// the server did not close it.
func autoAttachOnStart(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest, cwd string) string {
	key := autoAttachKey(req.Provider, cwd)
	if autoAttachDisabled() || key == "" || req.HostKey == "" {
		return ""
	}
	folder := autoAttachRead(root, key)
	if folder.Off || len(folder.Records) == 0 {
		return ""
	}
	hint := "AgentWorks: this folder had an agent before. Type /agentworks attach to connect this chat (or /agentworks auto off to stop this hint)."
	if len(folder.Records) != 1 {
		return hint
	}
	record := folder.Records[0]
	list, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	if err != nil {
		return ""
	}
	for _, state := range list.AgentStates {
		if state.AgentName != record.Name {
			continue
		}
		if state.ServerClosed {
			forgetAutoAttach(root, req.Provider, cwd, record.Name)
			return ""
		}
		if state.Online && !(state.Provider == req.Provider && state.SessionID == req.SessionID) {
			return "AgentWorks: agent:" + record.Name + " is still attached to another chat in this folder. Type /agentworks attach NAME to give this chat its own agent."
		}
	}
	if _, statErr := os.Stat(record.ConfigPath); statErr != nil {
		forgetAutoAttach(root, req.Provider, cwd, record.Name)
		return ""
	}
	req.Action, req.AgentName, req.Purpose, req.ConfigPath = "attach", record.Name, record.Purpose, record.ConfigPath
	req.Setup, req.ReuseIdentity = true, false
	req.NativeRemote = discoverNativeRemote(req.Provider, req.SessionID, req.HostKey)
	if err = ensureLocalBridge(ctx, root, o); err != nil {
		return ""
	}
	out, err := agentworksclient.CallLocalBridge(ctx, root, req)
	if err != nil || !out.Attached || out.Error != "" {
		return hint
	}
	return fmt.Sprintf("AgentWorks: reconnected as agent:%s (the previous chat in this folder was closed). Type /agentworks detach to stop, or /agentworks auto off to turn this off for the folder.", out.Attachment.AgentName)
}

func autoAttachCommand(words []string) (on bool, ok bool) {
	if len(words) != 3 {
		return false, false
	}
	switch strings.ToLower(words[2]) {
	case "on":
		return true, true
	case "off":
		return false, true
	}
	return false, false
}
