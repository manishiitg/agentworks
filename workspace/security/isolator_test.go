package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestEnvironment holds test fixtures
type TestEnvironment struct {
	TempDir      string
	ReadOnlyDir  string
	ReadWriteDir string
	ForbiddenDir string
	DownloadsDir string
	CleanupFuncs []func()
	t            *testing.T
}

// Setup creates test directories and files
func (te *TestEnvironment) Setup(t *testing.T) error {
	te.t = t

	// Create temp directory for tests
	tempDir, err := os.MkdirTemp("", "folder-guard-test-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	te.TempDir = tempDir
	te.CleanupFuncs = append(te.CleanupFuncs, func() {
		os.RemoveAll(tempDir)
	})

	// Create test directories
	te.ReadOnlyDir = filepath.Join(tempDir, "readonly")
	te.ReadWriteDir = filepath.Join(tempDir, "readwrite")
	te.ForbiddenDir = filepath.Join(tempDir, "forbidden")
	te.DownloadsDir = filepath.Join(tempDir, "Downloads")

	for _, dir := range []string{te.ReadOnlyDir, te.ReadWriteDir, te.ForbiddenDir, te.DownloadsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create test dir %s: %w", dir, err)
		}
	}

	// Create test files
	testFiles := map[string]string{
		filepath.Join(te.ReadOnlyDir, "public.txt"):    "public data",
		filepath.Join(te.ReadWriteDir, "data.txt"):     "writable data",
		filepath.Join(te.ForbiddenDir, "secret.txt"):   "secret data",
		filepath.Join(te.DownloadsDir, "download.txt"): "download data",
	}

	for path, content := range testFiles {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write test file %s: %w", path, err)
		}
	}

	t.Logf("Test environment created at: %s", tempDir)
	return nil
}

// Cleanup removes all test resources
func (te *TestEnvironment) Cleanup() {
	for _, cleanup := range te.CleanupFuncs {
		cleanup()
	}
}

// TestIsolatorOSDetection tests that the right isolation method is chosen
func TestIsolatorOSDetection(t *testing.T) {
	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	isolator := &Isolator{
		ReadPaths:  []string{env.ReadOnlyDir},
		WritePaths: []string{env.ReadWriteDir},
		WorkDir:    env.TempDir,
		BaseDir:    env.TempDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "echo", []string{"test"})
	if cleanup != nil {
		defer cleanup()
	}

	if err != nil {
		t.Fatalf("ExecuteIsolated failed: %v", err)
	}

	// Verify the right command is used based on OS
	if runtime.GOOS == "darwin" {
		if cmd.Args[0] != "sandbox-exec" {
			t.Errorf("Expected sandbox-exec on macOS, got: %s", cmd.Args[0])
		}
		t.Logf("✓ macOS: Using sandbox-exec")
	} else if runtime.GOOS == "linux" {
		backend := filepath.Base(cmd.Args[0])
		if backend != "unshare" && backend != landlockRunnerName {
			t.Errorf("Expected a supported Linux sandbox launcher, got: %s", cmd.Args[0])
		}
		t.Logf("✓ Linux: Using %s", backend)
	}

	// Verify safe environment is set
	if len(cmd.Env) == 0 {
		t.Error("Expected safe environment to be set, but Env is empty")
	}

	hasPath := false
	for _, envVar := range cmd.Env {
		if strings.HasPrefix(envVar, "PATH=") {
			hasPath = true
		}
		// Check for leaked secrets
		if strings.Contains(strings.ToUpper(envVar), "DATABASE") ||
			strings.Contains(strings.ToUpper(envVar), "SECRET") ||
			strings.Contains(strings.ToUpper(envVar), "API_KEY") {
			t.Errorf("SECURITY VIOLATION: Found secret in environment: %s", envVar)
		}
	}
	if !hasPath {
		t.Error("Safe environment missing PATH variable")
	}
	t.Logf("✓ Safe environment configured with %d variables", len(cmd.Env))
}

