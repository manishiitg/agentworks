package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/nativesession"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Only the authenticated Crew owner determines the account. The runtime was
// already derived from that owner's private conversation by crewProgramStore.
func crewNativeSlot(owner, runtimeDir, provider string) (string, bool) {
	if cliHostOS != "linux" || provider != "claude-code" {
		return "", false
	}
	slot, enabled, err := slots.For(owner)
	return slot, err == nil && enabled && slot != "" && inSlotStateFolder(runtimeDir, slot)
}

func crewSlotNativeTransfer(ctx context.Context, owner, slot, runtimeDir, id, transport string, b *sessiontransfer.Bundle, out string) (*sessiontransfer.Bundle, error) {
	return crewSlotNativeTransferBudget(ctx, owner, slot, runtimeDir, id, transport, b, out, sessiontransfer.MaxBytes())
}

func crewSlotNativeTransferBudget(ctx context.Context, owner, slot, runtimeDir, id, transport string, b *sessiontransfer.Bundle, out string, maxBytes int64) (*sessiontransfer.Bundle, error) {
	request := slots.NativeSessionRequest{OwnerID: owner, RuntimeID: filepath.Base(runtimeDir), SessionID: id, Transport: transport, Operation: "export", MaxBytes: maxBytes}
	if b != nil {
		request.Operation = "import"
		request.Bundle, _ = json.Marshal(b)
	}
	cfg, err := slots.LoadExecConfig(slots.ConfigPath())
	if err != nil {
		return nil, err
	}
	expected, err := request.NativeRuntime(cfg, slot)
	if err != nil {
		return nil, err
	}
	if expected != filepath.Clean(runtimeDir) {
		return nil, fmt.Errorf("native runtime differs from this owner's account")
	}
	argv := slots.SudoArgv(slot)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	reader, writer := io.Pipe()
	cmd.Stdin = reader
	var reason bytes.Buffer
	cmd.Stderr = &reason
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	sent := make(chan error, 1)
	go func() {
		e := json.NewEncoder(writer).Encode(slots.ExecRequest{NativeSession: &request})
		if e == nil && b != nil {
			e = nativesession.WriteFiles(writer, b)
		}
		writer.CloseWithError(e)
		sent <- e
	}()
	var result *sessiontransfer.Bundle
	if b == nil {
		result, err = nativesession.ReadBundle(stdout, out)
	} else {
		var answer struct {
			Imported bool `json:"imported"`
		}
		err = json.NewDecoder(io.LimitReader(stdout, 4096)).Decode(&answer)
		if err == nil && !answer.Imported {
			err = fmt.Errorf("native session import was not confirmed")
		}
	}
	if err != nil {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	reader.Close()
	sendErr := <-sent
	if err != nil || waitErr != nil || sendErr != nil {
		diagnostic := strings.TrimSpace(reason.String())
		if len(diagnostic) > 1024 {
			diagnostic = diagnostic[:1024]
		}
		return nil, fmt.Errorf("native session transfer as assigned account failed: read=%v process=%v send=%v: %s", err, waitErr, sendErr, diagnostic)
	}
	completed := result
	if completed == nil {
		completed = b
	}
	var size int64
	for _, f := range completed.Files {
		size += f.Size
	}
	log.Printf("[SLOT_NATIVE] owner=%s slot=%s op=%s session=%s files=%d bytes=%d", owner, slot, request.Operation, id, len(completed.Files), size)
	return result, nil
}

func exportCrewNativeProgram(ctx context.Context, owner, runtimeDir, store, cwd, id, provider, transport, out string, remember bool) (*sessiontransfer.Bundle, error) {
	if slot, ok := crewNativeSlot(owner, runtimeDir, provider); ok {
		return crewSlotNativeTransfer(ctx, owner, slot, runtimeDir, id, transport, nil, out)
	}
	return exportCrewProgram(store, cwd, id, provider, transport, out, remember)
}

// carryClaudeTranscriptIntoSlot moves a Claude chat's native transcript from the app account's runtime folder into the
// same runtime folder in the owner's slot, so a chat last used before it moved to the slot resumes natively instead of
// starting a fresh session with a continuity notice (owner, 2 Nov: "native resume as much as possible"). It runs only
// for the owner's own chat (the slot helper checks the account), only for app -> slot with the same runtime id, and
// only before resume is decided; on any failure the old behaviour (fresh session + archive) stays.
func (api *StreamingAPI) carryClaudeTranscriptIntoSlot(owner string, runtime *ChatHistoryAgentRuntime, agent *mcpagent.Agent) {
	if runtime == nil || runtime.AgentSessionHandle == nil || agent == nil {
		return
	}
	saved := runtime.AgentSessionHandle.Provider
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil || saved.Provider != "claude-code" || current.Provider.Provider != "claude-code" {
		return
	}
	id := firstNonEmptyTrimmed(saved.NativeSessionID, runtime.ExternalSessionID)
	oldDir, newDir := strings.TrimSpace(saved.WorkingDir), strings.TrimSpace(current.Provider.WorkingDir)
	if id == "" || oldDir == "" || newDir == "" || filepath.Clean(oldDir) == filepath.Clean(newDir) || filepath.Base(oldDir) != filepath.Base(newDir) {
		return
	}
	slot, ok := crewNativeSlot(owner, newDir, "claude-code")
	if !ok || inSlotStateFolder(oldDir, slot) {
		return
	}
	// The saved handle is stored data and the app account can read every person's app runtime: never read from the
	// path it names. Recompute the one app-side folder this runtime id can have and require it exactly, so a
	// tampered or stale handle cannot pull another person's transcript into this owner's slot.
	appState, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	expected := filepath.Join(appState, "cli-runtimes", "v1", filepath.Base(newDir))
	if filepath.Clean(oldDir) != filepath.Clean(expected) {
		log.Printf("[SLOT_NATIVE] not carried into %s: session=%s: saved runtime %q is not this runtime's app folder", slot, id, oldDir)
		return
	}
	oldDir = expected
	out, err := os.MkdirTemp("", "native-slot-move-")
	if err != nil {
		return
	}
	defer os.RemoveAll(out)
	b, err := sessiontransfer.ExportJSONL("claude-code", saved.Transport, filepath.Join(oldDir, security.SandboxPersistentDirName, "cli-home", "claude-code", ".claude"), oldDir, id, out)
	if err != nil {
		log.Printf("[SLOT_NATIVE] not carried into %s: session=%s: %v", slot, id, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err = crewSlotNativeTransfer(ctx, owner, slot, newDir, id, saved.Transport, b, ""); err != nil {
		log.Printf("[SLOT_NATIVE] not carried into %s: session=%s: %v", slot, id, err)
		return
	}
	moved := *runtime.AgentSessionHandle
	moved.Provider.WorkingDir = newDir
	runtime.AgentSessionHandle = &moved
	log.Printf("[SLOT_NATIVE] carried Claude session %s from the app runtime into %s; resuming natively", id, slot)
}
