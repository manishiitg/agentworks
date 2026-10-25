package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// The override selects SDK routing; an environment value is not person proof.
// Other providers retain the shared default unchanged.
func piBridgeRoot() (string, error) {
	root := os.Getenv("AGENTWORKS_BRIDGE_ROOT")
	if root == "" {
		return agentworksclient.DefaultLocalBridgeRoot()
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("Pi bridge root must be an absolute clean path")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || !piPrivateInfo(info) {
		return "", errors.New("Pi bridge root must be an owned private directory")
	}
	return root, nil
}

type piSessionRecord struct {
	PID     int    `json:"pid"`
	Host    string `json:"host_key"`
	Session string `json:"session_id"`
	Socket  string `json:"socket"`
	Cwd     string `json:"cwd"`
	Mode    string `json:"mode"`
}

// Pi rewrites its process title; Node/script launches must resolve the actual
// Pi entrypoint rather than matching an arbitrary process with a UUID argument.
func piProcessFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	if strings.EqualFold(filepath.Base(fields[0]), "pi") {
		return true
	}
	executable := strings.ToLower(filepath.Base(fields[0]))
	if executable != "node" && executable != "nodejs" {
		return false
	}
	for _, field := range fields[1:] {
		if !strings.HasSuffix(field, ".js") && !strings.HasSuffix(field, ".mjs") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(field)
		if err != nil {
			continue
		}
		name := filepath.ToSlash(resolved)
		if strings.Contains(name, "/pi-coding-agent/dist/") && (strings.HasSuffix(name, "/cli.js") || strings.HasSuffix(name, "/pi.mjs")) {
			return true
		}
	}
	return false
}

func piPIDHostKey(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=", "-o", "args=", "-o", "lstart=")
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := command.Output()
	if err != nil {
		return "", errors.New("Pi process is unavailable")
	}
	fields := strings.Fields(string(out))
	if len(fields) < 7 || !piProcessFields(fields[:len(fields)-5]) {
		return "", errors.New("process is not an exact Pi host")
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(pid) + " " + strings.Join(fields[len(fields)-5:], " ")))
	return hex.EncodeToString(sum[:]), nil
}

