package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

type rememberedBrowserTabs struct {
	Version int      `json:"version"`
	Daemon  string   `json:"daemon"`
	URLs    []string `json:"urls"`
	Active  int      `json:"active"`
}
type rememberedTab struct {
	Ref    string `json:"tabId"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

var browserTabLocks [64]sync.Mutex
var browserTabMonitors sync.Map

func browserTabsLock(session string) *sync.Mutex {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(session))
	return &browserTabLocks[hash.Sum32()%64]
}
func browserTabsPath(session string) (string, error) {
	if !browserconfig.IsUserSession(session) {
		return "", fmt.Errorf("browser is ephemeral")
	}
	profile := browserconfig.ProfilePathForSession(session)
	if profile == "" {
		return "", fmt.Errorf("browser is ephemeral")
	}
	real, err := filepath.EvalSymlinks(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(real, ".agentworks-tabs.json"), nil
}
func currentBrowserTabs(ctx context.Context, socket, session string) ([]rememberedTab, error) {
	out, err := existingBrowserCommand(ctx, socket, session, map[string]any{"id": "remember-tabs", "action": "tab_list"})
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			Tabs []rememberedTab `json:"tabs"`
		} `json:"data"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		return nil, err
	}
	return result.Data.Tabs, nil
}
func saveBrowserTabs(ctx context.Context, socket, session string, expectedPID ...string) error {
	lock := browserTabsLock(session)
	lock.Lock()
	defer lock.Unlock()
	statePath, err := browserTabsPath(session)
	if err != nil {
		return err
	}
	pid, err := os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil {
		return err
	}
	if len(expectedPID) > 0 && expectedPID[0] != string(pid) {
		return fmt.Errorf("browser restarted")
	}
	tabs, err := currentBrowserTabs(ctx, socket, session)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil || string(current) != string(pid) {
		return fmt.Errorf("browser restarted")
	}
	state := rememberedBrowserTabs{Version: 1, Daemon: string(pid), URLs: []string{}}
	for _, tab := range tabs {
		if len(state.URLs) >= 50 {
			break
		}
		if len(tab.URL) > 8192 || !validTeachingAddress(tab.URL) {
			continue
		}
		if tab.Active {
			state.Active = len(state.URLs)
		}
		state.URLs = append(state.URLs, tab.URL)
	}
	// No pages during shutdown is not an intentional closed tab strip.
	if len(state.URLs) == 0 {
		return nil
	}
	return writeRememberedBrowserTabs(statePath, state)
}
func writeRememberedBrowserTabs(statePath string, state rememberedBrowserTabs) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(statePath); e == nil {
		if !info.Mode().IsRegular() || info.Size() > 512<<10 {
			return fmt.Errorf("invalid browser tab state")
		}
		previous, e := os.ReadFile(statePath)
		if e == nil && string(previous) == string(data) {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	file, err := os.CreateTemp(filepath.Dir(statePath), ".tabs-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), statePath)
}
func restoreBrowserTabs(ctx context.Context, session string) error {
	if browserconfig.IsUserSession(session) && browserconfig.ProfilePathForSession(session) == "" {
		return nil
	}
	lock := browserTabsLock(session)
	lock.Lock()
	defer lock.Unlock()
	statePath, err := browserTabsPath(session)
	if err != nil {
		return err
	}
	info, err := os.Lstat(statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return fmt.Errorf("invalid browser tab state")
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state rememberedBrowserTabs
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || len(state.URLs) > 50 || state.Active < 0 || state.Active >= len(state.URLs) {
		return fmt.Errorf("invalid browser tab state")
	}
	for _, address := range state.URLs {
		if len(address) > 8192 || !validTeachingAddress(address) {
			return fmt.Errorf("invalid browser tab address")
		}
	}
	_, socket, err := browserLiveEndpoint(session)
	if err != nil {
		return err
	}
	tabs, err := currentBrowserTabs(ctx, socket, session)
	if err != nil {
		return err
	}
	pid, err := os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil {
		return err
	}
	// Repeated Start must not change the selected tab of a live browser.
	if state.Daemon == string(pid) {
		return nil
	}
	// Full Chrome may restore pages itself, in a different target order. Reuse
	// those targets and select the remembered page without closing any of them.
	if len(tabs) != 1 || tabs[0].URL != "about:blank" {
		if len(tabs) != len(state.URLs) {
			return nil
		}
		used := map[int]bool{}
		selected := ""
		for i, address := range state.URLs {
			found := false
			for j, tab := range tabs {
				if !used[j] && tab.URL == address {
					used[j] = true
					found = true
					if i == state.Active {
						selected = tab.Ref
					}
					break
				}
			}
			if !found {
				return nil
			}
		}
		if _, err = existingBrowserCommand(ctx, socket, session, map[string]any{"id": "restore-active", "action": "tab_switch", "tabId": selected}); err != nil {
			return err
		}
		state.Daemon = string(pid)
		return writeRememberedBrowserTabs(statePath, state)
	}
	for i, address := range state.URLs {
		action := "tab_new"
		if i == 0 {
			action = "navigate"
		}
		if _, err = existingBrowserCommand(ctx, socket, session, map[string]any{"id": "restore-tab", "action": action, "url": address}); err != nil {
			return err
		}
	}
	tabs, err = currentBrowserTabs(ctx, socket, session)
	if err != nil {
		return err
	}
	if len(tabs) != len(state.URLs) {
		return fmt.Errorf("browser tabs changed during restore")
	}
	_, err = existingBrowserCommand(ctx, socket, session, map[string]any{"id": "restore-active", "action": "tab_switch", "tabId": tabs[state.Active].Ref})
	if err != nil {
		return err
	}
	state.Daemon = string(pid)
	return writeRememberedBrowserTabs(statePath, state)
}

// Service-only: the agent checks workspace ownership, write access and the
// browser lease before starting/restoring. No addresses are supplied by HTTP.
func BrowserRestoreTabs(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := restoreBrowserTabs(ctx, c.Param("session")); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Previous tabs could not be reopened. Start the browser again to retry."})
		return
	}
	monitorBrowserTabs(c.Param("session"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type browserTabMonitor struct {
	pid  string
	stop chan struct{}
}

// A private profile snapshot survives daemon crashes, idle reaping and deploys.
// It runs while the daemon lives, including after the panel is closed. A changed
// PID stops the old monitor before it can overwrite remembered pages with blank.
func monitorBrowserTabs(session string) {
	if !browserconfig.IsUserSession(session) {
		return
	}
	_, socket, err := browserLiveEndpoint(session)
	if err != nil {
		return
	}
	pidPath := filepath.Join(socket, session+".pid")
	pid, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	monitor := &browserTabMonitor{pid: string(pid), stop: make(chan struct{})}
	old, loaded := browserTabMonitors.LoadOrStore(session, monitor)
	if loaded {
		previous := old.(*browserTabMonitor)
		if previous.pid == monitor.pid {
			return
		}
		if !browserTabMonitors.CompareAndSwap(session, old, monitor) {
			return
		}
		close(previous.stop)
	}
	go func() {
		defer browserTabMonitors.CompareAndDelete(session, monitor)
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		failures := 0
		for {
			select {
			case <-monitor.stop:
				return
			case <-timer.C:
			}
			current, e := os.ReadFile(pidPath)
			if e != nil || string(current) != monitor.pid {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			e = saveBrowserTabs(ctx, socket, session, monitor.pid)
			cancel()
			if e != nil {
				// Browser commands serialize: a long navigation can temporarily
				// delay tab inspection. Do not lose memory after one busy read.
				failures++
				if failures >= 10 {
					return
				}
				continue
			}
			failures = 0
		}
	}()
}
