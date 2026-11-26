package sessiontransfer_test

import (
	"context"
	"flag"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

var nativeTransferLive = flag.Bool("native-transfer-live", false, "run authenticated native state relocation checks")

// PLAT-856-442: exercise the actual adapter/SDK and importer, including the
// return leg. Passing an archive to a fresh agent must not satisfy this test.
func TestCursorSDKNativeTransferRoundTripLive(t *testing.T) {
	if !*nativeTransferLive {
		t.Skip("requires an existing Cursor login and the managed SDK")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	sourceWork, destWork, sourceRuntime, destRuntime := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	adapter := cursorcli.NewCursorCLIAdapter("", "composer-2.5[fast=false]", nil)
	turn := func(work, runtime, id, marker, prompt string) (*llmtypes.ContentResponse, string) {
		t.Helper()
		opts := []llmtypes.CallOption{cursorcli.WithWorkingDir(work), cursorcli.WithInteractiveSessionID("native-sdk-" + filepath.Base(runtime)), cursorcli.WithPersistentInteractiveSession(false), llmtypes.WithCLIRuntimeDir(runtime)}
		if id != "" {
			opts = append(opts, cursorcli.WithResumeSessionID(id))
		}
		response, e := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "This is a public test. When asked for the location marker, answer "+marker+". Keep the public test codes only in this chat; never write them to files."), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, opts...)
		if e != nil {
			t.Fatal(e)
		}
		handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(response)
		if !ok || handle.NativeSessionID == "" {
			t.Fatal("SDK returned no native identity")
		}
		if id != "" && handle.NativeSessionID != id {
			t.Fatalf("native identity changed: %s -> %s", id, handle.NativeSessionID)
		}
		return response, handle.NativeSessionID
	}
	_, id := turn(sourceWork, sourceRuntime, "", "SOURCE_AMBER", "Remember public code NATIVE_AMBER_714 in this conversation only. Reply with it and the location marker.")
	sourceStore := filepath.Join(sourceRuntime, ".sandbox-cache", "cursor-sdk-state")
	destStore := filepath.Join(destRuntime, ".sandbox-cache", "cursor-sdk-state")
	bundle, e := sessiontransfer.ExportCursorSDK(sourceStore, sourceWork, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = sessiontransfer.ImportCursorSDK(destStore, destWork, bundle); e != nil {
		t.Fatal(e)
	}
	response, _ := turn(destWork, destRuntime, id, "DESTINATION_JADE", "What public code did I ask you to remember earlier? Also remember NATIVE_JADE_936 in this chat only. Report the earlier code and current location marker.")
	text := response.Choices[0].Content
	if !strings.Contains(text, "NATIVE_AMBER_714") || !strings.Contains(text, "DESTINATION_JADE") {
		t.Fatalf("destination lost native history or current instructions: %s", text)
	}
	back, e := sessiontransfer.ExportCursorSDK(destStore, destWork, id, t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = sessiontransfer.ImportCursorSDK(sourceStore, sourceWork, back); e != nil {
		t.Fatal(e)
	}
	response, _ = turn(sourceWork, sourceRuntime, id, "SOURCE_AMBER", "What are the two public codes I asked you to remember? Reply with both codes and the current location marker.")
	text = response.Choices[0].Content
	if !strings.Contains(text, "NATIVE_AMBER_714") || !strings.Contains(text, "NATIVE_JADE_936") || !strings.Contains(text, "SOURCE_AMBER") {
		t.Fatalf("return lost native history or current instructions: %s", text)
	}
	// A pre-private-store chat cannot silently become a fresh empty agent.
	missingRuntime := t.TempDir()
	_, e = adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What were the public codes?")}, cursorcli.WithWorkingDir(sourceWork), cursorcli.WithInteractiveSessionID("missing-native-"+filepath.Base(missingRuntime)), cursorcli.WithPersistentInteractiveSession(false), cursorcli.WithResumeSessionID(id), llmtypes.WithCLIRuntimeDir(missingRuntime))
	if e == nil {
		t.Fatal("missing private native history silently started an empty chat")
	}
	t.Logf("same native SDK agent %s: source/destination/return memory and instructions passed", id)
}
