package codingagentjob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const delegatedSystemPrompt = `You are a delegated coding agent working for another coding agent. Complete only the supplied task in the given working directory. Do not read, write, or run commands outside that working directory. Do not delegate to another coding agent. Respect the current workspace state, do not discard existing changes, and end with a concise result that the parent agent can use.`

var ansiEscapePattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

type ProviderRunner struct {
	Logger io.Writer
}

func (r ProviderRunner) Run(ctx context.Context, job Job, progress func(ProgressUpdate)) (RunResult, error) {
	provider := llmproviders.Provider(job.Provider)
	contract, ok := llmproviders.GetCodingAgentProviderContract(provider, job.Model)
	if !ok {
		return RunResult{}, fmt.Errorf("provider %q is not available for delegation", job.Provider)
	}
	logger := newWriterLogger(r.Logger)
	model, err := llmproviders.InitializeLLM(llmproviders.Config{
		Provider: provider,
		ModelID:  job.Model,
		Logger:   logger,
		Context:  ctx,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("initialize %s: %w", job.Provider, err)
	}
	unattended, err := unattendedProviderOptions(provider, job.WorkingDir)
	if err != nil {
		return RunResult{}, fmt.Errorf("configure unattended %s job: %w", job.Provider, err)
	}

	ownerSessionID := "llm-provider-mcp-" + job.ID
	defer closeProviderSession(provider, ownerSessionID, "delegated job finished")

	stream := make(chan llmtypes.StreamChunk, 64)
	streamCtx, stopStreaming := context.WithCancel(ctx)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		for {
			select {
			case <-streamCtx.Done():
				return
			case chunk, ok := <-stream:
				if !ok {
					return
				}
				if update := progressFromChunk(chunk); progress != nil && (update.Message != "" || update.TmuxSession != "") {
					progress(update)
				}
			}
		}
	}()

	options := []llmtypes.CallOption{llmtypes.WithStreamingChan(stream)}
	if option := llmproviders.CodingAgentWorkingDirOption(provider, job.WorkingDir); option != nil {
		options = append(options, option)
	}
	if option := llmproviders.CodingAgentInteractiveSessionOption(provider, ownerSessionID); option != nil {
		options = append(options, option)
	}
	if option := llmproviders.CodingAgentPersistentInteractiveOption(provider, false); option != nil {
		options = append(options, option)
	}
	if option := llmproviders.CodingAgentProjectInstructionOnlyOption(provider, true); option != nil {
		options = append(options, option)
	}
	options = append(options, unattended...)

	if progress != nil {
		progress(ProgressUpdate{Message: "Launching " + contract.DisplayName})
	}
	response, runErr := model.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, delegatedSystemPrompt),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, job.Task),
	}, options...)
	stopStreaming()
	<-streamDone
	if runErr != nil {
		return RunResult{}, fmt.Errorf("run %s: %w", job.Provider, runErr)
	}
	if response == nil || len(response.Choices) == 0 || response.Choices[0] == nil {
		return RunResult{}, fmt.Errorf("%s returned no response", job.Provider)
	}
	content := strings.TrimSpace(response.Choices[0].Content)
	if content == "" {
		return RunResult{}, fmt.Errorf("%s returned an empty response", job.Provider)
	}
	usage := response.Usage
	if usage == nil {
		usage = llmtypes.ExtractUsageFromGenerationInfo(response.Choices[0].GenerationInfo)
	}
	return RunResult{
		Content: content,
		Model:   model.GetModelID(),
		Usage:   jobUsage(usage),
	}, nil
}

// unattendedProviderOptions prevents detached tmux jobs from waiting on an
// approval prompt that nobody can see. Each provider keeps its native project
// sandbox where one is available; Claude receives explicit project-scoped file
// rules because dontAsk otherwise denies every operation that would prompt.
func unattendedProviderOptions(provider llmproviders.Provider, workingDir string) ([]llmtypes.CallOption, error) {
	switch provider {
	case llmproviders.ProviderCodexCLI:
		return []llmtypes.CallOption{
			llmproviders.WithCodexApprovalPolicy("never"),
			llmproviders.WithCodexSandbox("workspace-write"),
		}, nil
	case llmproviders.ProviderCursorCLI:
		return []llmtypes.CallOption{
			llmproviders.WithCursorForce(),
			llmproviders.WithCursorSandbox("enabled"),
		}, nil
	case llmproviders.ProviderClaudeCode:
		settings, err := claudeUnattendedSettings(workingDir)
		if err != nil {
			return nil, err
		}
		return []llmtypes.CallOption{
			llmproviders.WithClaudeCodeTools("default"),
			llmproviders.WithClaudeCodeSettings(settings),
		}, nil
	case llmproviders.ProviderPiCLI:
		// Pi enables its built-in coding tools by default and the adapter passes
		// --approve to trust project-local resources for this one run.
		return nil, nil
	default:
		return nil, nil
	}
}