func readPiSession(root, file string) (piSessionRecord, error) {
	var record piSessionRecord
	directory := filepath.Join(root, "pi-sessions")
	if filepath.Dir(file) != directory {
		return record, errors.New("invalid Pi registration path")
	}
	for _, dir := range []string{root, directory} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !piPrivateInfo(info) {
			return record, errors.New("Pi registration directory is not private")
		}
	}
	f, err := openPiRegistration(file)
	if err != nil {
		return record, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !piPrivateInfo(info) || info.Size() > 4096 {
		return record, errors.New("Pi registration is not private")
	}
	if json.NewDecoder(io.LimitReader(f, 4097)).Decode(&record) != nil || record.PID < 2 || filepath.Base(file) != strconv.Itoa(record.PID)+".json" {
		return record, errors.New("invalid Pi registration")
	}
	if _, err := uuid.Parse(record.Session); err != nil {
		return record, errors.New("Pi SDK did not supply a native UUID")
	}
	if filepath.Dir(record.Socket) != root || !strings.HasPrefix(filepath.Base(record.Socket), "pi-"+strconv.Itoa(record.PID)+"-") || !strings.HasSuffix(record.Socket, ".sock") {
		return record, errors.New("invalid Pi activation socket")
	}
	info, err = os.Lstat(record.Socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || !piPrivateInfo(info) {
		return record, errors.New("Pi activation socket is not private")
	}
	key, err := piPIDHostKey(record.PID)
	if err != nil || key != record.Host {
		return record, errors.New("Pi registration process fence changed")
	}
	return record, nil
}

func piSessionForHost(root, host, session string) (piSessionRecord, error) {
	files, _ := filepath.Glob(filepath.Join(root, "pi-sessions", "*.json"))
	for i, file := range files {
		if i >= 256 {
			break
		}
		record, err := readPiSession(root, file)
		if err != nil {
			tidyPiSession(root, file)
			continue
		}
		if record.Host == host && (session == "" || record.Session == session) {
			return record, nil
		}
	}
	return piSessionRecord{}, errors.New("no SDK registration for this exact Pi process/session; reload Pi")
}

func activatePiSession(ctx context.Context, root, host, session, lease string) error {
	record, err := piSessionForHost(root, host, session)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"host_key": host, "session_id": session, "attachment_id": lease})
	if err != nil {
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialPiSDK(ctx, record)
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/activate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return errors.New("Pi activation unavailable; type /agentworks attach in that chat")
	}
	defer response.Body.Close()
	var result struct {
		Active bool `json:"active"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil || !result.Active {
		return errors.New("Pi refused activation of this exact lease; type /agentworks attach in that chat")
	}
	return nil
}

// Same-user records and socket paths can be replaced by a model shell. Check
// the kernel peer before sending any nonce or accepting an SDK response.
func dialPiSDK(ctx context.Context, record piSessionRecord) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", record.Socket)
	if err != nil {
		return nil, err
	}
	refuse := func() (net.Conn, error) {
		_ = connection.Close()
		return nil, errors.New("Pi SDK socket does not belong to its exact live process")
	}
	socket, ok := connection.(*net.UnixConn)
	if !ok {
		return refuse()
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return refuse()
	}
	var pid int
	var peerErr error
	if err = raw.Control(func(fd uintptr) { pid, peerErr = localSocketPeerPID(int(fd)) }); err != nil || peerErr != nil || pid != record.PID {
		return refuse()
	}
	host, err := piPIDHostKey(pid)
	if err != nil || host != record.Host {
		return refuse()
	}
	return connection, nil
}

// Installation uses the bundled source or this exact stamped source revision.
// The current project is not a trusted package source.
func installPiPlugin(ctx context.Context, o *options) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	source := filepath.Join(filepath.Dir(executable), "plugins", "pi")
	manifest, err := os.ReadFile(filepath.Join(source, "package.json"))
	var parsed struct {
		Name string `json:"name"`
	}
	bundled := err == nil && json.Unmarshal(manifest, &parsed) == nil && parsed.Name == "@agentworks/pi"
	revision := strings.TrimSpace(cliVersion)
	if !bundled {
		if len(revision) != 40 {
			return errors.New("this development CLI has no bundled Pi package or pinned revision; install a reviewed plugins/pi directory manually, reload Pi and attach")
		}
		if _, err := hex.DecodeString(revision); err != nil {
			return errors.New("CLI revision cannot pin the Pi package")
		}
		source = "https://github.com/manishiitg/agentworks at commit " + revision + " (plugins/pi)"
	}
	if !o.setupAll {
		fmt.Fprintf(o.stderr, "Install AgentWorks Pi in %s from %s? Review Pi trust prompts afterward. Type yes: ", cwd, source)
		var answer string
		if _, err = fmt.Fscanln(o.stdin, &answer); err != nil || answer != "yes" {
			return errors.New("Pi package installation cancelled")
		}
	}
	if !bundled {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		parent := filepath.Join(cache, "agentworks")
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !piPrivateInfo(info) {
			return errors.New("Pi package cache is not private")
		}
		dir, err := os.MkdirTemp(parent, "pi-package-")
		if err != nil {
			return err
		}
		clone := exec.CommandContext(ctx, "git", "clone", "--depth=1", "--filter=blob:none", "--sparse", "--no-checkout", "https://github.com/manishiitg/agentworks", dir)
		if err = clone.Run(); err == nil {
			err = exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--depth=1", "origin", revision).Run()
		}
		if err == nil {
			err = exec.CommandContext(ctx, "git", "-C", dir, "sparse-checkout", "set", "plugins/pi").Run()
		}
		if err == nil {
			err = exec.CommandContext(ctx, "git", "-C", dir, "checkout", "--detach", revision).Run()
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return errors.New("could not download pinned Pi package; install a reviewed plugins/pi directory manually")
		}
		source = filepath.Join(dir, "plugins", "pi")
	}
	piArgs := []string{"install", source, "--local"}
	if o.setupAll {
		piArgs = piArgs[:2] // every project
	}
	install := exec.CommandContext(ctx, "pi", piArgs...)
	install.Stdin, install.Stdout, install.Stderr = o.stdin, o.stdout, o.stderr
	if err := install.Run(); err != nil {
		return errors.New("Pi package installation failed; review the Pi output")
	}
	return nil
}

// The nonce exists only in the SDK instance's memory. Neither the parent PID
// nor a model-authored hook JSON document is sufficient person authorization.
func confirmPiPersonInput(ctx context.Context, root, host, session, prompt, token string) error {
	if token == "" {
		return errors.New("missing Pi person-input proof")
	}
	record, err := piSessionForHost(root, host, session)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"host_key": host, "session_id": session, "prompt": prompt, "pi_person_token": token})
	if err != nil {
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialPiSDK(ctx, record)
	}}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/person-input", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return errors.New("Pi SDK person-input proof unavailable")
	}
	defer response.Body.Close()
	var result struct {
		PersonInput bool `json:"person_input"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil || !result.PersonInput {
		return errors.New("Pi rejected person-input proof")
	}
	return nil
}

