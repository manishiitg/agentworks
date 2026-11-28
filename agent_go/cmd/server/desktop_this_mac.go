package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// "This Mac" for a server project opened in the Mac app (owner, 27 Nov): the Mac app connects itself to a signed-in
// server as the person's computer, so they pick folders in Finder without installing or starting the command-line app.
// It asks that server once for a computer-only connection (devices:connect) with the saved sign-in and keeps it with
// the connection. The server's per-person laptop settings still decide which folders a project may use; this only keeps
// the computer online.

type thisMacRun struct {
	cancel context.CancelFunc
	status string // connecting, online, offline, signed_out
}

var thisMacRuns = struct {
	sync.Mutex
	m map[string]*thisMacRun
}{m: map[string]*thisMacRun{}}

var thisMacIDMu sync.Mutex

// thisMacDeviceID is this Mac's one computer name for every server, kept in the app's state so it never changes.
func thisMacDeviceID() (string, error) {
	thisMacIDMu.Lock()
	defer thisMacIDMu.Unlock()
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	file := filepath.Join(root, "this-mac-device-id")
	if raw, err := os.ReadFile(file); err == nil && localfiles.ValidID(strings.TrimSpace(string(raw))) {
		return strings.TrimSpace(string(raw)), nil
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	host = regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(host, "-")
	host = strings.Trim(host, "-")
	if host == "" {
		host = "mac"
	}
	if len(host) > 40 {
		host = host[:40]
	}
	id := host + "-" + uuid.NewString()[:8]
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return id, os.WriteFile(file, []byte(id), 0600)
}

func thisMacStatus(connection string) string {
	thisMacRuns.Lock()
	defer thisMacRuns.Unlock()
	if run := thisMacRuns.m[connection]; run != nil {
		return run.status
	}
	return ""
}

func setThisMacStatus(connection, status string) {
	thisMacRuns.Lock()
	defer thisMacRuns.Unlock()
	if run := thisMacRuns.m[connection]; run != nil {
		run.status = status
	}
}

func stopThisMac(connection string) {
	thisMacRuns.Lock()
	defer thisMacRuns.Unlock()
	if run := thisMacRuns.m[connection]; run != nil {
		run.cancel()
		delete(thisMacRuns.m, connection)
	}
}

// saveThisMacToken stores (or clears, with "") the computer connection of one saved server.
func saveThisMacToken(owner, connection, token string) error {
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	records, err := readDesktopConnections(owner)
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].ID == connection {
			records[i].DeviceToken = token
		}
	}
	return writeDesktopConnections(owner, records)
}

// mintThisMacToken asks the server for a computer-only connection with the saved sign-in.
func mintThisMacToken(ctx context.Context, owner string, c desktopConnection) (string, error) {
	body, _ := json.Marshal(map[string]any{"name": "This Mac (AgentWorks app)", "scopes": []string{"devices:connect"}, "expires_in_days": 90})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Server, "/")+"/api/auth/access-tokens", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := desktopServerDo(desktopHTTPClient(), owner, c, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the server refused a computer connection (%d)", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", errors.New("the server returned no computer connection")
	}
	return out.Token, nil
}

// ensureThisMac connects this Mac to one saved server as the person's computer, asking for the connection first when
// it has none. It returns at once; the connection keeps itself open in the background.
func (api *StreamingAPI) ensureThisMac(ctx context.Context, owner, connection string) (string, error) {
	device, err := thisMacDeviceID()
	if err != nil {
		return "", err
	}
	if thisMacStatus(connection) != "" {
		return device, nil
	}
	c, err := desktopConnectionFor(owner, connection)
	if err != nil {
		return "", err
	}
	if c.DeviceToken == "" {
		token, err := mintThisMacToken(ctx, owner, c)
		if err != nil {
			return "", err
		}
		if err := saveThisMacToken(owner, connection, token); err != nil {
			return "", err
		}
		c.DeviceToken = token
	}
	api.startThisMac(owner, c, device)
	return device, nil
}

