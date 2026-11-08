package runlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746: fragments must be visible before completion, retain their observed
// stream order, and keep the last unterminated line without buffering it away.
func TestLiveFragmentsKeepStreamOrderAndCannotOverwrite(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	log, err := Create(root, "logs/attempt.log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	_, _ = log.Stream("stdout").Write([]byte("first"))
	live, err := os.ReadFile(log.Path())
	if err != nil || !strings.Contains(string(live), "first") {
		t.Fatalf("not live: %q %v", live, err)
	}
	_, _ = log.Stream("stderr").Write([]byte("second\n"))
	_, _ = log.Stream("stdout").Write([]byte("-continued\nlast"))
	log.Close()
	data, _ := os.ReadFile(log.Path())
	text := string(data)
	if !(strings.Index(text, "first") < strings.Index(text, "second") && strings.Index(text, "second") < strings.Index(text, "-continued")) || !strings.HasSuffix(text, "last") {
		t.Fatalf("fragments lost/reordered: %s", text)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if _, err := time.Parse(time.RFC3339Nano, strings.SplitN(line, " ", 2)[0]); err != nil {
			t.Fatalf("line has no capture time: %q", line)
		}
	}
	if _, err := Create(root, "logs/attempt.log"); err == nil {
		t.Fatal("overwrote prior attempt")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root.Name(), "link")); err == nil {
		if _, err := Create(root, "link/escape.log"); err == nil {
			t.Fatal("followed a link out of the log root")
		}
	}
}

// Generic background output has an explicit large spill bound. Scripts with
// an explicit run log remain uncapped; a capped spill is visibly incomplete.
func TestGenericSpillLimitIsVisibleAndExplicitScriptLogStaysFull(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	open := func(name string) func() (*Log, error) { return func() (*Log, error) { return Create(root, name) } }
	spill := NewCapture(8, open("spill.log"), false)
	spill.maxFileBytes = 256
	_, _ = spill.Stream("stdout").Write([]byte(strings.Repeat("Q", 2000) + "TAIL"))
	spill.Close()
	data, _ := os.ReadFile(spill.Path())
	if spill.Error() == "" || spill.Complete() || !strings.Contains(string(data), "capture stopped") || strings.Contains(string(data), "TAIL") {
		t.Fatalf("cap was silent: %s %s", spill.Error(), data)
	}
	explicit := NewCapture(8, open("script.log"), true)
	_, _ = explicit.Stream("stdout").Write([]byte(strings.Repeat("Q", 2000) + "TAIL"))
	explicit.Close()
	data, _ = os.ReadFile(explicit.Path())
	if explicit.Error() != "" || !explicit.Complete() || strings.Count(string(data), "Q") != 2000 || !strings.HasSuffix(string(data), "TAIL") {
		t.Fatal("explicit script log was truncated")
	}
}