// Retire only old private registrations with proof their process ended or its
// birth identity changed. Permission/ps failures never authorize deletion.
func tidyPiSession(root, file string) {
	directory := filepath.Join(root, "pi-sessions")
	if filepath.Dir(file) != directory {
		return
	}
	for _, dir := range []string{root, directory} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !piPrivateInfo(info) {
			return
		}
	}
	f, err := openPiRegistration(file)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !piPrivateInfo(info) || info.Size() > 4096 || time.Since(info.ModTime()) < time.Minute {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(file), ".json"))
	if err != nil || pid < 2 {
		return
	}
	var record piSessionRecord
	parsed := json.NewDecoder(io.LimitReader(f, 4097)).Decode(&record) == nil && record.PID == pid
	dead := piProcessGone(pid)
	if !dead {
		key, e := piPIDHostKey(pid)
		if !parsed || e != nil || key == record.Host {
			return
		}
	}
	// Check that a live writer did not atomically replace the opened record.
	current, err := os.Lstat(file)
	if err != nil || !os.SameFile(info, current) {
		return
	}
	if parsed && filepath.Dir(record.Socket) == root && strings.HasPrefix(filepath.Base(record.Socket), "pi-"+strconv.Itoa(pid)+"-") && strings.HasSuffix(record.Socket, ".sock") {
		socket, e := os.Lstat(record.Socket)
		if e == nil && socket.Mode()&os.ModeSocket != 0 && piPrivateInfo(socket) {
			_ = os.Remove(record.Socket)
		}
	}
	_ = os.Remove(file)
}

func discoverPiLiveAgents(root string, local agentworksclient.LocalBridgeReply) []liveAgentSession {
	names := map[string]string{}
	for _, state := range localAttachedChats(local) {
		if state.Provider == "pi" {
			names[state.SessionID] = state.AgentName
		}
	}
	files, _ := filepath.Glob(filepath.Join(root, "pi-sessions", "*.json"))
	found := []liveAgentSession{}
	for i, file := range files {
		if i >= 256 {
			break
		}
		record, err := readPiSession(root, file)
		if err != nil {
			tidyPiSession(root, file)
			continue
		}
		if record.Mode != "tui" {
			continue
		}
		found = append(found, liveAgentSession{provider: "pi", session: record.Session, host: record.Host, cwd: record.Cwd, name: names[record.Session]})
	}
	return found
}
