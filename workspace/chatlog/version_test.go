package chatlog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// PLAT-746-876: check source identity while holding Save/Append's file lock.
// Two writers starting with one version must not append the same tail twice.
func TestConditionalAppendSerializesWritersAndRejectsRewrittenSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builder", "conversation", "test-conversation.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"session_id":"test","conversation_history":[{"Role":"human","Parts":[{"Text":"saved"}]}]}`)
	if err := Save(path, original); err != nil {
		t.Fatal(err)
	}
	version, err := CurrentVersion(path)
	if err != nil {
		t.Skip("native file identity unavailable: " + err.Error())
	}
	row := json.RawMessage(`{"Role":"ai","Parts":[{"Text":"new tail"}]}`)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- AppendIfVersion(path, []json.RawMessage{row}, nil, &version)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, changed := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrSourceChanged) {
			changed++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || changed != 1 {
		t.Fatalf("conditional writers accepted=%d conflicts=%d", accepted, changed)
	}
	raw, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		History []json.RawMessage `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.History) != 2 {
		t.Fatal("duplicate or missing tail")
	}
	previous, err := CurrentVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, original); err != nil {
		t.Fatal(err)
	}
	if err := AppendIfVersion(path, []json.RawMessage{row}, nil, &previous); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("appended onto rewritten source: %v", err)
	}
}
