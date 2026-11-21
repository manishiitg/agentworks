// Package noderuntime gives a local AgentWorks install its own Node.js when the
// computer has none. Coding SDKs, Computer use, npx skills and npm-installed
// coding CLIs all need Node; a fresh laptop often lacks it, and asking
// non-technical people to install it (or to manage nvm) is the wrong first step.
//
// The current Node LTS is downloaded from nodejs.org into the app's state
// folder, checked against the release's published SHA-256 list, and its bin
// folder is put first on this process's PATH so every child process finds it.
// A Node already on the computer is always used as it is.
package noderuntime

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// distBase is the Node.js release server; tests point it at a local server.
var distBase = "https://nodejs.org/dist"

// Runtime is one private Node.js folder: <root>/<version>/ with <root>/current
// pointing at the active version.
type Runtime struct {
	root string
	mu   sync.Mutex
}

var (
	defaultMu sync.RWMutex
	defaultRT *Runtime
)

// SetDefault installs the runtime that EnsureDefault uses. Servers leave it
// unset: their Node comes from the host setup.
func SetDefault(rt *Runtime) {
	defaultMu.Lock()
	defaultRT = rt
	defaultMu.Unlock()
}

// EnsureDefault makes node, npm and npx available when a default runtime is
// set; without one it does nothing.
func EnsureDefault(ctx context.Context) error {
	defaultMu.RLock()
	rt := defaultRT
	defaultMu.RUnlock()
	if rt == nil {
		return nil
	}
	return rt.Ensure(ctx)
}

// New returns the runtime kept under root (for example <state>/node).
func New(root string) *Runtime { return &Runtime{root: root} }

func (r *Runtime) binDir() string { return filepath.Join(r.root, "current", "bin") }

// Activate puts an already installed private Node first on PATH. It reports
// whether one was found.
func (r *Runtime) Activate() bool {
	bin := r.binDir()
	if _, err := os.Stat(filepath.Join(bin, "node")); err != nil {
		return false
	}
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == bin {
			return true
		}
	}
	_ = os.Setenv("PATH", bin+string(os.PathListSeparator)+path)
	return true
}

// Ensure leaves an existing Node alone; otherwise it installs the current LTS
// once and activates it.
func (r *Runtime) Ensure(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if available() {
		return nil
	}
	if r.Activate() && available() {
		return nil
	}
	if err := r.install(ctx); err != nil {
		return fmt.Errorf("AgentWorks could not set up Node.js (%v). Check the internet connection and try again, or install Node.js from nodejs.org", err)
	}
	if !r.Activate() || !available() {
		return errors.New("AgentWorks set up Node.js but cannot start it. Install Node.js from nodejs.org and try again")
	}
	return nil
}

func available() bool {
	for _, name := range []string{"node", "npm", "npx"} {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}

func platform() (string, error) {
	osName := map[string]string{"darwin": "darwin", "linux": "linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		return "", fmt.Errorf("no Node.js download for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return osName + "-" + arch, nil
}

func (r *Runtime) install(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	plat, err := platform()
	if err != nil {
		return err
	}
	version, err := latestLTS(ctx)
	if err != nil {
		return err
	}
	name := "node-" + version + "-" + plat
	file := name + ".tar.gz"
	want, err := publishedSHA256(ctx, version, file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	archive, err := os.CreateTemp(r.root, "download-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	sum := sha256.New()
	if err := fetch(ctx, distBase+"/"+version+"/"+file, io.MultiWriter(archive, sum)); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("download of %s did not match its published checksum", file)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(r.root, "extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := extract(archive, name, staging); err != nil {
		return err
	}
	final := filepath.Join(r.root, version)
	_ = os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		return err
	}
	link := filepath.Join(r.root, "current.tmp")
	_ = os.Remove(link)
	if err := os.Symlink(version, link); err != nil {
		return err
	}
	return os.Rename(link, filepath.Join(r.root, "current"))
}

func latestLTS(ctx context.Context) (string, error) {
	var body strings.Builder
	if err := fetch(ctx, distBase+"/index.json", &body); err != nil {
		return "", err
	}
	var releases []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	if err := json.Unmarshal([]byte(body.String()), &releases); err != nil {
		return "", fmt.Errorf("read the Node.js release list: %w", err)
	}
	for _, release := range releases {
		if lts, ok := release.LTS.(string); ok && lts != "" && strings.HasPrefix(release.Version, "v") {
			return release.Version, nil
		}
	}
	return "", errors.New("the Node.js release list has no LTS version")
}

func publishedSHA256(ctx context.Context, version, file string) (string, error) {
	var body strings.Builder
	if err := fetch(ctx, distBase+"/"+version+"/SHASUMS256.txt", &body); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(strings.NewReader(body.String()))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == file && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no published checksum for %s", file)
}

func fetch(ctx context.Context, url string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// extract unpacks the release archive, dropping its top folder (name). Entries
// outside that folder, or links pointing out of the destination, are refused.
func extract(r io.Reader, name, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(header.Name)), name)
		rel = strings.TrimPrefix(rel, "/")
		if !ok || rel == "" {
			if ok && header.Typeflag == tar.TypeDir {
				continue
			}
			return fmt.Errorf("unexpected archive entry %q", header.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if !within(dest, target) {
			return fmt.Errorf("archive entry %q leaves the install folder", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(header.Linkname) || !within(dest, filepath.Join(filepath.Dir(target), header.Linkname)) {
				return fmt.Errorf("archive link %q leaves the install folder", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		default:
			// Release archives hold only folders, files and links.
		}
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