// TestMacOSSandboxProfile tests the macOS sandbox profile generation
func TestMacOSSandboxProfile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	isolator := &Isolator{
		ReadPaths:  []string{env.ReadOnlyDir},
		WritePaths: []string{env.ReadWriteDir},
		WorkDir:    env.TempDir,
		BaseDir:    env.TempDir,
	}

	profile := isolator.generateSandboxProfile()

	// Verify profile structure
	if !strings.Contains(profile, "(version 1)") {
		t.Error("Sandbox profile missing version declaration")
	}
	if !strings.Contains(profile, "(allow default)") {
		t.Error("Sandbox profile missing default allow")
	}
	// CRITICAL: Should deny the project root (source code, configs) for security
	if !strings.Contains(profile, "(deny file-read* file-write*") {
		t.Error("Sandbox profile missing project root denial")
	}
	canonicalProjectRoot := canonicalPath(filepath.Dir(env.TempDir))
	projectRootMetadataGrant := fmt.Sprintf("(allow file-read-metadata (literal \"%s\"))", canonicalProjectRoot)
	if !strings.Contains(profile, projectRootMetadataGrant) {
		t.Error("sandbox profile must allow project-root metadata for canonical path resolution")
	}
	if strings.Contains(profile, fmt.Sprintf("(allow file-read* (subpath \"%s\"))", canonicalProjectRoot)) {
		t.Error("sandbox profile must not grant project-root file reads")
	}

	// Verify paths are included
	canonicalReadOnly := canonicalPath(env.ReadOnlyDir)
	if !strings.Contains(profile, canonicalReadOnly) {
		t.Errorf("Read-only path not in profile: %s", canonicalReadOnly)
	}
	canonicalReadWrite := canonicalPath(env.ReadWriteDir)
	if !strings.Contains(profile, canonicalReadWrite) {
		t.Errorf("Read-write path not in profile: %s", canonicalReadWrite)
	}

	canonicalWorkDir := canonicalPath(env.TempDir)
	if strings.Contains(profile, fmt.Sprintf("(allow file-read* (subpath \"%s\"))", canonicalWorkDir)) {
		t.Error("sandbox profile grants broad read access to the working directory")
	}
	workDirLiteralGrant := fmt.Sprintf("(allow file-read* (literal \"%s\"))", canonicalWorkDir)
	if count := strings.Count(profile, workDirLiteralGrant); count != 1 {
		t.Errorf("sandbox profile must grant the working-directory literal exactly once for getcwd; count=%d", count)
	}

	t.Logf("✓ Sandbox profile generated correctly:\n%s", profile)
}

// TestEnvironmentIsolation tests that BuildSafeEnvironment is applied
func TestEnvironmentIsolation(t *testing.T) {
	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	// Set a secret environment variable in the parent process
	os.Setenv("DATABASE_URL", "postgresql://secret")
	os.Setenv("API_KEY", "super-secret-key")
	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("API_KEY")
	}()

	isolator := &Isolator{
		ReadPaths:  []string{},
		WritePaths: []string{env.TempDir},
		WorkDir:    env.TempDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Execute command that prints environment
	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", "env"})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated failed: %v", err)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Command may fail due to isolation, but we can still check output
		t.Logf("Command exited with error (may be expected): %v", err)
	}

	outputStr := string(output)
	t.Logf("Environment output:\n%s", outputStr)

	// CRITICAL: Verify secrets are NOT leaked
	if strings.Contains(outputStr, "DATABASE_URL") {
		t.Error("SECURITY VIOLATION: DATABASE_URL leaked to subprocess")
	}
	if strings.Contains(outputStr, "API_KEY") {
		t.Error("SECURITY VIOLATION: API_KEY leaked to subprocess")
	}
	if strings.Contains(outputStr, "super-secret-key") {
		t.Error("SECURITY VIOLATION: Secret value leaked to subprocess")
	}

	// Verify safe environment is present
	if !strings.Contains(outputStr, "PATH=") {
		t.Error("Safe environment missing PATH")
	}

	t.Log("✓ Environment isolation working correctly")
}

