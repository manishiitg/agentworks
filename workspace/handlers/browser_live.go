package handlers

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

var browserLiveSessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// BrowserLiveStream stays inside the workspace network namespace, where the
// managed headless daemon runs. No externally configured Chrome/CDP is needed.
// Only the agent service may call this route, after checking workflow ownership.
func BrowserLiveStream(c *gin.Context) {
	port, err := browserLivePort(c.Param("session"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Live browser unavailable. Start a browser session with a streaming-capable agent-browser version."})
		return
	}
	if c.GetHeader("X-Browser-Managed") == "true" {
		monitorBrowserTabs(c.Param("session"))
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(r *http.Request) {
		original(r)
		r.URL.Path = "/"
		r.URL.RawPath = ""
		r.URL.RawQuery = ""
		r.Host = target.Host
		r.Header.Del("Authorization")
		r.Header.Del("Cookie")
		r.Header.Del("X-Workspace-Token")
		r.Header.Del("X-Browser-Managed")
		r.Header.Set("Origin", "http://localhost")
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func browserLivePort(session string) (int, error) {
	port, _, err := browserLiveEndpoint(session)
	return port, err
}

func browserLiveEndpoint(session string) (int, string, error) {
	if !browserLiveSessionName.MatchString(session) {
		return 0, "", fmt.Errorf("invalid session")
	}
	for _, dir := range browserSocketDirs(session) {
		data, err := os.ReadFile(filepath.Join(dir, session+".stream"))
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && port > 0 && port <= 65535 {
			return port, dir, nil
		}
	}
	return 0, "", fmt.Errorf("stream metadata unavailable")
}

func browserSocketDirs(session string) []string {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".agent-browser"), filepath.Join(os.TempDir(), "agent-browser"), filepath.Join(os.TempDir(), ".agent-browser"), "/tmp/.agent-browser"}
	// A managed browser keeps its sockets in its own folder.
	if own := browserconfig.SocketDirForSession(session); own != browserconfig.SocketRoot {
		dirs = append([]string{own}, dirs...)
	}
	if own := browserconfig.SandboxSocketDir(session); own != "" {
		dirs = append(dirs, own)
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		dirs = append([]string{filepath.Join(runtimeDir, "agent-browser")}, dirs...)
	}
	if socketDir := os.Getenv("AGENT_BROWSER_SOCKET_DIR"); socketDir != "" {
		dirs = append([]string{socketDir}, dirs...)
	}
	if namespace := strings.TrimSpace(os.Getenv("AGENT_BROWSER_NAMESPACE")); namespace != "" {
		namespaced := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			namespaced = append(namespaced, filepath.Join(dir, "namespaces", namespace, "run"))
		}
		dirs = append(namespaced, dirs...)
	}
	return dirs
}