func (api *StreamingAPI) startThisMac(owner string, c desktopConnection, device string) {
	thisMacRuns.Lock()
	if thisMacRuns.m[c.ID] != nil {
		thisMacRuns.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	thisMacRuns.m[c.ID] = &thisMacRun{cancel: cancel, status: "connecting"}
	thisMacRuns.Unlock()
	go api.runThisMac(ctx, owner, c, device)
}

func (api *StreamingAPI) runThisMac(ctx context.Context, owner string, c desktopConnection, device string) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		setThisMacStatus(c.ID, "offline")
		return
	}
	private := []string{root}
	if file, err := desktopConnectionFile(owner); err == nil {
		private = append(private, file)
	}
	executor, err := localfiles.OpenSettings(device, filepath.Join(root, "this-mac", c.ID), private)
	if err != nil {
		log.Printf("[THIS_MAC] connection=%s event=open_failed err=%v", c.ID, err)
		setThisMacStatus(c.ID, "offline")
		return
	}
	defer executor.Close()
	client, err := agentworksclient.New(c.Server, c.DeviceToken)
	if err != nil {
		setThisMacStatus(c.ID, "offline")
		return
	}
	client.WithExecutorOwner(c.User.ID)
	delay := time.Second
	for {
		err := client.ServeExecutor(ctx, executor, func() {
			delay = time.Second
			setThisMacStatus(c.ID, "online")
			log.Printf("[THIS_MAC] connection=%s device=%s event=online", c.ID, device)
		})
		if ctx.Err() != nil {
			return
		}
		var apiErr *agentworksclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
			// The computer connection expired or was revoked: forget it, so the next open asks for a new one.
			log.Printf("[THIS_MAC] connection=%s event=signed_out status=%d", c.ID, apiErr.Status)
			_ = saveThisMacToken(owner, c.ID, "")
			stopThisMac(c.ID)
			return
		}
		setThisMacStatus(c.ID, "offline")
		wait := delay + time.Duration(rand.Int64N(int64(delay/4)+1))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

// startThisMacConnections reconnects, when the app starts, every server this Mac was already connected to.
func (api *StreamingAPI) startThisMacConnections() context.CancelFunc {
	if !IsLocalMode() {
		return func() {}
	}
	go func() {
		owner := GetDefaultUserID()
		records, err := readDesktopConnections(owner)
		if err != nil {
			return
		}
		device, err := thisMacDeviceID()
		if err != nil {
			return
		}
		for _, c := range records {
			if c.DeviceToken != "" {
				api.startThisMac(owner, c, device)
			}
		}
	}()
	return func() {
		thisMacRuns.Lock()
		defer thisMacRuns.Unlock()
		for id, run := range thisMacRuns.m {
			run.cancel()
			delete(thisMacRuns.m, id)
		}
	}
}

// handleThisMac: GET says whether this Mac is connected to a saved server as the person's computer; POST connects it.
func (api *StreamingAPI) handleThisMac(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	connection := mux.Vars(r)["connection"]
	device, err := thisMacDeviceID()
	if err != nil {
		http.Error(w, `{"error":"This Mac's name could not be read."}`, 500)
		return
	}
	if r.Method == http.MethodPost {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if device, err = api.ensureThisMac(ctx, owner, connection); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "This Mac could not connect to that server: " + err.Error()})
			return
		}
	}
	status := thisMacStatus(connection)
	if status == "" {
		status = "not_connected"
	}
	desktopJSON(w, map[string]string{"device_id": device, "status": status})
}