// TestLinuxMountIsolation tests Linux-specific mount namespace isolation
func TestLinuxMountIsolation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping Linux-specific test")
	}

	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	// Check if unshare is available and we have permissions
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare not available")
	}

	// Test that unshare works (requires SYS_ADMIN capability in Docker)
	testCmd := exec.Command("unshare", "-m", "echo", "test")
	if err := testCmd.Run(); err != nil {
		t.Skip("unshare requires SYS_ADMIN capability (add --cap-add=SYS_ADMIN to Docker)")
	}

	isolator := &Isolator{
		ReadPaths:  []string{env.ReadOnlyDir},
		WritePaths: []string{env.ReadWriteDir},
		WorkDir:    env.TempDir,
	}

	// Test reading from read-only path
	t.Run("ReadFromReadOnlyPath", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", fmt.Sprintf("cat %s/public.txt", env.ReadOnlyDir)})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Failed to read from read-only path: %v\nOutput: %s", err, output)
		}

		if !strings.Contains(string(output), "public data") {
			t.Errorf("Expected to read 'public data', got: %s", output)
		}
		t.Log("✓ Read from read-only path succeeded")
	})

	// Test writing to read-write path
	t.Run("WriteToReadWritePath", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		testFile := filepath.Join(env.ReadWriteDir, "test_write.txt")
		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", fmt.Sprintf("echo 'test' > %s", testFile)})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Failed to write to read-write path: %v\nOutput: %s", err, output)
		}

		// Verify file was created
		if _, err := os.Stat(testFile); os.IsNotExist(err) {
			t.Error("File was not created in read-write path")
		} else {
			t.Log("✓ Write to read-write path succeeded")
		}
	})

	// Test that writing to read-only path fails
	t.Run("WriteToReadOnlyPath", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", fmt.Sprintf("echo 'test' > %s/test_write.txt", env.ReadOnlyDir)})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}

		output, err := cmd.CombinedOutput()
		// Should fail
		if err == nil {
			t.Errorf("SECURITY VIOLATION: Write to read-only path succeeded!\nOutput: %s", output)
		} else {
			t.Logf("✓ Write to read-only path correctly blocked: %v", err)
		}
	})
}

// TestMacOSSandboxIsolation tests macOS sandbox-exec isolation
func TestMacOSSandboxIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	// Override base dir for macOS testing (use temp dir instead of /app/workspace-docs)
	// We'll need to modify the isolator to accept base dir as parameter
	// For now, we test that the mechanism works

	isolator := &Isolator{
		ReadPaths:  []string{env.ReadOnlyDir},
		WritePaths: []string{env.ReadWriteDir},
		WorkDir:    env.TempDir,
	}

	// Test basic command execution
	t.Run("BasicExecution", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "echo", []string{"test"})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Command execution failed: %v\nOutput: %s", err, output)
		}

		if !strings.Contains(string(output), "test") {
			t.Errorf("Expected output 'test', got: %s", output)
		}
		t.Log("✓ Basic command execution works with sandbox-exec")
	})

	// Test file operations
	t.Run("FileOperations", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Test reading
		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", fmt.Sprintf("cat %s/data.txt", env.ReadWriteDir)})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Read test output: %s", output)
		}

		t.Log("✓ File operations completed with sandbox-exec")
	})
}

