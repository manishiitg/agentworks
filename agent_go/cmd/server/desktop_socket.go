package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Only the split local launcher enables this socket. Packaged/full desktop
// launches keep their private fd3 bootstrap. No credential is written to disk.
var desktopSocketKey []byte

func desktopLoopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && u.Scheme == "http" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		(u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
}

func desktopSocketProof(origin string) string {
	if len(desktopSocketKey) == 0 || !desktopLoopbackOrigin(origin) {
		return ""
	}
	// Bind the proof to the actual Vite origin without registering mutable ports.
	mac := hmac.New(sha256.New, desktopSocketKey)
	mac.Write([]byte(origin))
	return hex.EncodeToString(mac.Sum(nil))
}

func startDesktopSocket(address *net.TCPAddr) (net.Listener, error) {
	path := os.Getenv("AGENTWORKS_DESKTOP_SOCKET")
	if path == "" || desktopStartup.Secret != "" {
		return nil, nil
	}
	if !address.IP.IsLoopback() || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("desktop socket requires a loopback backend and an absolute socket path")
	}
	apiOrigin := "http://" + address.String()
	if configured := os.Getenv("MCP_AGENT_SERVER_URL"); configured != "" {
		parsed, err := url.Parse(configured)
		if err != nil || !desktopLoopbackOrigin(configured) || parsed.Port() != strconv.Itoa(address.Port) {
			return nil, fmt.Errorf("desktop API origin must name this loopback backend port")
		}
		apiOrigin = configured
	}
	apiURL, _ := url.Parse(apiOrigin)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !desktopSocketOwnedByUser(info) {
		return nil, fmt.Errorf("desktop socket directory must belong to this user with mode 0700")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 || !desktopSocketOwnedByUser(info) {
			return nil, fmt.Errorf("desktop socket path is not this user's socket")
		}
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("desktop socket is already in use")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot verify desktop socket is stale: %w", dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		listener.Close()
		return nil, err
	}
	desktopSocketKey = key
	desktopBootstrapHost = apiURL.Host
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				var request struct {
					AppOrigin string `json:"app_origin"`
				}
				if json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&request) != nil {
					return
				}
				proof := desktopSocketProof(request.AppOrigin)
				if proof != "" {
					json.NewEncoder(conn).Encode(map[string]string{"secret": proof, "api_origin": apiOrigin, "app_origin": request.AppOrigin})
				}
			}()
		}
	}()
	return listener, nil
}
