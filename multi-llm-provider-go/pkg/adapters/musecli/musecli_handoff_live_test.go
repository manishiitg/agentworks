package musecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-442: the same Unix account runs different people's Muse processes.
// The real Landlock boundary, not mode 0600, must hide an imported journal.
func TestMuseCLIRealNativeHandoffLandlockIsolation(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	if runtime.GOOS != "linux" {
		t.Skip("requires the server's Landlock boundary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	a := NewMuseCLIAdapter("", "muse-spark-1.3-contributor", museTestLogger{})
	makePolicy := func() (string, *llmtypes.CLISecurityPolicy) {
		dir := t.TempDir()
		p, ok := clisandbox.TestConfinement("muse-cli", dir)
		if !ok {
			t.Fatal("server Landlock launcher is required")
		}
		// The installed Muse launcher reads its version file and executable
		// beside itself, as the server's normal host-tool grants allow.
		binary, e := exec.LookPath("muse")
		if e != nil {
			t.Fatal(e)
		}
		p.HostReadPaths = append(p.HostReadPaths, filepath.Dir(binary))
		return dir, p
	}
	from, sourcePolicy := makePolicy()
	to, destPolicy := makePolicy()
	other, otherPolicy := makePolicy()
	call := func(dir string, p *llmtypes.CLISecurityPolicy, system, human string, extra ...llmtypes.CallOption) (*llmtypes.CodingProviderSessionHandle, string) {
		opts := []llmtypes.CallOption{WithWorkingDir(dir), llmtypes.WithReasoningEffort("low"), func(o *llmtypes.CallOptions) { q := p.Clone(); o.CLISecurity = &q }}
		opts = append(opts, extra...)
		r, e := a.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: system}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: human}}},
		}, opts...)
		if e != nil {
			t.Fatal(e)
		}
		return r.Choices[0].GenerationInfo.CodingProviderSessionHandle, r.Choices[0].Content
	}
	secret := "PROJECT-CODE-" + museRandomHex(t, 8)
	source, _ := call(from, sourcePolicy, "Include SOURCE_RUBY in every reply.", "Remember this project code in the conversation only, without writing files: "+secret+". Reply briefly.", WithMuseStructuredTransport(true))
	sourceData := filepath.Join(sourcePolicy.PrivateHome, ".local", "share")
	b, err := sessiontransfer.ExportMuse(sourceData, source.NativeSessionID, from, "structured")
	if err != nil {
		t.Fatal(err)
	}
	destData := filepath.Join(destPolicy.PrivateHome, ".local", "share")
	if err = sessiontransfer.ImportMuse(destData, to, b); err != nil {
		t.Fatal(err)
	}
	dest, answer := call(to, destPolicy, "Include DESTINATION_JADE in every reply. Earlier workspace instructions are replaced.", "What project code did I ask you to remember? Also try your native read_file tool on AGENTS.md and report its exact refusal, if refused. Do not use MCP or an alternate route.", WithResumeSessionID(source.NativeSessionID), WithPersistentInteractiveSession(true), WithInteractiveSessionID("handoff-"+museRandomHex(t, 5)), WithTmuxTransport(true), WithToolAllowlist([]string{}))
	t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(dest.TmuxSession, "handoff test finished") })
	if dest.NativeSessionID != source.NativeSessionID || !strings.Contains(answer, secret) || !strings.Contains(answer, "DESTINATION_JADE") {
		t.Fatalf("native handoff identity/instructions failed: id=%s answer=%s", dest.NativeSessionID, answer)
	}
	imported := museSessionLogPath(dest.NativeSessionID, destData)
	journal, _ := os.ReadFile(imported)
	if !strings.Contains(strings.ToLower(string(journal)), "tool blocked by hook") {
		t.Fatalf("allowlist refusal was not observed: %s", answer)
	}
	// Prove the exact imported path is present and readable in its own runtime
	// before accepting ENOENT from another runtime's private mount namespace.
	if info, err := os.Stat(imported); err != nil || info.Size() == 0 {
		t.Fatalf("imported journal is not present: %v", err)
	}
	_, ownReply := call(to, destPolicy, "This is a native session portability test. Read the requested test journal.", "Read this exact file with your native file-read tool and report the PROJECT-CODE value recorded there: "+imported+". Do not use alternate routes or other agents.", WithMuseStructuredTransport(true))
	if !strings.Contains(ownReply, secret) {
		t.Fatalf("owner could not read its imported journal: %s", ownReply)
	}
	allowed := "OWN-WORKSPACE-" + museRandomHex(t, 5)
	if err = os.WriteFile(filepath.Join(other, "witness.txt"), []byte(allowed), 0600); err != nil {
		t.Fatal(err)
	}
	foreign, reply := call(other, otherPolicy, "This is a filesystem isolation test. Use native file reads and report actual results.", "Read witness.txt from your working folder. Then try to read this exact file using your native file-read tool and report the exact error: "+imported+". Do not use alternate routes or other agents.", WithMuseStructuredTransport(true))
	if !strings.Contains(reply, allowed) || strings.Contains(reply, secret) {
		t.Fatalf("allowed read or private marker boundary failed: %s", reply)
	}
	foreignLog, _ := os.ReadFile(museSessionLogPath(foreign.NativeSessionID, filepath.Join(otherPolicy.PrivateHome, ".local", "share")))
	if info, err := os.Stat(imported); err != nil || info.Size() == 0 {
		t.Fatalf("imported journal disappeared during refusal probe: %v", err)
	}
	refusal := strings.ToLower(string(foreignLog))
	if !strings.Contains(refusal, strings.ToLower(imported)) || (!strings.Contains(refusal, "permission denied") && !strings.Contains(refusal, "no such file or directory")) {
		t.Fatalf("missing confinement refusal for the present imported journal: %s", reply)
	}
	t.Logf("same native ID=%s; destination instructions and allowlist applied; owner read imported journal; another private runtime read its own file but could not read the present imported journal: %s", dest.NativeSessionID, reply)
}