// TestDownloadsRequiresExplicitPermission ensures a shared folder name does not
// bypass the same allow-list applied to every other workspace path.
func TestDownloadsRequiresExplicitPermission(t *testing.T) {
	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	// Note: This test checks the profile/script generation
	// Actual enforcement requires /app/workspace-docs structure

	isolator := &Isolator{
		WorkDir: env.TempDir,
		BaseDir: env.TempDir,
	}

	if runtime.GOOS == "darwin" {
		profile := isolator.generateSandboxProfile()
		if strings.Contains(profile, canonicalPath(env.DownloadsDir)) {
			t.Error("Downloads must not be implicitly allowed in macOS sandbox profile")
		}
		isolator.ReadPaths = []string{env.DownloadsDir}
		if !strings.Contains(isolator.generateSandboxProfile(), canonicalPath(env.DownloadsDir)) {
			t.Error("explicit Downloads read permission missing from macOS sandbox profile")
		}
	} else if runtime.GOOS == "linux" {
		script := isolator.generateMountScript("echo", []string{"test"})
		if strings.Contains(script, env.DownloadsDir) {
			t.Error("Downloads must not be implicitly allowed in Linux mount script")
		}
		isolator.ReadPaths = []string{env.DownloadsDir}
		if !strings.Contains(isolator.generateMountScript("echo", []string{"test"}), env.DownloadsDir) {
			t.Error("explicit Downloads read permission missing from Linux mount script")
		}
	}
}

func TestMountScriptBindsExternalAbsolutePathsFromHost(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	inside := filepath.Join(root, "Workflow", "example")
	iso := &Isolator{
		BaseDir:    root,
		WorkDir:    inside,
		ReadPaths:  []string{inside},
		WritePaths: []string{inside, external},
	}

	tempPath, target := resolveTempAndAbsPath(external, root, "/tmp/workspace-original-$$")
	if tempPath != external || target != external {
		t.Fatalf("external bind = (%q, %q), want host path %q", tempPath, target, external)
	}
	script := iso.generateMountScript("true", nil)
	if !strings.Contains(script, fmt.Sprintf(`mount --bind "%s" "%s"`, external, external)) {
		t.Fatalf("external path must bind from host: %s", script)
	}
	if strings.Contains(script, filepath.Join("/tmp/workspace-original-$$", strings.TrimPrefix(external, "/"))) {
		t.Fatal("external path was incorrectly sourced from the workspace copy")
	}
	if strings.Contains(script, fmt.Sprintf(`mkdir -p "%s"`, external)) {
		t.Fatal("mount script must not create an external grant")
	}
	internalSource, _ := resolveTempAndAbsPath(inside, root, "/tmp/workspace-original-$$")
	if internalSource != filepath.Join("/tmp/workspace-original-$$", "Workflow", "example") {
		t.Fatalf("workspace bind source changed: %q", internalSource)
	}
}

// TestMacOSAuthorizedSiblingDirectory verifies the complete permission path
// used by Builder attachments: an allowed directory below workspace-docs must
// permit access to the directory node as well as its descendants. A subpath-
// only rule permits existing child files but macOS still rejects listing the
// directory and creating a new child with "Operation not permitted".
func TestMacOSAuthorizedSiblingDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}

	projectRoot := t.TempDir()
	baseDir := filepath.Join(projectRoot, "workspace-docs")
	workDir := filepath.Join(baseDir, "Workflow", "jobsearch")
	downloadsDir := filepath.Join(baseDir, "Downloads")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		t.Fatalf("mkdir Downloads: %v", err)
	}

	isolator := &Isolator{
		ReadPaths:  []string{"Workflow/jobsearch", "Downloads"},
		WritePaths: []string{"Workflow/jobsearch", "Downloads"},
		WorkDir:    workDir,
		BaseDir:    baseDir,
	}

	canonicalDownloads := canonicalPath(downloadsDir)
	profile := isolator.generateSandboxProfile()
	if !strings.Contains(profile, fmt.Sprintf("(literal \"%s\")", sandboxQuoted(canonicalDownloads))) {
		t.Fatalf("authorized directory node is missing its literal grant:\n%s", profile)
	}
	if !strings.Contains(profile, fmt.Sprintf("(subpath \"%s\")", sandboxQuoted(canonicalDownloads))) {
		t.Fatalf("authorized directory descendants are missing their subpath grant:\n%s", profile)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listCmd, listCleanup, err := isolator.ExecuteIsolated(ctx, "ls", []string{downloadsDir})
	if listCleanup != nil {
		defer listCleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated list failed: %v", err)
	}
	if output, err := listCmd.CombinedOutput(); err != nil {
		t.Fatalf("authorized Downloads list failed: %v\nOutput: %s", err, output)
	}

	testFile := filepath.Join(downloadsDir, "builder-download-test.txt")
	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "touch", []string{testFile})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("ExecuteIsolated write failed: %v", err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("authorized Downloads write failed: %v\nOutput: %s", err, output)
	}
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("authorized file was not created: %v", err)
	}
}

