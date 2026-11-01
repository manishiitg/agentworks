package server

// The server's model proxy for company logins used from the Mac app (PLAT-856-441). AgentWorks runs CLIProxyAPI
// (`cli-proxy-api`, installed with the server's tools) itself, as a child process listening only on 127.0.0.1 with
// a random client key, and keeps its config and logins in a private folder no AI program on the server can read.
// An admin turns a program on by signing the proxy in from that program's admin-managed account card (a link and a
// code to approve in the browser) and off by signing it out; the proxy's login is its own, separate from the
// server's own login for the program.
import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

// modelProxyLogins is each program the proxy can hold a login for: its sign-in option and the prefix of the login
// files it writes.
var modelProxyLogins = map[string]struct {
	flag, prefix string
	// callback: the sign-in ends on a page of the server (localhost) that the admin's browser cannot reach; the
	// admin pastes the address it ended on and it is typed into the sign-in.
	callback bool
}{
	"muse-cli":    {flag: "--meta-login", prefix: "meta-"},
	"codex-cli":   {flag: "--codex-device-login", prefix: "codex-"},
	"claude-code": {flag: "--claude-login", prefix: "claude-", callback: true},
}

type modelProxySignIn struct {
	URL           string    `json:"url"`
	Code          string    `json:"code,omitempty"`
	NeedsCallback bool      `json:"needs_callback,omitempty"`
	Started       time.Time `json:"started"`
	cmd           *exec.Cmd
	stdin         io.WriteCloser
}

var companyProxy = struct {
	sync.Mutex
	cmd     *exec.Cmd
	port    int
	key     string
	signIns map[string]*modelProxySignIn
	catalog map[string]cachedCatalog
}{signIns: map[string]*modelProxySignIn{}, catalog: map[string]cachedCatalog{}}

type cachedCatalog struct {
	body []byte
	at   time.Time
}

func modelProxyDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "state", "agentworks", "model-proxy")
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

// modelProxyBinary is the installed proxy program; without it the server offers no company logins to Macs.
func modelProxyBinary() string {
	if desktopBootstrapHost != "" {
		return "" // the Mac app uses servers' proxies; it runs none itself
	}
	path, err := exec.LookPath("cli-proxy-api")
	if err != nil {
		return ""
	}
	return path
}

// modelProxyConfig writes the proxy's config: 127.0.0.1 only, the client key, no management API.
func modelProxyConfig(dir string, port int, key string) (string, error) {
	config := fmt.Sprintf("config-version: 8\nserver:\n  host: \"127.0.0.1\"\n  port: %d\nmanagement:\n  allow-remote: false\n  secret-key: \"\"\n  disable-control-panel: true\naccess:\n  api-keys:\n    - %q\noauth:\n  auth-dir: %q\n", port, key, filepath.Join(dir, "auth"))
	path := filepath.Join(dir, "config.yaml")
	return path, os.WriteFile(path, []byte(config), 0o600)
}

// modelProxyLoginFiles are the proxy's saved logins for a program.
func modelProxyLoginFiles(dir, provider string) []string {
	login, ok := modelProxyLogins[provider]
	if !ok {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "auth", login.prefix+"*.json"))
	return files
}

// modelProxySignedInAs is the email of the proxy's login for a program, or "" when it has none.
func modelProxySignedInAs(provider string) string {
	dir, err := modelProxyDir()
	if err != nil {
		return ""
	}
	for _, file := range modelProxyLoginFiles(dir, provider) {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var login struct {
			Email    string `json:"email"`
			Disabled bool   `json:"disabled"`
		}
		if json.Unmarshal(raw, &login) == nil && !login.Disabled {
			if login.Email == "" {
				login.Email = "signed in"
			}
			return login.Email
		}
	}
	return ""
}

