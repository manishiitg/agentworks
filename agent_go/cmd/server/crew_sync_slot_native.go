package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/nativesession"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
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
	request := slots.NativeSessionRequest{OwnerID: owner, RuntimeID: filepath.Base(runtimeDir), SessionID: id, Transport: transport, Operation: "export"}
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