func TestSandboxAllowPathsRejectWorkspaceSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "workspace-docs")
	outsideDir := filepath.Join(root, "repo-secrets")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("mkdir outside target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(baseDir, "escaped-read")); err != nil {
		t.Fatalf("create read symlink: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(baseDir, "escaped-write")); err != nil {
		t.Fatalf("create write symlink: %v", err)
	}

	iso := &Isolator{
		ReadPaths:  []string{"escaped-read"},
		WritePaths: []string{filepath.Join(baseDir, "escaped-write")},
		WorkDir:    baseDir,
		BaseDir:    baseDir,
	}

	profile := iso.generateSandboxProfile()
	canonicalOutside := canonicalPath(outsideDir)
	if strings.Contains(profile, fmt.Sprintf("(subpath \"%s\")", canonicalOutside)) {
		t.Fatalf("workspace symlink escape was re-allowed in sandbox profile:\n%s", profile)
	}

	if path, ok := iso.sandboxAllowedPath("escaped-read"); ok || path != "" {
		t.Fatalf("relative workspace symlink escape accepted: path=%q ok=%v", path, ok)
	}
	if path, ok := iso.sandboxAllowedPath(filepath.Join(baseDir, "escaped-write")); ok || path != "" {
		t.Fatalf("absolute workspace symlink escape accepted: path=%q ok=%v", path, ok)
	}

	if runtime.GOOS == "darwin" {
		t.Run("RealSandboxBlocksEscapedRead", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd, cleanup, err := iso.ExecuteIsolated(ctx, "cat", []string{filepath.Join(baseDir, "escaped-read", "secret.txt")})
			if cleanup != nil {
				defer cleanup()
			}
			if err != nil {
				t.Fatalf("prepare isolated read: %v", err)
			}
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("sandbox read escaped through workspace symlink: %s", output)
			}
		})

		t.Run("RealSandboxBlocksEscapedWrite", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			createdPath := filepath.Join(baseDir, "escaped-write", "created.txt")
			cmd, cleanup, err := iso.ExecuteIsolated(ctx, "touch", []string{createdPath})
			if cleanup != nil {
				defer cleanup()
			}
			if err != nil {
				t.Fatalf("prepare isolated write: %v", err)
			}
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("sandbox write escaped through workspace symlink: %s", output)
			}
			if _, err := os.Stat(filepath.Join(outsideDir, "created.txt")); !os.IsNotExist(err) {
				t.Fatalf("escaped write created outside file: %v", err)
			}
		})
	}
}

