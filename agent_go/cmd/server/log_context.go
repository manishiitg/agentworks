package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const (
	missingServerLogContextValue = "-"
	maxServerLogContextEntries   = 8192
)

type serverLogContext struct {
	Workflow string
	Group    string
	Mode     string
	UserID   string
	Username string
	Session  string
}

func normalizeServerLogMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "workflow", "workflow_phase", "workshop":
		return "workflow"
	case "simple", "multi-agent":
		return "multi-agent"
	default:
		return strings.TrimSpace(mode)
	}
}

func workflowLogName(workspacePath string) string {
	trimmed := strings.TrimSpace(workspacePath)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return ""
	}
	return filepath.Base(cleaned)
}

func singleSelectedGroupName(groupNames []string) string {
	normalized := normalizeScheduleGroupNames(groupNames)
	if len(normalized) != 1 {
		return ""
	}
	return normalized[0]
}

func newServerLogContext(workspacePath, groupName, mode, userID, username, sessionID string) serverLogContext {
	return serverLogContext{
		Workflow: workflowLogName(workspacePath),
		Group:    strings.TrimSpace(groupName),
		Mode:     normalizeServerLogMode(mode),
		UserID:   strings.TrimSpace(userID),
		Username: strings.TrimSpace(username),
		Session:  strings.TrimSpace(sessionID),
	}
}

func requestLogContext(ctx context.Context, req QueryRequest, sessionID string) serverLogContext {
	user := GetUserFromContext(ctx)
	userID := ""
	username := ""
	if user != nil {
		userID = user.UserID
		username = user.Username
	}

	groupName := ""
	if req.ExecutionOptions != nil {
		groupName = singleSelectedGroupName(req.ExecutionOptions.EnabledGroupNames)
	}

	return newServerLogContext(req.SelectedFolder, groupName, req.AgentMode, userID, username, sessionID)
}

func (api *StreamingAPI) httpRequestLogContext(r *http.Request) serverLogContext {
	if r == nil {
		return serverLogContext{}
	}
	user := GetUserFromContext(r.Context())
	userID := ""
	username := ""
	if user != nil {
		userID = user.UserID
		username = user.Username
	}

	sessionID := strings.TrimSpace(r.Header.Get("X-Session-ID"))
	if sessionID == "" {
		for _, key := range []string{"session_id", "session"} {
			if sessionID = strings.TrimSpace(r.URL.Query().Get(key)); sessionID != "" {
				break
			}
		}
	}
	workspacePath := ""
	for _, key := range []string{"workspace_path", "selected_folder", "workflow"} {
		if workspacePath = strings.TrimSpace(r.URL.Query().Get(key)); workspacePath != "" {
			break
		}
	}
	mode := ""
	if api != nil && sessionID != "" {
		api.activeSessionsMux.RLock()
		active := api.activeSessions[sessionID]
		if active != nil {
			if workspacePath == "" {
				workspacePath = active.WorkspacePath
				if workspacePath == "" {
					workspacePath = active.WorkflowName
				}
			}
			mode = active.AgentMode
			if userID == "" {
				userID = active.UserID
			}
			if username == "" {
				username = active.Username
			}
		}
		api.activeSessionsMux.RUnlock()
	}
	if username == "" && userID != "" {
		username = logUsernameForUserID(userID)
	}
	return newServerLogContext(workspacePath, "", mode, userID, username, sessionID)
}

func scheduleLogContext(sctx *ScheduleContext) serverLogContext {
	if sctx == nil {
		return serverLogContext{}
	}

	return newServerLogContext(
		sctx.WorkspacePath,
		singleSelectedGroupName(sctx.Schedule.GroupNames),
		"workflow",
		sctx.OwnerUserID,
		logUsernameForUserID(sctx.OwnerUserID),
		"",
	)
}

func logUsernameForUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	if rec := directoryUserFor(userID, "", ""); rec != nil && strings.TrimSpace(rec.Username) != "" {
		return strings.TrimSpace(rec.Username)
	}
	return userID
}

func (lc serverLogContext) WithSession(sessionID string) serverLogContext {
	lc.Session = strings.TrimSpace(sessionID)
	return lc
}

func (lc serverLogContext) WithWorkflow(workspacePath string) serverLogContext {
	lc.Workflow = workflowLogName(workspacePath)
	return lc
}

func (lc serverLogContext) WithGroup(groupName string) serverLogContext {
	lc.Group = strings.TrimSpace(groupName)
	return lc
}

