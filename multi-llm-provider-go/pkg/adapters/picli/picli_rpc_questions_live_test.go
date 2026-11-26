package picli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-872-21: actual Pi extension UI before prompt acceptance must not deadlock.
// This executes native dialogs without a model call; no credential is required.
func TestPiRPCNativeDialogsLive(t *testing.T) {
	if os.Getenv("RUN_PI_RPC_DIALOG_LIVE") != "1" {
		t.Skip("set RUN_PI_RPC_DIALOG_LIVE=1")
	}
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extension := filepath.Join(dir, "questions.ts")
	result := filepath.Join(dir, "answers.json")
	source := fmt.Sprintf(`import {writeFileSync} from "node:fs";
export default function(pi) { pi.registerCommand("native_questions", {description:"Test native questions",handler:async(args,ctx)=>{
 const selected=await ctx.ui.select("Choose color",["Blue","Green"]);
 const confirmed=await ctx.ui.confirm("Proceed?","Native confirmation test");
 const input=await ctx.ui.input("Enter text");
 const edited=await ctx.ui.editor("Edit text","original");
 await new Promise(resolve=>setTimeout(resolve,180));
 writeFileSync(%q,JSON.stringify({selected,confirmed,input,edited}));
}});}`, result)
	if err = os.WriteFile(extension, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "pi-test")
	if err = os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec %s \"$@\" --extension %s\n", fmt.Sprintf("%q", binary), fmt.Sprintf("%q", extension))), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPiBinary, wrapper)
	t.Setenv("PI_STRUCTURED_WEDGE_SILENCE", "300ms")
	owner := t.Name()
	defer closePiRPCSession(owner)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	asked := 0
	ctx = llmtypes.WithCodingAgentQuestionHandler(ctx, "pi-cli", owner, func(_ context.Context, args map[string]any) (string, error) {
		asked++
		// A person can take longer than the model-silence timeout.
		time.Sleep(650 * time.Millisecond)
		answer := "typed text"
		if asked == 1 {
			answer = "Green"
		}
		if asked == 2 {
			answer = "No"
		}
		if asked == 4 {
			answer = "edited text"
		}
		reply := map[string]any{"id": "input", "other_text": answer}
		if asked <= 2 {
			reply = map[string]any{"id": "input", "selected_labels": []string{answer}}
		}
		b, _ := json.Marshal(map[string]any{"status": "answered", "answers": []any{reply}})
		return string(b), nil
	})
	_, err = NewPiCLIAdapter("test-unused-key", "google/gemini-3.8-flash", &mockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "/native_questions")}, WithWorkingDir(dir), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if asked != 4 || got["selected"] != "Green" || got["confirmed"] != false || got["input"] != "typed text" || got["edited"] != "edited text" {
		t.Fatalf("asked=%d result=%s", asked, raw)
	}
}
