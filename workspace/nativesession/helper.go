package nativesession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Run is called only after the helper proves it is running as the actual slot
// uid. The root-owned slotctl binary contains no provider parser or bundler.
func Run(input io.Reader, output io.Writer, cfg slots.ExecConfig, slot string) error {
	decoder := json.NewDecoder(io.LimitReader(input, HeaderLimit+1))
	decoder.DisallowUnknownFields()
	var req slots.NativeSessionRequest
	if err := decoder.Decode(&req); err != nil {
		return err
	}
	runtime, err := req.NativeRuntime(cfg, slot)
	if err != nil {
		return err
	}
	payload, err := Payload(decoder, input)
	if err != nil {
		return err
	}
	// Resolve each component; private paths authored by an agent cannot redirect
	// an application-authorized operation to another session or a login file.
	relative := "cli-runtimes/v1/" + req.RuntimeID
	runtime, err = sessiontransfer.PathDirectory(cfg.SlotStateDir(slot), relative, false)
	if err != nil {
		return err
	}
	store, err := sessiontransfer.PathDirectory(runtime, ".sandbox-cache/cli-home/claude-code/.claude", req.Operation == "import")
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(runtime, ".native-session-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var b *sessiontransfer.Bundle
	if req.Operation == "export" {
		b, err = sessiontransfer.ExportJSONL("claude-code", req.Transport, store, runtime, req.SessionID, stage)
		if err != nil {
			return err
		}
		if err = json.NewEncoder(output).Encode(b); err == nil {
			err = WriteFiles(output, b)
		}
	} else {
		b = &sessiontransfer.Bundle{}
		descriptor := json.NewDecoder(bytes.NewReader(req.Bundle))
		descriptor.DisallowUnknownFields()
		if err = descriptor.Decode(b); err != nil {
			return err
		}
		if b.Provider != "claude-code" || b.Format != "claude-jsonl" || b.SessionID != req.SessionID || b.Transport != req.Transport {
			return fmt.Errorf("native session identity differs")
		}
		if err = ReadFiles(payload, b, stage); err != nil {
			return err
		}
		if err = sessiontransfer.ImportJSONL(store, runtime, b); err != nil {
			return err
		}
		err = json.NewEncoder(output).Encode(map[string]bool{"imported": true})
	}
	if err != nil {
		return err
	}
	var size int64
	for _, f := range b.Files {
		size += f.Size
	}
	log.Printf("[SLOT_NATIVE] owner=%s slot=%s op=%s session=%s files=%d bytes=%d", req.OwnerID, slot, req.Operation, req.SessionID, len(b.Files), size)
	return nil
}
