//go:build linux || darwin

package server

import (
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// PLAT-746-876: no credential access or endpoint is needed; private profiles
// must be produced by the real signal handler in an isolated test process.
func TestLocalOwnerSignalWritesPrivateProfiles(t *testing.T) {
	if os.Getenv("AW_PROFILE_TEST_CHILD") == "1" {
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
		stop := startLocalProfileSignal()
		defer stop()
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			files, _ := filepath.Glob(filepath.Join(os.Getenv("AGENTWORKS_LOG_DIR"), "pprof-*"))
			if len(files) == 2 {
				valid := true
				for _, path := range files {
					f, err := os.Open(path)
					if err != nil {
						valid = false
						break
					}
					z, err := gzip.NewReader(f)
					if err != nil {
						f.Close()
						valid = false
						break
					}
					n, err := io.Copy(io.Discard, z)
					z.Close()
					f.Close()
					if err != nil || n == 0 {
						valid = false
						break
					}
				}
				if valid {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("signal did not produce two complete profiles")
	}
	dir := filepath.Join(t.TempDir(), "logs")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLocalOwnerSignalWritesPrivateProfiles$")
	cmd.Env = append(os.Environ(), "AW_PROFILE_TEST_CHILD=1", "AGENTWORKS_LOG_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("profile child: %v %s", err, out)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatal("profile directory is not private")
	}
	files, err := filepath.Glob(filepath.Join(dir, "pprof-*"))
	if err != nil || len(files) != 2 {
		t.Fatal("missing profiles")
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("profile is not private")
		}
	}
}
