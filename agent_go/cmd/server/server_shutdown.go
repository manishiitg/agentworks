package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

func beginShutdownProgress(label string) (time.Time, func()) {
	start := time.Now()
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("⏳ Still %s (%s elapsed)\n", label, time.Since(start).Round(time.Second))
			}
		}
	}()
	return start, func() {
		once.Do(func() {
			close(done)
		})
	}
}

func cleanupCodingAgentInteractiveSessions(phase string) {
	cleanupProvider := func(name string, cleanup func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start, stopProgress := beginShutdownProgress(fmt.Sprintf("cleaning %s", name))
		fmt.Printf("  • %s cleanup started (5s max)\n", name)
		if err := cleanup(ctx); err != nil {
			stopProgress()
			elapsed := time.Since(start).Round(time.Millisecond)
			log.Printf("[%s] %s cleanup failed after %s: %v", name, phase, elapsed, err)
			fmt.Printf("  ⚠️ %s cleanup failed after %s: %v\n", name, elapsed, err)
			return
		}
		if ctx.Err() != nil {
			stopProgress()
			elapsed := time.Since(start).Round(time.Millisecond)
			log.Printf("[%s] %s cleanup context ended after %s: %v", name, phase, elapsed, ctx.Err())
			fmt.Printf("  ⚠️ %s cleanup context ended after %s: %v\n", name, elapsed, ctx.Err())
			return
		}
		stopProgress()
		fmt.Printf("  ✅ %s cleanup done (%s)\n", name, time.Since(start).Round(time.Millisecond))
	}

	cleanupProvider("CLAUDE-CODE", cleanupClaudeCodeProviderSessions)
	cleanupProvider("CODEX-CLI", cleanupCodexCLIProviderSessions)
	cleanupProvider("CURSOR-CLI", cleanupCursorCLIProviderSessions)
	cleanupProvider("PI-CLI", cleanupPiCLIProviderSessions)
}

func (api *StreamingAPI) cancelActiveWorkForShutdown() {
	if api == nil {
		return
	}

	// Set the cause before ordinary cancel funcs run. Context cancellation keeps
	// its first cause, so an earlier person Stop never becomes a restart.
	api.shutdownTurnCancels.Range(func(_, value interface{}) bool {
		value.(context.CancelCauseFunc)(errAppRestart)
		return true
	})
	var agentCancels []context.CancelFunc
	api.agentCancelMux.Lock()
	for sessionID, cancelFunc := range api.agentCancelFuncs {
		if cancelFunc != nil {
			agentCancels = append(agentCancels, cancelFunc)
		}
		delete(api.agentCancelFuncs, sessionID)
	}
	api.agentCancelMux.Unlock()
	for _, cancelFunc := range agentCancels {
		cancelFunc()
	}

	var workflowCancels []context.CancelFunc
	api.workflowOrchestratorContextMux.Lock()
	for queryID, cancelFunc := range api.workflowOrchestratorContexts {
		if cancelFunc != nil {
			workflowCancels = append(workflowCancels, cancelFunc)
		}
		delete(api.workflowOrchestratorContexts, queryID)
	}
	api.workflowOrchestratorContextMux.Unlock()
	for _, cancelFunc := range workflowCancels {
		cancelFunc()
	}

	sessionIDs := make([]string, 0)
	seen := make(map[string]struct{})
	api.activeSessionsMux.RLock()
	for sessionID := range api.activeSessions {
		seen[sessionID] = struct{}{}
		sessionIDs = append(sessionIDs, sessionID)
	}
	api.activeSessionsMux.RUnlock()

	api.sessionQueryIDMux.Lock()
	for sessionID := range api.sessionQueryIDs {
		if _, ok := seen[sessionID]; !ok {
			seen[sessionID] = struct{}{}
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	api.sessionQueryIDs = map[string][]string{}
	api.sessionQueryIDMux.Unlock()

	for _, sessionID := range sessionIDs {
		api.cancelBackgroundAgents(sessionID)
		api.cancelTrackedExecutionsForSession(sessionID)
		api.setSessionBusy(sessionID, false)
		api.setSyntheticTurn(sessionID, false)
	}

	api.workshopChatSessions.Range(func(key, value interface{}) bool {
		if ws, ok := value.(interface{ Close() }); ok {
			ws.Close()
		}
		api.workshopChatSessions.Delete(key)
		return true
	})
}
