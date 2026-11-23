package codeexec

import (
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"strings"
	"testing"
)

type mappingCountLogger struct {
	loggerv2.Logger
	warnings, reuses int
}

func (l *mappingCountLogger) Warn(msg string, _ ...loggerv2.Field) {
	if strings.HasPrefix(msg, "Tool already mapped") {
		l.warnings++
	}
}
func (l *mappingCountLogger) Debug(msg string, _ ...loggerv2.Field) {
	if msg == "Tool mapping reused across delegation scopes" {
		l.reuses++
	}
}

// PLAT-746-876: new delegation hashes for the same provider are not new
// conflicts; log once per public mapping without changing connection routing.
func TestScopedMappingReuseLogsOnceWithoutChangingRouting(t *testing.T) {
	logger := &mappingCountLogger{Logger: loggerv2.NewNoop()}
	tool := "scope_log_regression_tool"
	original := "vault_log_example__scope_" + strings.Repeat("a", 32)
	InitRegistry(nil, nil, map[string]string{tool: original}, logger)
	for _, hash := range []string{"b", "b", "c", "d"} {
		InitRegistry(nil, nil, map[string]string{tool: "vault_log_example__scope_" + strings.Repeat(hash, 32)}, logger)
		InitRegistry(nil, nil, map[string]string{tool: "vault_log_other__scope_" + strings.Repeat(hash, 32)}, logger)
	}
	if logger.reuses != 1 || logger.warnings != 1 {
		t.Fatalf("repeated mapping logs: reuse=%d warnings=%d", logger.reuses, logger.warnings)
	}
	registry := GetRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.toolToServer[tool] != original {
		t.Fatal("log deduplication changed scoped routing")
	}
	delete(registry.toolToServer, tool)
	for key := range registry.reportedToolMappingConflicts {
		if strings.HasPrefix(key, tool+"\x00") {
			delete(registry.reportedToolMappingConflicts, key)
		}
	}
}
