package handlers

import (
	"os/exec"
	"testing"
	"time"
)

// The managed Chrome on the servers is /opt/google/chrome/chrome, not "chromium": the process list (and so Stop/cleanup) used to
// match only "chromium", so stopping a browser found nothing to stop.
func TestBrowserProcessListSeesGoogleChrome(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("needs bash for exec -a")
	}
	cmd := exec.Command(bash, "-c", "exec -a /opt/google/chrome/chrome sleep 30")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start a stand-in process")
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	var found bool
	for i := 0; i < 20 && !found; i++ {
		time.Sleep(100 * time.Millisecond)
		processes, err := getBrowserProcesses()
		if err != nil {
			t.Fatal(err)
		}
		for _, process := range processes {
			if process.PID == cmd.Process.Pid {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("pid %d (argv0 /opt/google/chrome/chrome) is not in the browser process list", cmd.Process.Pid)
	}
}
