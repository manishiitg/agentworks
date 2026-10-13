package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// On a real Windows machine the process primitives must see this very process and a child of it, with start
// times, and the provider host key must be stable for one process.
func TestWindowsProcessPrimitivesSeeThisProcessAndAChild(t *testing.T) {
	self, err := winProcRead(os.Getpid())
	if err != nil || self.PID != os.Getpid() || self.Created == 0 || self.Name == "" {
		t.Fatalf("self = %+v, %v", self, err)
	}
	child := exec.Command("cmd.exe", "/c", "ping -n 20 127.0.0.1 >nul")
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	time.Sleep(300 * time.Millisecond)
	got, err := winProcRead(child.Process.Pid)
	if err != nil || got.PPID != os.Getpid() || procBaseName(got.Name) != "cmd" || got.Created < self.Created {
		t.Fatalf("child = %+v, %v (self %+v)", got, err, self)
	}
	key1, key2 := winHostKey(got), winHostKey(got)
	if key1 != key2 || key1 == winHostKey(self) {
		t.Fatal("host key must be stable for one process and differ between processes")
	}
	if !winHostAlive("cmd", key1) {
		t.Fatal("a running process was reported dead")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if winHostAlive("cmd", key1) {
		t.Fatal("an ended process was reported alive")
	}
}