// companyModelProxyUpstream starts the proxy if needed and returns its address and client key.
func companyModelProxyUpstream() (string, string, bool) {
	binary := modelProxyBinary()
	if binary == "" {
		return "", "", false
	}
	companyProxy.Lock()
	defer companyProxy.Unlock()
	if companyProxy.cmd != nil && companyProxy.cmd.ProcessState == nil {
		return fmt.Sprintf("http://127.0.0.1:%d", companyProxy.port), companyProxy.key, true
	}
	dir, err := modelProxyDir()
	if err != nil {
		log.Printf("[MODEL_PROXY] cannot prepare its folder: %v", err)
		return "", "", false
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", false
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", false
	}
	key := hex.EncodeToString(secret)
	config, err := modelProxyConfig(dir, port, key)
	if err != nil {
		return "", "", false
	}
	cmd := exec.Command(binary, "--config", config)
	cmd.Dir = dir
	cmd.Env = modelProxyEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logFile, _ := os.OpenFile(filepath.Join(dir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		log.Printf("[MODEL_PROXY] cannot start: %v", err)
		return "", "", false
	}
	go func() { _ = cmd.Wait(); log.Printf("[MODEL_PROXY] proxy exited") }()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	companyProxy.cmd, companyProxy.port, companyProxy.key = cmd, port, key
	log.Printf("[MODEL_PROXY] started on 127.0.0.1:%d", port)
	return base, key, true
}

// modelProxyEnv is a plain environment for the proxy: no server secrets.
func modelProxyEnv() []string {
	env := []string{}
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "LANG", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

var modelProxyCodePattern = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,6}\b`)
var modelProxyURLPattern = regexp.MustCompile(`https://\S+`)

// startModelProxySignIn runs the proxy's own sign-in for a program and returns the link and code to approve.
// The sign-in keeps waiting for approval (up to its own timeout); the running proxy picks up the saved login.
func startModelProxySignIn(provider string) (*modelProxySignIn, error) {
	login, ok := modelProxyLogins[provider]
	binary := modelProxyBinary()
	if !ok || binary == "" {
		return nil, fmt.Errorf("this server cannot lend %s to the Mac app", providerAccountProductName(provider))
	}
	if _, _, ok := companyModelProxyUpstream(); !ok {
		return nil, fmt.Errorf("the model proxy did not start")
	}
	dir, err := modelProxyDir()
	if err != nil {
		return nil, err
	}
	companyProxy.Lock()
	if current := companyProxy.signIns[provider]; current != nil && current.cmd.ProcessState == nil && time.Since(current.Started) < 10*time.Minute {
		companyProxy.Unlock()
		return current, nil
	}
	companyProxy.Unlock()
	cmd := exec.Command(binary, "--config", filepath.Join(dir, "config.yaml"), login.flag, "--no-browser")
	cmd.Dir = dir
	cmd.Env = modelProxyEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	signIn := &modelProxySignIn{Started: time.Now(), cmd: cmd, stdin: stdin, NeedsCallback: login.callback}
	found := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if signIn.URL == "" {
				signIn.URL = strings.TrimRight(modelProxyURLPattern.FindString(line), ".,)")
			}
			if signIn.Code == "" && !login.callback {
				signIn.Code = modelProxyCodePattern.FindString(line)
			}
			if signIn.URL != "" && (signIn.Code != "" || login.callback) {
				select {
				case found <- struct{}{}:
				default:
				}
			}
		}
		_ = cmd.Wait()
		log.Printf("[MODEL_PROXY] %s sign-in ended (signed in: %v)", provider, modelProxySignedInAs(provider) != "")
	}()
	go func() { time.Sleep(10 * time.Minute); _ = cmd.Process.Kill() }()
	select {
	case <-found:
	case <-time.After(20 * time.Second):
		if signIn.URL == "" {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("the sign-in did not start; try again")
		}
	}
	companyProxy.Lock()
	companyProxy.signIns[provider] = signIn
	companyProxy.Unlock()
	log.Printf("[MODEL_PROXY] %s sign-in started", provider)
	return signIn, nil
}