func TestSandboxAllowPathsPreserveExplicitExternalGrant(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "workspace-docs")
	externalDir := filepath.Join(t.TempDir(), "Downloads")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(externalDir, 0755); err != nil {
		t.Fatalf("mkdir external grant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(externalDir, "profile.json"), []byte(`{"external":true}`), 0644); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	iso := &Isolator{
		BaseDir:           baseDir,
		WorkDir:           filepath.Dir(root),
		ReadPaths:         []string{externalDir},
		BlockedWritePaths: []string{externalDir},
	}
	allowedPath, ok := iso.sandboxAllowedPath(externalDir)
	if !ok {
		t.Fatal("explicit external absolute grant was rejected")
	}
	if allowedPath != canonicalPath(externalDir) {
		t.Fatalf("external grant canonicalized incorrectly: got %q want %q", allowedPath, canonicalPath(externalDir))
	}
	if !strings.Contains(iso.generateSandboxProfile(), fmt.Sprintf("(subpath \"%s\")", sandboxQuoted(allowedPath))) {
		t.Fatal("explicit external absolute grant missing from sandbox profile")
	}
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd, cleanup, err := iso.ExecuteIsolated(ctx, "cat", []string{filepath.Join(externalDir, "profile.json")})
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("prepare isolated external read: %v", err)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("read explicitly granted external file: %v\n%s", err, output)
		}
		if string(output) != `{"external":true}` {
			t.Fatalf("unexpected external content: %q", output)
		}

		writePath := filepath.Join(externalDir, "must-not-write.txt")
		writeCmd, writeCleanup, err := iso.ExecuteIsolated(ctx, "touch", []string{writePath})
		if writeCleanup != nil {
			defer writeCleanup()
		}
		if err != nil {
			t.Fatalf("prepare isolated external write: %v", err)
		}
		if output, err := writeCmd.CombinedOutput(); err == nil {
			t.Fatalf("write to read-only external grant succeeded: %s", output)
		}
		if _, err := os.Stat(writePath); !os.IsNotExist(err) {
			t.Fatalf("read-only external grant created a file: %v", err)
		}
	}
}