// handleThisMacDownloads links the person's Downloads folder (Read) to a server project opened in this Mac app (owner,
// 28 Nov), as this Mac's own projects already have it (mac_downloads_access.go), so files saved there reach the
// project's agent without a settings screen. It does so only when the person has no computer saved for that project
// yet and has not removed Downloads on this Mac; a saved choice, even one with no folders, is never changed.
func (api *StreamingAPI) handleThisMacDownloads(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	connection := mux.Vars(r)["connection"]
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if !localfiles.ValidID(project) {
		http.Error(w, `{"error":"Name a project."}`, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	linked, err := api.linkThisMacDownloads(ctx, owner, connection, project)
	if err != nil {
		log.Printf("[THIS_MAC] connection=%s project=%s event=downloads_not_linked err=%v", connection, project, err)
	}
	desktopJSON(w, map[string]bool{"linked": linked})
}

func (api *StreamingAPI) linkThisMacDownloads(ctx context.Context, owner, connection, project string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	downloads := filepath.Join(home, "Downloads")
	if common.HostDownloadsRemoved.Load() {
		return false, nil
	}
	if info, err := os.Stat(downloads); err != nil || !info.IsDir() {
		return false, nil
	}
	c, err := desktopConnectionFor(owner, connection)
	if err != nil {
		return false, err
	}
	settingsURL := strings.TrimRight(c.Server, "/") + "/api/agent-profiles/work/projects/" + url.PathEscape(project) + "/laptop"
	call := func(method string, body any) (*crewLaptopSettings, int, error) {
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, settingsURL, reader)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := desktopServerDo(desktopHTTPClient(), owner, c, req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		var out crewLaptopSettings
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode, fmt.Errorf("the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return &out, resp.StatusCode, json.Unmarshal(raw, &out)
	}
	saved, status, err := call(http.MethodGet, nil)
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return false, nil // not this person's project to connect a computer to
	}
	if err != nil {
		return false, err
	}
	if saved.DeviceID != "" || len(saved.Folders) > 0 {
		return false, nil
	}
	device, err := api.ensureThisMac(ctx, owner, connection)
	if err != nil {
		return false, err
	}
	// The server checks the folder on the computer before saving it, so wait until this Mac is online.
	for thisMacStatus(connection) != "online" {
		select {
		case <-ctx.Done():
			return false, errors.New("this Mac did not come online in time")
		case <-time.After(250 * time.Millisecond):
		}
	}
	if _, _, err := call(http.MethodPut, crewLaptopSettings{DeviceID: device, Folders: []localfiles.FolderSelection{{Path: downloads, Access: "read"}}}); err != nil {
		return false, err
	}
	log.Printf("[THIS_MAC] connection=%s project=%s device=%s event=downloads_linked", connection, project, device)
	return true, nil
}

var gitHubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// handleThisMacCheckout finds a copy of a GitHub repo on this Mac (its origin is that repo), so a server project that
// works on the repo can offer it in one click. It looks in the home folder and the usual code folders, two levels deep.
func (api *StreamingAPI) handleThisMacCheckout(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	repo := strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("repo")), ".git")
	if !gitHubRepoPattern.MatchString(repo) {
		http.Error(w, `{"error":"Name a GitHub repo as owner/name."}`, 400)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		desktopJSON(w, map[string]any{"paths": []string{}})
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	found := []string{}
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if time.Now().After(deadline) || len(found) >= 5 || seen[dir] {
			return
		}
		seen[dir] = true
		if gitOriginIs(dir, repo) {
			found = append(found, dir)
			return
		}
		if depth == 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "Library" || name == "Applications" {
				continue
			}
			walk(filepath.Join(dir, name), depth-1)
		}
	}
	for _, base := range []string{"code", "Code", "projects", "Projects", "src", "dev", "work", "git", "repos", "github", "GitHub", "Developer", "Documents", "Desktop", "AgentWorks"} {
		walk(filepath.Join(home, base), 2)
	}
	walk(home, 1)
	desktopJSON(w, map[string]any{"paths": found})
}

// gitOriginIs reports whether dir is a git checkout whose origin is github.com/<repo>.
func gitOriginIs(dir, repo string) bool {
	f, err := os.Open(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return false
	}
	defer f.Close()
	inOrigin := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if !inOrigin || !strings.HasPrefix(line, "url") {
			continue
		}
		_, url, _ := strings.Cut(line, "=")
		url = strings.TrimSuffix(strings.TrimSpace(url), "/")
		url = strings.TrimSuffix(url, ".git")
		for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:", "ssh://git@github.com/"} {
			if strings.EqualFold(strings.TrimPrefix(url, prefix), repo) && strings.HasPrefix(url, prefix) {
				return true
			}
		}
	}
	return false
}