// modelProxyCatalog is a program's model catalog from its provider, fetched with the proxy's login and cached
// for an hour (Muse's full-screen program will not start without it).
func modelProxyCatalog(ctx context.Context, provider string) ([]byte, error) {
	companyProxy.Lock()
	cached := companyProxy.catalog[provider]
	companyProxy.Unlock()
	if cached.body != nil && time.Since(cached.at) < time.Hour {
		return cached.body, nil
	}
	if provider != "muse-cli" {
		return nil, fmt.Errorf("no catalog for %s", provider)
	}
	dir, err := modelProxyDir()
	if err != nil {
		return nil, err
	}
	var login struct {
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	for _, file := range modelProxyLoginFiles(dir, provider) {
		if raw, err := os.ReadFile(file); err == nil && json.Unmarshal(raw, &login) == nil && login.APIKey != "" {
			break
		}
	}
	if login.APIKey == "" {
		return cached.body, fmt.Errorf("the proxy has no Muse login")
	}
	origin := "https://api.meta.ai"
	if base := strings.TrimSuffix(strings.TrimRight(login.BaseURL, "/"), "/v1"); strings.HasPrefix(base, "https://") {
		origin = base
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/muse-code/models", nil)
	if err != nil {
		return cached.body, err
	}
	req.Header.Set("Authorization", "Bearer "+login.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cached.body, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return cached.body, fmt.Errorf("catalog request failed (%d)", resp.StatusCode)
	}
	companyProxy.Lock()
	companyProxy.catalog[provider] = cachedCatalog{body: body, at: time.Now()}
	companyProxy.Unlock()
	return body, nil
}

// modelProxyProgramStatus is one program's card state for admins.
type modelProxyProgramStatus struct {
	Available  bool              `json:"available"`
	SignedInAs string            `json:"signed_in_as,omitempty"`
	SigningIn  *modelProxySignIn `json:"signing_in,omitempty"`
}

// /api/model-proxy/{provider}/mac-app — admins: GET the state of "Use from the Mac app" for the program's
// admin-managed account; POST {"action": "sign_in" | "sign_out"} turns it on (returns the link and code) or off;
// {"action": "finish", "callback_url"} completes a sign-in that ends on the server's own page (Claude).
func (api *StreamingAPI) handleModelProxyMacApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSignedIn(w, r); !ok {
		return
	}
	if !currentUserIsAdmin(r) {
		desktopJSONError(w, 403, "Only an admin can change this.")
		return
	}
	provider := mux.Vars(r)["provider"]
	if _, ok := modelProxyLogins[provider]; !ok || modelProxyBinary() == "" {
		desktopJSON(w, modelProxyProgramStatus{})
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Action string `json:"action"`
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		_ = json.Unmarshal(raw, &input)
		switch input.Action {
		case "sign_in":
			if _, err := startModelProxySignIn(provider); err != nil {
				desktopJSONError(w, 502, err.Error())
				return
			}
		case "sign_out":
			if dir, err := modelProxyDir(); err == nil {
				for _, file := range modelProxyLoginFiles(dir, provider) {
					_ = os.Remove(file)
				}
			}
			log.Printf("[MODEL_PROXY] %s signed out by %s", provider, GetUserFromContext(r.Context()).UserID)
		case "finish":
			// The address the Claude sign-in ended on, typed into the waiting sign-in.
			var finish struct {
				CallbackURL string `json:"callback_url"`
			}
			_ = json.Unmarshal(raw, &finish)
			companyProxy.Lock()
			signIn := companyProxy.signIns[provider]
			companyProxy.Unlock()
			callback := strings.TrimSpace(finish.CallbackURL)
			if signIn == nil || signIn.cmd.ProcessState != nil || !signIn.NeedsCallback {
				desktopJSONError(w, 409, "The sign-in has ended. Turn it on again.")
				return
			}
			if !strings.Contains(callback, "code=") || strings.ContainsAny(callback, "\r\n") {
				desktopJSONError(w, 400, "Paste the whole address from the browser after approving (it contains code=).")
				return
			}
			if _, err := io.WriteString(signIn.stdin, callback+"\n"); err != nil {
				desktopJSONError(w, 409, "The sign-in has ended. Turn it on again.")
				return
			}
			for i := 0; i < 40 && modelProxySignedInAs(provider) == ""; i++ {
				time.Sleep(250 * time.Millisecond)
			}
		default:
			desktopJSONError(w, 400, "Unknown action.")
			return
		}
	}
	status := modelProxyProgramStatus{Available: true, SignedInAs: modelProxySignedInAs(provider)}
	companyProxy.Lock()
	if signIn := companyProxy.signIns[provider]; signIn != nil && signIn.cmd.ProcessState == nil && status.SignedInAs == "" {
		status.SigningIn = signIn
	}
	companyProxy.Unlock()
	desktopJSON(w, status)
}
