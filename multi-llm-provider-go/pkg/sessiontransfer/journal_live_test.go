package sessiontransfer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-442: real native memory survives relocation and return; no archive
// history is supplied. Runtime-specific tests share one selected local login.
func TestNativeJournalAdapterRoundTripLive(t *testing.T) {
	if !*nativeTransferLive {
		t.Skip("requires existing CLI logins and installed native runtimes")
	}
	for _, tc := range []struct{ provider, transport, model string }{{"claude-code", "sdk", "sonnet"}, {"claude-code", "tmux", "sonnet"}, {"codex-cli", "app-server", "gpt-6-luna"}, {"codex-cli", "tmux", "gpt-6-luna"}, {"cursor-cli", "tmux", "composer-2.5"}} {
		t.Run(tc.provider+"-"+tc.transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			sourceWork, destWork, sourceRuntime, destRuntime := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			sourceWork, _ = filepath.EvalSymlinks(sourceWork)
			destWork, _ = filepath.EvalSymlinks(destWork)
			home, e := os.UserHomeDir()
			if e != nil {
				t.Fatal(e)
			}
			sourceStore, destStore := "", ""
			switch tc.provider {
			case "claude-code":
				sourceStore = os.Getenv("CLAUDE_CONFIG_DIR")
				if sourceStore == "" {
					sourceStore = filepath.Join(home, ".claude")
				}
				destStore = sourceStore
			case "codex-cli":
				selected := os.Getenv("CODEX_HOME")
				if selected == "" {
					selected = filepath.Join(home, ".codex")
				}
				// Local-only test fixture: two independent homes use the person's existing
				// login. These credentials are never part of the exported native packet.
				sourceStore, destStore = t.TempDir(), t.TempDir()
				auth, e := os.ReadFile(filepath.Join(selected, "auth.json"))
				if e != nil {
					t.Fatal(e)
				}
				for _, root := range []string{sourceStore, destStore} {
					if e = os.WriteFile(filepath.Join(root, "auth.json"), auth, 0600); e != nil {
						t.Fatal(e)
					}
				}
			case "cursor-cli":
				sourceStore, e = cursorcli.PrivateRuntimeConfigDirectory(sourceRuntime, os.Environ())
				if e != nil {
					t.Fatal(e)
				}
				destStore, e = cursorcli.PrivateRuntimeConfigDirectory(destRuntime, os.Environ())
				if e != nil {
					t.Fatal(e)
				}
			}
			turn := func(work, runtimeDir, store, id, marker, prompt string) (*llmtypes.ContentResponse, string) {
				t.Helper()
				owner := "native-transfer-" + filepath.Base(runtimeDir)
				opts := []llmtypes.CallOption{llmtypes.WithCLIRuntimeDir(runtimeDir), llmtypes.WithCodingProviderTransport(tc.transport)}
				var model llmtypes.Model
				switch tc.provider {
				case "claude-code":
					model = claudecode.NewClaudeCodeInteractiveAdapter(tc.model, nativeLiveLogger{})
					opts = append(opts, claudecode.WithWorkingDir(work), claudecode.WithInteractiveSessionID(owner), claudecode.WithPersistentInteractiveSession(true))
					if id != "" {
						opts = append(opts, claudecode.WithResumeSessionID(id))
					}
					defer claudecode.CloseClaudeCodeInteractiveSessionForOwner(owner, "native transfer test complete")
				case "codex-cli":
					model = codexcli.NewCodexCLIAdapter("", tc.model, nativeLiveLogger{})
					opts = append(opts, codexcli.WithProjectDirID(work), codexcli.WithInteractiveSessionID(owner), codexcli.WithPersistentInteractiveSession(false), codexcli.WithApprovalPolicy("never"), llmtypes.WithProviderAccountEnvironment(map[string]string{"CODEX_HOME": store}))
					if id != "" {
						opts = append(opts, codexcli.WithResumeSessionID(id))
					}
					defer codexcli.CloseCodexCLIInteractiveSessionForOwner(owner, "native transfer test complete")
				case "cursor-cli":
					model = cursorcli.NewCursorCLIAdapter("", tc.model, nativeLiveLogger{})
					opts = append(opts, cursorcli.WithWorkingDir(work), cursorcli.WithInteractiveSessionID(owner), cursorcli.WithPersistentInteractiveSession(false))
					if id != "" {
						opts = append(opts, cursorcli.WithResumeSessionID(id))
					}
					defer cursorcli.CloseCursorCLIInteractiveSessionForOwner(owner, "native transfer test complete")
				}
				response, e := model.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Public integration test. Only when asked for the location marker, report "+marker+". Keep public codes only in chat. Never use tools or write these codes into a file."), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, opts...)
				if e != nil {
					t.Fatal(e)
				}
				h, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(response)
				if !ok || h.NativeSessionID == "" {
					t.Fatal("native identity missing")
				}
				if id != "" && h.NativeSessionID != id {
					t.Fatalf("native identity changed %s -> %s", id, h.NativeSessionID)
				}
				if h.Transport != tc.transport {
					t.Fatalf("wrong runtime %s", h.Transport)
				}
				return response, h.NativeSessionID
			}
			export := func(store, work, id string) *sessiontransfer.Bundle {
				t.Helper()
				var b *sessiontransfer.Bundle
				var e error
				if tc.provider == "cursor-cli" {
					actual, e2 := cursorcli.NativeRuntimeConfigDirectory(map[bool]string{true: sourceRuntime, false: destRuntime}[work == sourceWork], os.Environ())
					if e2 != nil {
						t.Fatal(e2)
					}
					store = actual
					b, e = sessiontransfer.ExportCursorCLI(store, work, id, t.TempDir(), true)
				} else {
					b, e = sessiontransfer.ExportJSONL(tc.provider, tc.transport, store, work, id, t.TempDir())
				}
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			importBundle := func(store, work string, b *sessiontransfer.Bundle) {
				t.Helper()
				var e error
				if tc.provider == "cursor-cli" {
					runtimeDir := destRuntime
					if work == sourceWork {
						runtimeDir = sourceRuntime
					}
					actual, e2 := cursorcli.NativeRuntimeConfigDirectory(runtimeDir, os.Environ())
					if e2 != nil {
						t.Fatal(e2)
					}
					store = actual
					e = sessiontransfer.ImportCursorCLI(store, work, b)
				} else {
					e = sessiontransfer.ImportJSONL(store, work, b)
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			_, id := turn(sourceWork, sourceRuntime, sourceStore, "", "SOURCE_AMBER", "Remember public code NATIVE_AMBER_714 in this conversation only. Reply with this code and the location marker.")
			b := export(sourceStore, sourceWork, id)
			importBundle(destStore, destWork, b)
			response, _ := turn(destWork, destRuntime, destStore, id, "DESTINATION_JADE", "What public code did I ask you to remember? Also remember NATIVE_JADE_936 in this chat only. Report the earlier code and current location marker.")
			content := response.Choices[0].Content
			if !strings.Contains(content, "NATIVE_AMBER_714") || !strings.Contains(content, "DESTINATION_JADE") {
				t.Fatalf("destination lost history/current instructions: %s", content)
			}
			back := export(destStore, destWork, id)
			importBundle(sourceStore, sourceWork, back)
			response, _ = turn(sourceWork, sourceRuntime, sourceStore, id, "SOURCE_AMBER", "Report both public codes I asked you to remember and the current location marker.")
			content = response.Choices[0].Content
			if !strings.Contains(content, "NATIVE_AMBER_714") || !strings.Contains(content, "NATIVE_JADE_936") || !strings.Contains(content, "SOURCE_AMBER") {
				t.Fatalf("return lost history/current instructions: %s", content)
			}
			t.Logf("%s/%s same native %s: destination/return memory and current instructions passed", tc.provider, tc.transport, id)
		})
	}
}

type nativeLiveLogger struct{}

func (nativeLiveLogger) Infof(string, ...any)  {}
func (nativeLiveLogger) Errorf(string, ...any) {}
func (nativeLiveLogger) Debugf(string, ...any) {}