// TestDenyListSymlinkFixup tests that Mode 1 (deny-list) preserves symlinks
// pointing into blocked paths by generating fixup mount/allow commands.
func TestDenyListSymlinkFixup(t *testing.T) {
	// Create a temp directory simulating /app/workspace-docs
	tempDir, err := os.MkdirTemp("", "isolator-symlink-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create shared folders at root level
	for _, folder := range []string{"Chats", "Downloads", "skills"} {
		if err := os.MkdirAll(filepath.Join(tempDir, folder), 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, folder, "test.txt"), []byte("data"), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	// Create a blocked directory
	blockedDir := filepath.Join(tempDir, "secret")
	if err := os.MkdirAll(blockedDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}

	isolator := &Isolator{
		BlockedPaths: []string{"secret"},
		WorkDir:      tempDir,
		BaseDir:      tempDir,
	}

	t.Run("LinuxMountScriptBlocksPaths", func(t *testing.T) {
		script := isolator.generateMountScript("ls Chats/", nil)

		// Should hide blocked path with tmpfs
		if !strings.Contains(script, "mount -t tmpfs tmpfs") {
			t.Error("Mount script should hide blocked path with tmpfs")
		}

		t.Logf("✓ Linux mount script correctly blocks paths:\n%s", script)
	})

	t.Run("MacOSSandboxBlocksPaths", func(t *testing.T) {
		profile := isolator.generateSandboxProfile()

		// Should deny blocked paths
		if !strings.Contains(profile, "deny file-read* file-write*") {
			t.Error("Sandbox profile should deny access to blocked paths")
		}

		t.Logf("✓ macOS sandbox profile blocks paths:\n%s", profile)
	})
}

// TestBlockedWritePathsAllowsReads verifies the write-only deny primitive:
// reads to paths under BlockedWritePaths pass through, writes are denied. This
// is the semantic that lets the chat-agent #workflow setup grant
// Workflow/<name>/ as a broad write prefix while keeping planning/ readable but
// not writable. Distinct from BlockedPaths which denies both reads and writes.
func TestBlockedWritePathsAllowsReads(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "isolator-blocked-write-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Workflow/test-ops/{reports,planning}/ with a file in planning so the
	// isolator has something to bind-mount read-only on Linux.
	workflowRoot := filepath.Join(tempDir, "Workflow", "test-ops")
	if err := os.MkdirAll(filepath.Join(workflowRoot, "planning"), 0755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workflowRoot, "reports"), 0755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowRoot, "planning", "plan.json"), []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("write plan.json: %v", err)
	}

	iso := &Isolator{
		WritePaths:        []string{"Workflow/test-ops"},
		BlockedWritePaths: []string{"Workflow/test-ops/planning"},
		WorkDir:           tempDir,
		BaseDir:           tempDir,
	}

	t.Run("MacOSProfileEmitsWriteOnlyDenyAndPreservesReads", func(t *testing.T) {
		profile := iso.generateSandboxProfile()

		// The profile has an unrelated `(deny file-read* file-write*` for the
		// project-root (source code / configs) — that's a different concern.
		// Narrow our assertion to the BlockedWritePaths section specifically.
		const marker = "; Explicit deny for write-only blocked paths"
		start := strings.Index(profile, marker)
		if start < 0 {
			t.Fatalf("profile missing BlockedWritePaths section (marker %q); got:\n%s", marker, profile)
		}
		// Take the block from marker to end of profile (it's appended last).
		writeOnlySection := profile[start:]

		if !strings.Contains(writeOnlySection, "(deny file-write*") {
			t.Errorf("BlockedWritePaths section should emit `(deny file-write*`; got:\n%s", writeOnlySection)
		}
		// The read-deny form MUST NOT appear within the BlockedWritePaths block.
		// If it does, reads to planning/ would be denied and the agent can't cat plan.json.
		if strings.Contains(writeOnlySection, "file-read*") {
			t.Errorf("BlockedWritePaths section must NOT deny reads; got:\n%s", writeOnlySection)
		}
		// The blocked path must be referenced under this block.
		if !strings.Contains(writeOnlySection, "Workflow/test-ops/planning") {
			t.Errorf("BlockedWritePaths section should reference the write-blocked subpath; got:\n%s", writeOnlySection)
		}
	})

	t.Run("LinuxMountScriptBindMountsReadOnlyOverWritableParent", func(t *testing.T) {
		script := iso.generateMountScript("cat Workflow/test-ops/planning/plan.json", nil)

		// The WritePath should be bound read-write.
		if !strings.Contains(script, `mount --bind "`) {
			t.Errorf("mount script should bind-mount WritePaths; got:\n%s", script)
		}
		// The BlockedWritePath should be overlaid as a read-only bind AFTER the rw bind.
		// Assert that the ro bind for Workflow/test-ops/planning appears in the script.
		wantRO := `mount --bind -o ro`
		if !strings.Contains(script, wantRO) {
			t.Errorf("mount script should overlay BlockedWritePaths with `%s`; got:\n%s", wantRO, script)
		}
		if !strings.Contains(script, "Workflow/test-ops/planning") {
			t.Errorf("mount script should reference the write-blocked subpath; got:\n%s", script)
		}
		// Ordering check: the rw bind for the parent must be emitted before the ro
		// overlay for the child, otherwise the rw bind would overwrite the ro one.
		rwIdx := strings.Index(script, `mount --bind "`)
		roIdx := strings.Index(script, wantRO)
		if rwIdx < 0 || roIdx < 0 || roIdx < rwIdx {
			t.Errorf("ro bind-mount must come AFTER the rw bind-mount so the ro layer wins; rwIdx=%d roIdx=%d", rwIdx, roIdx)
		}
	})
}

// BenchmarkIsolatorOverhead measures the performance overhead of isolation
func BenchmarkIsolatorOverhead(b *testing.B) {
	env := &TestEnvironment{}
	if err := env.Setup(&testing.T{}); err != nil {
		b.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	isolator := &Isolator{
		ReadPaths:  []string{env.ReadOnlyDir},
		WritePaths: []string{env.ReadWriteDir},
		WorkDir:    env.TempDir,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "echo", []string{"test"})
		if err != nil {
			b.Fatalf("ExecuteIsolated failed: %v", err)
		}

		if err := cmd.Run(); err != nil {
			b.Logf("Command failed: %v", err)
		}

		if cleanup != nil {
			cleanup()
		}
		cancel()
	}
}