// Context carries the diagnostic identity through detached/background agent
// contexts. These values are deliberately metadata only: authorization still
// uses UserIDKey and the existing workspace/session guards.
func (lc serverLogContext) Context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, common.UsernameKey, logContextValue(lc.Username))
	ctx = context.WithValue(ctx, common.WorkflowNameKey, logContextValue(lc.Workflow))
	return ctx
}

// Logger scopes the shared structured logger to this request/session. The
// fallback fields on the base logger are overwritten because loggerv2 resolves
// duplicate keys in favor of the child logger's later fields.
func (lc serverLogContext) Logger(base loggerv2.Logger) loggerv2.Logger {
	if base == nil {
		return nil
	}
	return base.With(lc.Fields()...)
}

func logContextValue(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return missingServerLogContextValue
}

func (lc serverLogContext) Fields() []loggerv2.Field {
	fields := make([]loggerv2.Field, 0, 7)
	// Keep these first-class and unconditional so every structured entry has
	// stable search keys, even for startup/global work where no user or workflow
	// genuinely exists.
	fields = append(fields,
		loggerv2.String("username", logContextValue(lc.Username)),
		loggerv2.String("workflow", logContextValue(lc.Workflow)),
	)
	if lc.Group != "" {
		fields = append(fields, loggerv2.String("group", lc.Group))
	}
	if lc.Mode != "" {
		fields = append(fields, loggerv2.String("mode", lc.Mode))
	}
	if lc.Username != "" {
		// Retain the older user field for dashboards while username becomes the
		// canonical human-readable account field.
		fields = append(fields, loggerv2.String("user", lc.Username))
	}
	if lc.UserID != "" {
		fields = append(fields, loggerv2.String("user_id", lc.UserID))
	}
	if lc.Session != "" {
		fields = append(fields, loggerv2.String("session", lc.Session))
	}
	return fields
}

func (lc serverLogContext) Prefix() string {
	parts := make([]string, 0, 7)
	parts = append(parts,
		"username="+formatServerLogValue(logContextValue(lc.Username)),
		"workflow="+formatServerLogValue(logContextValue(lc.Workflow)),
	)
	if lc.Group != "" {
		parts = append(parts, fmt.Sprintf("group=%s", lc.Group))
	}
	if lc.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%s", lc.Mode))
	}
	if lc.Username != "" {
		parts = append(parts, "user="+formatServerLogValue(lc.Username))
	}
	if lc.UserID != "" {
		parts = append(parts, fmt.Sprintf("user_id=%s", lc.UserID))
	}
	if lc.Session != "" {
		parts = append(parts, fmt.Sprintf("session=%s", lc.Session))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func formatServerLogValue(value string) string {
	if value == "" {
		return missingServerLogContextValue
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("-._/:@", r) {
			continue
		}
		return strconv.Quote(value)
	}
	return value
}

func formatContextLog(logCtx serverLogContext, format string, args ...interface{}) string {
	message := fmt.Sprintf(format, args...)
	if prefix := logCtx.Prefix(); prefix != "" {
		return prefix + " " + message
	}
	return message
}

func logfWithContext(logCtx serverLogContext, format string, args ...interface{}) {
	log.Print(formatContextLog(logCtx, format, args...))
}

type registeredServerLogContext struct {
	Context   serverLogContext
	UpdatedAt time.Time
}

var serverLogContextRegistry = struct {
	sync.RWMutex
	entries map[string]registeredServerLogContext
}{entries: make(map[string]registeredServerLogContext)}

// registerServerLogContext lets the process-wide standard logger recover
// request identity from the session/query identifiers that downstream browser,
// workspace, and transport packages already include in most useful messages.
func registerServerLogContext(logCtx serverLogContext, identifiers ...string) {
	now := time.Now()
	serverLogContextRegistry.Lock()
	for _, identifier := range identifiers {
		if identifier = strings.TrimSpace(identifier); identifier != "" {
			serverLogContextRegistry.entries[identifier] = registeredServerLogContext{Context: logCtx, UpdatedAt: now}
		}
	}
	if len(serverLogContextRegistry.entries) > maxServerLogContextEntries {
		pruneServerLogContextRegistryLocked(maxServerLogContextEntries * 3 / 4)
	}
	serverLogContextRegistry.Unlock()
}

func pruneServerLogContextRegistryLocked(target int) {
	type contextAge struct {
		identifier string
		updatedAt  time.Time
	}
	ages := make([]contextAge, 0, len(serverLogContextRegistry.entries))
	for identifier, entry := range serverLogContextRegistry.entries {
		ages = append(ages, contextAge{identifier: identifier, updatedAt: entry.UpdatedAt})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].updatedAt.Before(ages[j].updatedAt) })
	remove := len(ages) - target
	for index := 0; index < remove; index++ {
		delete(serverLogContextRegistry.entries, ages[index].identifier)
	}
}