func claudeUnattendedSettings(workingDir string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(workingDir))
	if err != nil {
		return "", fmt.Errorf("resolve Claude workspace: %w", err)
	}
	absolute = filepath.ToSlash(filepath.Clean(absolute))
	if !strings.HasPrefix(absolute, "/") {
		return "", fmt.Errorf("Claude workspace must be an absolute path")
	}
	workspaceRule := "/" + strings.TrimSuffix(absolute, "/") + "/**"
	settings := map[string]any{
		"permissions": map[string]any{
			"allow": []string{
				"Bash",
				"Read(" + workspaceRule + ")",
				"Edit(" + workspaceRule + ")",
				"Write(" + workspaceRule + ")",
				"WebFetch",
				"WebSearch",
			},
		},
		"sandbox": map[string]any{
			"enabled":                  true,
			"autoAllowBashIfSandboxed": true,
			"allowUnsandboxedCommands": false,
			"failIfUnavailable":        true,
		},
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode Claude unattended settings: %w", err)
	}
	return string(encoded), nil
}

func closeProviderSession(provider llmproviders.Provider, ownerSessionID, reason string) {
	switch provider {
	case llmproviders.ProviderClaudeCode:
		llmproviders.CloseClaudeCodeInteractiveSessionForOwner(ownerSessionID, reason)
	case llmproviders.ProviderCodexCLI:
		llmproviders.CloseCodexCLIInteractiveSessionForOwner(ownerSessionID, reason)
	case llmproviders.ProviderCursorCLI:
		llmproviders.CloseCursorCLIInteractiveSessionForOwner(ownerSessionID, reason)
	case llmproviders.ProviderPiCLI:
		llmproviders.ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason)
	}
}

func jobUsage(usage *llmtypes.Usage) *Usage {
	if usage == nil {
		return nil
	}
	return &Usage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	}
}

func progressFromChunk(chunk llmtypes.StreamChunk) ProgressUpdate {
	update := ProgressUpdate{}
	if chunk.Metadata != nil {
		update.TmuxSession, _ = chunk.Metadata["tmux_session"].(string)
	}
	switch chunk.Type {
	case llmtypes.StreamChunkTypeToolCallStart:
		if chunk.ToolName != "" {
			update.Message = "Using tool " + chunk.ToolName
		}
	case llmtypes.StreamChunkTypeToolCallEnd:
		if chunk.ToolName != "" {
			update.Message = "Finished tool " + chunk.ToolName
		}
	case llmtypes.StreamChunkTypeStatusLine:
		if chunk.StatusLine != nil {
			if update.TmuxSession == "" && chunk.StatusLine.Metadata != nil {
				update.TmuxSession, _ = chunk.StatusLine.Metadata["tmux_session"].(string)
			}
			model := strings.TrimSpace(chunk.StatusLine.Model)
			if model != "" {
				update.Message = "Coding agent is running with " + model
			} else {
				update.Message = "Coding agent is running"
			}
		}
	case llmtypes.StreamChunkTypeContent, llmtypes.StreamChunkTypeTerminal:
		update.Message = lastMeaningfulLine(chunk.Content)
	}
	return update
}

func sanitizeProgress(message string) string {
	message = ansiEscapePattern.ReplaceAllString(message, "")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[len(runes)-240:])
		if index := strings.IndexByte(message, ' '); index >= 0 {
			message = message[index+1:]
		}
	}
	return strings.TrimSpace(message)
}

func lastMeaningfulLine(content string) string {
	content = ansiEscapePattern.ReplaceAllString(content, "")
	lines := strings.Split(content, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := sanitizeProgress(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

type writerLogger struct {
	logger *log.Logger
	mu     sync.Mutex
}

func newWriterLogger(writer io.Writer) *writerLogger {
	if writer == nil {
		writer = io.Discard
	}
	return &writerLogger{logger: log.New(writer, "", log.LstdFlags|log.Lmicroseconds)}
}

func (l *writerLogger) Infof(format string, values ...any) {
	l.printf("INFO", format, values...)
}

func (l *writerLogger) Errorf(format string, values ...any) {
	l.printf("ERROR", format, values...)
}

func (l *writerLogger) Debugf(format string, values ...any) {
	l.printf("DEBUG", format, values...)
}

func (l *writerLogger) printf(level, format string, values ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Printf(level+" "+format, values...)
}