// TestMountNamespaceCommandUsesUnprivilegedUserNamespace pins the exact
// privilege shape executeIsolatedMountNamespace requests. A plain "unshare
// -m" (mount namespace only) needs CAP_SYS_ADMIN in the CURRENT user
// namespace, which an unprivileged service account never has -- it fails
// with EPERM regardless of Landlock/AppArmor state, making the whole
// fallback permanently unusable for every rootless deployment. Confirmed
// live on the Dominion Hetzner deployment 2026-08-29: identical "-m" alone
// failed with "Operation not permitted" as the unprivileged service user,
// while "--mount --user --map-root-user" succeeded -- the standard
// unprivileged-mount-namespace pattern rootless container runtimes use.
// This test is portable (no linux build tag) so it catches a regression on
// any platform, even though the command it inspects only ever runs on Linux.
func TestMountNamespaceCommandUsesUnprivilegedUserNamespace(t *testing.T) {
	isolator := &Isolator{WorkDir: t.TempDir()}
	cmd, cleanup, err := isolator.executeIsolatedMountNamespace(context.Background(), "true", nil)
	if err != nil {
		t.Fatalf("executeIsolatedMountNamespace: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	args := cmd.Args
	if len(args) < 1 || !strings.HasSuffix(args[0], "unshare") {
		t.Fatalf("args[0] = %q, want it to invoke unshare", args)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--mount", "--user", "--map-root-user"} {
		if !strings.Contains(joined, want) {
			t.Errorf("unshare args %v missing %q -- without it this command needs CAP_SYS_ADMIN in the caller's own (unprivileged) user namespace and will always fail with EPERM on a rootless deployment", args, want)
		}
	}
}

// TestStrictSandboxNetworkPolicy pins the two shapes an agent profile's
// runtime.sandbox policy produces: deny-by-default with network cut, and
// deny-by-default with network opted in. The execution half runs a real
// socket connect under sandbox-exec: with network denied the kernel refuses
// the socket itself ("Operation not permitted"); with network allowed the
// connect reaches the (closed) port and is refused instead.
func TestStrictSandboxNetworkPolicy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific test")
	}
	env := &TestEnvironment{}
	if err := env.Setup(t); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer env.Cleanup()

	noNetwork := &Isolator{ReadPaths: []string{env.ReadOnlyDir}, WritePaths: []string{env.ReadWriteDir}, WorkDir: env.TempDir, BaseDir: env.TempDir, StrictAllowlist: true}
	withNetwork := &Isolator{ReadPaths: []string{env.ReadOnlyDir}, WritePaths: []string{env.ReadWriteDir}, WorkDir: env.TempDir, BaseDir: env.TempDir, StrictAllowlist: true, AllowNetwork: true}

	if p := noNetwork.generateSandboxProfile(); !strings.Contains(p, "(deny default)") || strings.Contains(p, "(allow network*)") {
		t.Fatalf("strict/no-network profile wrong:\n%s", p)
	}
	if p := withNetwork.generateSandboxProfile(); !strings.Contains(p, "(deny default)") || !strings.Contains(p, "(allow network*)") {
		t.Fatalf("strict/network profile wrong:\n%s", p)
	}

	probe := filepath.Join(env.ReadWriteDir, "probe.py")
	if err := os.WriteFile(probe, []byte("import socket\ns = socket.socket()\ns.settimeout(3)\ntry:\n    s.connect((\"127.0.0.1\", 9))\n    print(\"connected\")\nexcept OSError as e:\n    print(\"ERR:\" + str(e))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(iso *Isolator) string {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd, cleanup, err := iso.ExecuteIsolated(ctx, "python3 "+probe, nil)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			t.Fatalf("ExecuteIsolated failed: %v", err)
		}
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := run(noNetwork); !strings.Contains(out, "not permitted") {
		t.Fatalf("network should be denied under the strict/no-network sandbox, got: %s", out)
	}
	if out := run(withNetwork); !strings.Contains(out, "refused") {
		t.Fatalf("network should be allowed (and the closed port refused) under strict/network, got: %s", out)
	}
	if out := run(noNetwork); strings.Contains(out, "No module") || strings.Contains(out, "python3: command not found") {
		t.Skipf("python3 unavailable inside the sandbox: %s", out)
	}
}
