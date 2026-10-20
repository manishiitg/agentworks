package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	LabelName  string `json:"label_name,omitempty"`
	Name       string `json:"name"`
	SessionID  string `json:"session_id,omitempty"`
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
	// Hook processes run concurrently: a unique temp name keeps two writers from
	// sharing one file, and the rename keeps readers from seeing half a file.
	tmp, err := os.CreateTemp(root, "auto-attach-*.tmp")
	if err != nil {
		return
	}
	if err = agentworksclient.SecurePrivateFile(tmp); err == nil {
		_, err = tmp.Write(raw)
	}
	if err != nil || tmp.Close() != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), autoAttachPath(root)) != nil {
		os.Remove(tmp.Name())
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
func rememberAutoAttach(root, provider, cwd string, a agentworksclient.AgentAttachment, configPath, labelName string) {
	if a.AgentName == "" || configPath == "" {
		return
	}
	autoAttachUpdate(root, autoAttachKey(provider, cwd), func(f *autoAttachFolder) {
		kept := f.Records[:0]
		for _, r := range f.Records {
			if r.Name != a.AgentName {
				kept = append(kept, r)
			} else if labelName == "" {
				labelName = r.LabelName
			}
		}
		f.Records = append(kept, autoAttachRecord{Name: a.AgentName, SessionID: a.SessionID, Purpose: a.Purpose, ConfigPath: configPath, LabelName: labelName})
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

// forgetDetachedAutoAttach drops the agent a chat just detached from: by name when known, and
// otherwise by the chat's session (records made before sessions were kept are matched by name only).
func forgetDetachedAutoAttach(root, provider, cwd, name, sessionID string) {
	autoAttachUpdate(root, autoAttachKey(provider, cwd), func(f *autoAttachFolder) {
		kept := f.Records[:0]
		for _, r := range f.Records {
			if name != "" && r.Name == name || name == "" && sessionID != "" && r.SessionID == sessionID {
				continue
			}
			kept = append(kept, r)
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
		// Never guess between agents without the person typing; name the free ones
		// so a bare /agentworks attach is a known choice.
		if free := freeAutoAttachRecords(ctx, root, req, cwd); len(free) > 0 {
			names := make([]string, len(free))
			for i, r := range free {
				names[i] = "agent:" + r.Name
			}
			return "AgentWorks: this folder had " + strings.Join(names, ", ") + ". Type /agentworks attach to reconnect as agent:" + free[0].Name + " (most recent), or /agentworks attach NAME."
		}
		return hint
	}
	record := folder.Records[0]
	list, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	if err != nil {
		return ""
	}
	serverOnline := false
	if reply, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "agent_online", AgentName: record.Name, ConfigPath: record.ConfigPath}); e == nil && reply.Error == "" {
		var seen struct {
			Online bool `json:"online"`
		}
		_ = json.Unmarshal(reply.Result, &seen)
		serverOnline = seen.Online
	} else {
		return "" // cannot prove the name is free: never take it on a guess
	}
	switch autoAttachDecision(list.AgentStates, record.Name, req, serverOnline) {
	case "forget":
		forgetAutoAttach(root, req.Provider, cwd, record.Name)
		return ""
	case "hint":
		return "AgentWorks: agent:" + record.Name + " is still attached to another chat. Type /agentworks attach NAME to give this chat its own agent."
	}
	if _, statErr := os.Stat(record.ConfigPath); statErr != nil {
		forgetAutoAttach(root, req.Provider, cwd, record.Name)
		return ""
	}
	req.Action, req.AgentName, req.Purpose, req.ConfigPath = "attach", record.Name, record.Purpose, record.ConfigPath
	req.LabelName = record.LabelName
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

// autoAttachDecision says what a new chat may do with an earlier agent's name:
// "attach" only when no chat of this computer still holds it (online, or offline
// while its process runs) and the server sees it attached nowhere else; "forget"
// when the server closed it; otherwise "hint".
func autoAttachDecision(states []agentworksclient.LocalAgentState, name string, req agentworksclient.LocalBridgeRequest, serverOnline bool) string {
	for _, state := range states {
		if state.AgentName != name {
			continue
		}
		if state.ServerClosed {
			return "forget"
		}
		own := state.Provider == req.Provider && state.SessionID == req.SessionID
		if !own && (state.Online || state.HostAlive) {
			return "hint"
		}
	}
	if serverOnline {
		return "hint"
	}
	return "attach"
}

// freeAutoAttachRecords lists this folder's earlier agents that nothing holds,
// most recent first: no live chat or running process on this computer, not
// attached elsewhere on the server, not closed by it. A name that cannot be
// proven free is left out.
func freeAutoAttachRecords(ctx context.Context, root string, req agentworksclient.LocalBridgeRequest, cwd string) []autoAttachRecord {
	free, _ := autoAttachScan(ctx, root, req, cwd)
	return free
}

// autoAttachScan lists the folder's free earlier agents (see freeAutoAttachRecords) and, when there is none,
// one plain sentence saying why a bare attach did not bring an earlier agent back ("" when there was nothing to
// bring back or the reason is unknown).
func autoAttachScan(ctx context.Context, root string, req agentworksclient.LocalBridgeRequest, cwd string) ([]autoAttachRecord, string) {
	key := autoAttachKey(req.Provider, cwd)
	if autoAttachDisabled() || key == "" {
		return nil, ""
	}
	folder := autoAttachRead(root, key)
	if folder.Off {
		return nil, "Automatic reconnect is off for this folder (/agentworks auto on turns it on)."
	}
	if len(folder.Records) == 0 {
		return nil, otherFolderAgents(root, req.Provider, key)
	}
	list, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	if err != nil {
		return nil, ""
	}
	var free []autoAttachRecord
	why := ""
	note := func(reason string) {
		if why == "" { // the most recent agent's reason is the one worth saying
			why = reason
		}
	}
	for i := len(folder.Records) - 1; i >= 0; i-- {
		record := folder.Records[i]
		reply, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "agent_online", AgentName: record.Name, ConfigPath: record.ConfigPath})
		if e != nil || reply.Error != "" {
			note("agent:" + record.Name + " could not be checked on the server just now, so it was not taken.")
			continue
		}
		var seen struct {
			Online bool `json:"online"`
		}
		_ = json.Unmarshal(reply.Result, &seen)
		switch autoAttachDecision(list.AgentStates, record.Name, req, seen.Online) {
		case "forget":
			forgetAutoAttach(root, req.Provider, cwd, record.Name)
			note("agent:" + record.Name + " was closed by the server, so it was forgotten.")
		case "hint":
			if seen.Online {
				note("agent:" + record.Name + " still looks online on the server (it can take a minute after its chat closes); try again shortly, or attach it by name.")
			} else {
				note("agent:" + record.Name + " is still attached to another chat on this computer.")
			}
		case "attach":
			if _, statErr := os.Stat(record.ConfigPath); statErr == nil {
				free = append(free, record)
			} else {
				note("agent:" + record.Name + "'s saved login is gone, so it could not be reconnected.")
			}
		}
	}
	return free, why
}

// otherFolderAgents says when this provider has agents remembered only for other folders: reuse is per folder, so
// the person can still take one by name.
func otherFolderAgents(root, provider, ownKey string) string {
	autoAttachMu.Lock()
	all := map[string]*autoAttachFolder{}
	if raw, err := os.ReadFile(autoAttachPath(root)); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	autoAttachMu.Unlock()
	var names []string
	for k, f := range all {
		if k != ownKey && f != nil && strings.HasPrefix(k, provider+"|") && len(f.Records) > 0 {
			names = append(names, "agent:"+f.Records[len(f.Records)-1].Name+" (used in "+filepath.Base(strings.TrimPrefix(k, provider+"|"))+")")
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	if len(names) > 3 {
		names = names[:3]
	}
	return "This folder has no earlier agent, and a new agent was created. Earlier agents belong to other folders: " + strings.Join(names, ", ") + "; /agentworks attach NAME takes one by name."
}
