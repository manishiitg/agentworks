package browser

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

const browserArtifactStagingDirName = "agentworks-browser-artifacts"
const browserUploadStagingDirName = "agentworks-browser-uploads"
const browserStagingNamespaceEnv = "AGENTWORKS_BROWSER_STAGING_NAMESPACE"

func browserArtifactStagingDir() string {
	// This path crosses the agent server, workspace server, and persistent
	// browser daemon. A deployment-qualified OS temp path avoids process-specific
	// TMPDIR values without colliding with another Unix service account.
	return filepath.Join(string(filepath.Separator), "tmp", browserStagingDirName(browserArtifactStagingDirName))
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

type browserArtifactPlan struct {
	Transfer             *ArtifactTransfer
	RewrittenArgs        []string
	RequestedPath        string
	StagedPath           string
	LeaseKey             string
	FinalizeOnCall       bool
	StoreLeaseOnSuccess  bool
	DeleteLeaseOnSuccess bool
	CleanupOnError       bool
}

type browserArtifactLease struct {
	Transfer      *ArtifactTransfer
	RequestedPath string
}

type browserUploadPlan struct {
	Transfers     []UploadTransfer
	RewrittenArgs []string
	StagedDirs    []string
}

var browserArtifactLeases = struct {
	sync.Mutex
	items map[string]browserArtifactLease
}{items: make(map[string]browserArtifactLease)}

func prepareBrowserArtifact(command string, args []string, ownerID, session string, stagingSession ...string) (*browserArtifactPlan, error) {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "screenshot":
		idx := screenshotOutputIndex(args)
		if idx < 0 {
			return nil, nil
		}
		requested := args[idx]
		staged, err := newBrowserArtifactStagingPath(filepath.Ext(requested), ".png", stagingSession...)
		if err != nil {
			return nil, err
		}
		rewritten := append([]string(nil), args...)
		rewritten[idx] = staged
		return &browserArtifactPlan{
			Transfer: &ArtifactTransfer{
				SourcePath:      staged,
				DestinationPath: requested,
				Kind:            "screenshot",
				Finalize:        true,
			},
			RewrittenArgs:  rewritten,
			RequestedPath:  requested,
			StagedPath:     staged,
			FinalizeOnCall: true,
			CleanupOnError: true,
		}, nil
	case "download":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return nil, nil
		}
		requested := args[1]
		staged, err := newBrowserArtifactStagingPath(".download", ".download", stagingSession...)
		if err != nil {
			return nil, err
		}
		rewritten := append([]string(nil), args...)
		rewritten[1] = staged
		return &browserArtifactPlan{
			Transfer: &ArtifactTransfer{
				SourcePath:      staged,
				DestinationPath: requested,
				Kind:            "download",
				Finalize:        true,
			},
			RewrittenArgs:  rewritten,
			RequestedPath:  requested,
			StagedPath:     staged,
			FinalizeOnCall: true,
			CleanupOnError: true,
		}, nil
	case "record":
		if len(args) == 0 {
			return nil, nil
		}
		action := strings.ToLower(strings.TrimSpace(args[0]))
		key := browserArtifactLeaseKey(ownerID, session, "record")
		switch action {
		case "start", "restart":
			if len(args) < 2 || strings.HasPrefix(args[1], "-") {
				return nil, nil
			}
			requested := args[1]
			staged, err := newBrowserArtifactStagingPath(filepath.Ext(requested), ".webm", stagingSession...)
			if err != nil {
				return nil, err
			}
			rewritten := append([]string(nil), args...)
			rewritten[1] = staged
			return &browserArtifactPlan{
				Transfer: &ArtifactTransfer{
					SourcePath:      staged,
					DestinationPath: requested,
					Kind:            "video",
					Finalize:        false,
				},
				RewrittenArgs:       rewritten,
				RequestedPath:       requested,
				StagedPath:          staged,
				LeaseKey:            key,
				StoreLeaseOnSuccess: true,
				CleanupOnError:      true,
			}, nil
		case "stop":
			if lease, ok := getBrowserArtifactLease(key); ok {
				transfer := *lease.Transfer
				transfer.Finalize = true
				return &browserArtifactPlan{
					Transfer:             &transfer,
					RewrittenArgs:        append([]string(nil), args...),
					RequestedPath:        lease.RequestedPath,
					StagedPath:           transfer.SourcePath,
					LeaseKey:             key,
					FinalizeOnCall:       true,
					DeleteLeaseOnSuccess: true,
				}, nil
			}
		}
	}
	return nil, nil
}

