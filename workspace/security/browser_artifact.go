package security

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

const BrowserArtifactStagingDirName = "agentworks-browser-artifacts"
const browserStagingNamespaceEnv = "AGENTWORKS_BROWSER_STAGING_NAMESPACE"

// BrowserArtifactStagingDir is the only host directory from which the trusted
// workspace server will finalize browser-generated artifacts.
func BrowserArtifactStagingDir() string {
	return filepath.Join(string(filepath.Separator), "tmp", browserStagingDirName(BrowserArtifactStagingDirName))
}

func browserStagingDirName(base string) string {
	namespace := strings.ToLower(strings.TrimSpace(os.Getenv(browserStagingNamespaceEnv)))
	if namespace == "" {
		return base
	}
	var safe strings.Builder
	for _, char := range namespace {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			safe.WriteRune(char)
		} else if safe.Len() > 0 && !strings.HasSuffix(safe.String(), "-") {
			safe.WriteByte('-')
		}
		if safe.Len() >= 48 {
			break
		}
	}
	qualified := strings.Trim(safe.String(), "-_")
	if qualified == "" {
		return base
	}
	return base + "-" + qualified
}

// Legacy broker outputs retain their old root. Scoped outputs must belong to
// the current request's trusted browser session, never a model-supplied path.
func browserArtifactSourceRoot(source string, browserSession []string) (string, error) {
	parent := filepath.Dir(filepath.Clean(source))
	if parent == filepath.Clean(BrowserArtifactStagingDir()) {
		return parent, nil
	}
	if len(browserSession) > 0 {
		root := browserconfig.ArtifactDirForSession(browserSession[0])
		if root != "" && parent == root {
			if canonicalPath(root) != filepath.Join(canonicalPath(browserconfig.SocketDirForSession(browserSession[0])), "artifacts") {
				return "", fmt.Errorf("browser artifact staging root escapes its browser session")
			}
			return root, nil
		}
	}
	return "", fmt.Errorf("browser artifact source must be in managed staging for this browser session")
}

// PrepareBrowserArtifactStaging creates and validates the one shared staging
// directory before a sandboxed browser command runs. It is used for both
// immediate screenshots and the start half of a start/stop recording lease.
func PrepareBrowserArtifactStaging(sourcePath string, browserSession ...string) error {
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve browser artifact source: %w", err)
	}
	root, err := browserArtifactSourceRoot(source, browserSession)
	if err != nil {
		return err
	}
	if filepath.Dir(filepath.Clean(source)) != root {
		return fmt.Errorf("browser artifact source must be a direct child of managed staging directory")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("create browser artifact staging directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("stat browser artifact staging directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("browser artifact staging root must be a real directory")
	}
	if err := os.Chmod(root, 0700); err != nil {
		return fmt.Errorf("secure browser artifact staging directory: %w", err)
	}
	return nil
}

