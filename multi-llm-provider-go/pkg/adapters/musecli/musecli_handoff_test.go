package musecli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-442: use the installed CLI, including its permission-frame replay,
// to prove identity survives both directions, and divergence never overwrites.
func TestMuseNativeHandoffPreservesIDAndRejectsDivergence(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	a := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	from, to := t.TempDir(), t.TempDir()
	homeA, homeB := os.Getenv("XDG_DATA_HOME"), t.TempDir()
	turn := func(home, dir, id, text string) (string, error) {
		t.Setenv("XDG_DATA_HOME", home)
		response, err := a.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Current test workspace: " + dir}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}},
		}, WithMuseStructuredTransport(true), WithWorkingDir(dir), WithResumeSessionID(id))
		if err != nil {
			return "", err
		}
		return response.Choices[0].GenerationInfo.CodingProviderSessionHandle.NativeSessionID, nil
	}
	id, err := turn(homeA, from, "", "FIRST")
	if err != nil {
		t.Fatal(err)
	}
	first, err := sessiontransfer.ExportMuse(homeA, id, from, "structured")
	if err != nil {
		t.Fatal(err)
	}
	if err = sessiontransfer.ImportMuse(homeB, to, first); err != nil {
		t.Fatal(err)
	}
	if got, err := turn(homeB, to, id, "SECOND"); err != nil || got != id {
		t.Fatalf("destination native ID=%q err=%v", got, err)
	}
	if _, err = turn(homeB, t.TempDir(), id, "WRONG_WORKSPACE"); err == nil {
		t.Fatal("handoff allowed a different destination folder")
	}
	second, err := sessiontransfer.ExportMuse(homeB, id, to, "structured")
	if err != nil {
		t.Fatal(err)
	}
	if err = sessiontransfer.ImportMuse(homeA, from, second); err != nil {
		t.Fatal(err)
	}
	if got, err := turn(homeA, from, id, "THIRD"); err != nil || got != id {
		t.Fatalf("return native ID=%q err=%v", got, err)
	}
	path := museSessionLogPath(id, homeA)
	before, _ := os.ReadFile(path)
	if err = sessiontransfer.ImportMuse(homeA, from, first); err == nil {
		t.Fatal("older divergent session overwrote new local turns")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("refused import changed the journal")
	}
	marker := filepath.Join(filepath.Dir(path), "agentworks-handoff.json")
	info, err := os.Stat(marker)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private handoff binding: %v %v", info, err)
	}
}