func prepareBrowserUploads(command string, args []string) (*browserUploadPlan, error) {
	if strings.ToLower(strings.TrimSpace(command)) != "upload" {
		return nil, nil
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("upload requires a selector followed by at least one workspace file")
	}

	plan := &browserUploadPlan{RewrittenArgs: append([]string(nil), args...)}
	for i := 1; i < len(args); i++ {
		source := strings.TrimSpace(args[i])
		if source == "" || strings.HasPrefix(source, "-") {
			cleanupBrowserUploadPlan(plan)
			return nil, fmt.Errorf("upload file %d must be a non-empty workspace path", i)
		}
		staged, err := newBrowserUploadStagingPath(source)
		if err != nil {
			cleanupBrowserUploadPlan(plan)
			return nil, err
		}
		plan.Transfers = append(plan.Transfers, UploadTransfer{SourcePath: source, StagedPath: staged})
		plan.RewrittenArgs[i] = staged
		plan.StagedDirs = append(plan.StagedDirs, filepath.Dir(staged))
	}
	return plan, nil
}

func browserUploadStagingDir() string {
	return filepath.Join(string(filepath.Separator), "tmp", browserStagingDirName(browserUploadStagingDirName))
}

func newBrowserUploadStagingPath(source string) (string, error) {
	name := filepath.Base(filepath.Clean(strings.TrimSpace(source)))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("upload source %q does not name a regular file", source)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate browser upload id: %w", err)
	}
	root := browserUploadStagingDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create browser upload staging directory: %w", err)
	}
	dir := filepath.Join(root, hex.EncodeToString(random))
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", fmt.Errorf("create browser upload staging slot: %w", err)
	}
	return filepath.Join(dir, name), nil
}

func cleanupBrowserUploadPlan(plan *browserUploadPlan) {
	if plan == nil {
		return
	}
	for _, dir := range plan.StagedDirs {
		_ = os.RemoveAll(dir)
	}
}

func screenshotOutputIndex(args []string) int {
	valueFlags := map[string]bool{
		"--screenshot-dir":     true,
		"--screenshot-quality": true,
		"--screenshot-format":  true,
		"--selector":           true,
		"-s":                   true,
	}
	positional := make([]int, 0, 2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if valueFlags[arg] {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positional = append(positional, i)
	}
	if len(positional) == 0 {
		return -1
	}
	// One positional is the documented path form. With selector + path, the
	// final positional is the output. Require a screenshot-like extension so a
	// selector-only call is never rewritten as a filesystem destination.
	idx := positional[len(positional)-1]
	ext := strings.ToLower(filepath.Ext(args[idx]))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return -1
	}
	return idx
}

func newBrowserArtifactStagingPath(extension, fallback string, stagingSession ...string) (string, error) {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension == "" {
		extension = fallback
	}
	if extension != ".png" && extension != ".jpg" && extension != ".jpeg" && extension != ".webm" && extension != ".download" {
		return "", fmt.Errorf("unsupported browser artifact extension %q", extension)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate browser artifact id: %w", err)
	}
	dir := browserArtifactStagingDir()
	if len(stagingSession) > 0 {
		dir = browserconfig.ArtifactDirForSession(stagingSession[0])
		if dir == "" {
			return "", fmt.Errorf("browser artifact requires a managed staging session")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create browser artifact staging directory: %w", err)
	}
	return filepath.Join(dir, hex.EncodeToString(random)+extension), nil
}

func browserArtifactLeaseKey(ownerID, session, feature string) string {
	return ownerID + "\x00" + session + "\x00" + feature
}

func setBrowserArtifactLease(key string, lease browserArtifactLease) {
	if key == "" || lease.Transfer == nil {
		return
	}
	browserArtifactLeases.Lock()
	browserArtifactLeases.items[key] = lease
	browserArtifactLeases.Unlock()
}

func getBrowserArtifactLease(key string) (browserArtifactLease, bool) {
	browserArtifactLeases.Lock()
	defer browserArtifactLeases.Unlock()
	lease, ok := browserArtifactLeases.items[key]
	return lease, ok
}

func deleteBrowserArtifactLease(key string) {
	browserArtifactLeases.Lock()
	delete(browserArtifactLeases.items, key)
	browserArtifactLeases.Unlock()
}

func rewriteBrowserArtifactOutput(output string, plan *browserArtifactPlan) string {
	if plan == nil || plan.StagedPath == "" || plan.RequestedPath == "" {
		return output
	}
	return strings.ReplaceAll(output, plan.StagedPath, plan.RequestedPath)
}