// FinalizeBrowserArtifact copies one staged browser output into a destination
// explicitly authorized by the current request's write guard. The copy runs in
// the trusted workspace server because a persistent browser daemon cannot
// safely inherit a different sandbox for every workflow step.
func FinalizeBrowserArtifact(sourcePath, destinationPath, kind, baseDir string, writePaths, blockedPaths, blockedWritePaths []string, browserSession ...string) error {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(destinationPath) == "" {
		return fmt.Errorf("browser artifact source and destination are required")
	}
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve browser artifact source: %w", err)
	}
	root, err := browserArtifactSourceRoot(source, browserSession)
	if err != nil {
		return err
	}
	stagingRoot := canonicalPath(root)
	canonicalSource := canonicalPath(source)
	if !pathWithin(canonicalSource, stagingRoot) || canonicalSource == stagingRoot {
		return fmt.Errorf("browser artifact source must be under managed staging directory")
	}
	info, err := os.Lstat(canonicalSource)
	if err != nil {
		return fmt.Errorf("stat staged browser artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged browser artifact must be a regular file")
	}
	if info.Size() <= 0 {
		return fmt.Errorf("staged browser artifact is empty")
	}

	base := canonicalPath(baseDir)
	destination := destinationPath
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(base, destination)
	}
	destination = canonicalPath(filepath.Clean(destination))
	if !pathWithin(destination, base) {
		return fmt.Errorf("browser artifact destination must be inside workspace")
	}
	if !pathAllowedForArtifactWrite(destination, base, writePaths) {
		return fmt.Errorf("browser artifact destination is not covered by this session's write paths")
	}
	if pathCoveredByGuard(destination, base, blockedPaths) || pathCoveredByGuard(destination, base, blockedWritePaths) {
		return fmt.Errorf("browser artifact destination is blocked for writes")
	}

	parent := filepath.Dir(destination)
	if err := mkdirLikeAncestor(parent); err != nil {
		return fmt.Errorf("create browser artifact destination directory: %w", err)
	}
	canonicalParent := canonicalPath(parent)
	if !pathWithin(canonicalParent, base) || !pathAllowedForArtifactWrite(canonicalParent, base, writePaths) {
		return fmt.Errorf("browser artifact destination resolves outside authorized write paths")
	}

	src, err := os.Open(canonicalSource)
	if err != nil {
		return fmt.Errorf("open staged browser artifact: %w", err)
	}
	defer src.Close()
	openedInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat opened browser artifact: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("staged browser artifact changed before it could be finalized")
	}
	if err := validateBrowserArtifact(src, kind); err != nil {
		return err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind staged browser artifact: %w", err)
	}

	tmp, err := os.CreateTemp(parent, ".agentworks-browser-artifact-*")
	if err != nil {
		return fmt.Errorf("create browser artifact destination temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return fmt.Errorf("copy browser artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync browser artifact: %w", err)
	}
	// CreateTemp makes the file 0600 for this server's account. A slot user's
	// chat could then not read its own recording (RTS 2026-10-07, PLAT-709), so
	// the file gets the access its folder gives.
	if parentInfo, err := os.Stat(parent); err == nil {
		if err := tmp.Chmod(fileModeForDir(parentInfo.Mode())); err != nil {
			return fmt.Errorf("set browser artifact permissions: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close browser artifact: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return fmt.Errorf("publish browser artifact: %w", err)
	}
	removeTmp = false
	_ = os.Remove(canonicalSource)
	return nil
}

// fileModeForDir gives a file the read and write access its folder gives each
// class: a 2770 project folder makes 0660, a 0755 folder makes 0644.
func fileModeForDir(dir os.FileMode) os.FileMode {
	mode := os.FileMode(0o600)
	if dir&0o040 != 0 {
		mode |= 0o040
	}
	if dir&0o020 != 0 {
		mode |= 0o020
	}
	if dir&0o004 != 0 {
		mode |= 0o004
	}
	return mode
}

// mkdirLikeAncestor creates missing folders with the permissions (including
// setgid) of the nearest existing one, so a folder made by this server inside a
// slot user's project stays writable for that user's group.
func mkdirLikeAncestor(dir string) error {
	var missing []string
	existing := dir
	for {
		info, err := os.Stat(existing)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", existing)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, existing)
		next := filepath.Dir(existing)
		if next == existing {
			break
		}
		existing = next
	}
	if len(missing) == 0 {
		return nil
	}
	mode := os.FileMode(0o755)
	if info, err := os.Stat(existing); err == nil {
		mode = info.Mode() & (os.ModePerm | os.ModeSetgid)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Chmod(missing[i], mode); err != nil {
			return err
		}
	}
	return nil
}

func pathAllowedForArtifactWrite(path, base string, writePaths []string) bool {
	for _, allowed := range writePaths {
		root := allowed
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		root = canonicalPath(root)
		if pathWithin(path, root) {
			return true
		}
	}
	return false
}

func pathCoveredByGuard(path, base string, guarded []string) bool {
	for _, value := range guarded {
		root := value
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		if pathWithin(path, canonicalPath(root)) {
			return true
		}
	}
	return false
}

func validateBrowserArtifact(file *os.File, kind string) error {
	header := make([]byte, 16)
	n, err := file.Read(header)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read staged browser artifact header: %w", err)
	}
	header = header[:n]
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "screenshot":
		isPNG := len(header) >= 8 && bytes.Equal(header[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
		isJPEG := len(header) >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff
		if !isPNG && !isJPEG {
			return fmt.Errorf("staged screenshot is not a PNG or JPEG image")
		}
	case "video":
		if len(header) < 4 || !bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
			return fmt.Errorf("staged video is not a WebM/EBML file")
		}
	case "download":
		// Downloads are intentionally format-agnostic. The caller requested this
		// web response explicitly; the surrounding checks still require a regular,
		// non-empty staged file and an authorized workspace destination.
	default:
		return fmt.Errorf("unsupported browser artifact kind %q", kind)
	}
	return nil
}