func lookupServerLogContext(identifier string) (serverLogContext, bool) {
	identifier = strings.TrimSpace(identifier)
	serverLogContextRegistry.RLock()
	defer serverLogContextRegistry.RUnlock()
	for candidate := identifier; candidate != ""; {
		if entry, ok := serverLogContextRegistry.entries[candidate]; ok {
			return entry.Context, true
		}
		separator := strings.LastIndex(candidate, ":")
		if separator < 0 {
			break
		}
		candidate = candidate[:separator]
	}
	return serverLogContext{}, false
}

type serverLogContextWriter struct {
	destination io.Writer
}

func newServerLogContextWriter(destination io.Writer) io.Writer {
	if destination == nil {
		destination = io.Discard
	}
	return &serverLogContextWriter{destination: destination}
}

func (w *serverLogContextWriter) Write(p []byte) (int, error) {
	parts := bytes.SplitAfter(p, []byte("\n"))
	var enriched bytes.Buffer
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		hasNewline := part[len(part)-1] == '\n'
		line := strings.TrimSuffix(string(part), "\n")
		enriched.WriteString(enrichServerLogLine(line))
		if hasNewline {
			enriched.WriteByte('\n')
		}
	}
	written, err := w.destination.Write(enriched.Bytes())
	if err != nil {
		return 0, err
	}
	if written != enriched.Len() {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

func enrichServerLogLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}
	logCtx := serverLogContext{}
	for _, key := range []string{"session_id", "session", "query_id"} {
		if identifier := serverLogFieldValue(line, key); identifier != "" {
			if registered, ok := lookupServerLogContext(identifier); ok {
				logCtx = registered
				break
			}
		}
	}

	missingUsername := !hasServerLogField(line, "username")
	missingWorkflow := !hasServerLogField(line, "workflow")
	if !missingUsername && !missingWorkflow {
		return line
	}
	if missingUsername && logCtx.Username == "" {
		logCtx.Username = serverLogFieldValue(line, "user")
	}

	fields := make([]string, 0, 2)
	if missingUsername {
		fields = append(fields, "username="+formatServerLogValue(logContextValue(logCtx.Username)))
	}
	if missingWorkflow {
		fields = append(fields, "workflow="+formatServerLogValue(logContextValue(logCtx.Workflow)))
	}
	return strings.TrimRight(line, " \t") + " " + strings.Join(fields, " ")
}

func hasServerLogField(line, key string) bool {
	return serverLogFieldValue(line, key) != ""
}

func serverLogFieldValue(line, key string) string {
	needle := key + "="
	for searchFrom := 0; searchFrom < len(line); {
		index := strings.Index(line[searchFrom:], needle)
		if index < 0 {
			return ""
		}
		index += searchFrom
		if index > 0 && isServerLogFieldChar(line[index-1]) {
			searchFrom = index + len(needle)
			continue
		}
		valueStart := index + len(needle)
		if valueStart >= len(line) {
			return ""
		}
		if line[valueStart] == '"' {
			for end := valueStart + 1; end < len(line); end++ {
				if line[end] == '"' && line[end-1] != '\\' {
					value, err := strconv.Unquote(line[valueStart : end+1])
					if err == nil {
						return value
					}
					return strings.Trim(line[valueStart:end+1], "\"")
				}
			}
			return ""
		}
		valueEnd := valueStart
		for valueEnd < len(line) && isServerLogFieldChar(line[valueEnd]) {
			valueEnd++
		}
		if valueEnd > valueStart {
			return line[valueStart:valueEnd]
		}
		return ""
	}
	return ""
}

func isServerLogFieldChar(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || strings.ContainsRune("-._/:@", rune(char))
}

var installDefaultServerLogContextWriterOnce sync.Once

func installDefaultServerLogContextWriter() {
	installDefaultServerLogContextWriterOnce.Do(func() {
		log.SetOutput(newServerLogContextWriter(log.Writer()))
	})
}
