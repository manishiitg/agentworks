package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// Claude stores transcripts below a project directory derived from the CLI's
// working directory. The exact escaping has changed across Claude Code
// versions, so the working-directory candidate is the fast path and the
// session-ID glob is retained only as a compatibility fallback.
//
// Cache keys include HOME because tests and embedded runtimes can change it
// between calls while reusing a session ID. Cwd is included because a native
// conversation can move to another project folder and retain that same ID.
var claudeTranscriptPathCache = struct {
	sync.RWMutex
	paths map[string]string
}{paths: make(map[string]string)}

const maxClaudeTranscriptPathCacheEntries = 512

// Kept as variables so regression tests can prove the hot polling path does
// not repeatedly glob or reopen the transcript.
var claudeTranscriptGlob = filepath.Glob
var claudeTranscriptOpen = os.Open

func resolveClaudeTranscriptPath(sessionID, workingDir string, allowGlobalFallback bool, accountHome ...string) (string, error) {
	if !isClaudeTranscriptSessionID(sessionID) {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if len(accountHome) > 0 && accountHome[0] != "" {
		home = accountHome[0]
	}
	cacheKey := claudeTranscriptCacheKey(home, sessionID, workingDir)
	if path := cachedClaudeTranscriptPath(cacheKey); path != "" {
		return path, nil
	}

	for _, path := range claudeTranscriptWorkingDirCandidates(home, workingDir, sessionID) {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			cacheClaudeTranscriptPath(cacheKey, path)
			return path, nil
		}
	}

	if !allowGlobalFallback {
		return "", nil
	}
	matches, err := claudeTranscriptGlob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	cacheClaudeTranscriptPath(cacheKey, matches[0])
	return matches[0], nil
}

func openClaudeTranscript(sessionID, workingDir string, accountHome ...string) (*os.File, string, error) {
	path, err := resolveClaudeTranscriptPath(sessionID, workingDir, true, accountHome...)
	if err != nil || path == "" {
		return nil, path, err
	}
	f, err := claudeTranscriptOpen(path)
	if err == nil {
		return f, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, path, err
	}

	// A cached transcript can disappear if Claude replaces a session or a test
	// swaps HOME. Forget only this exact mapping and resolve once more.
	forgetClaudeTranscriptPath(sessionID, path, accountHome...)
	path, resolveErr := resolveClaudeTranscriptPath(sessionID, workingDir, true, accountHome...)
	if resolveErr != nil || path == "" {
		return nil, path, resolveErr
	}
	f, err = claudeTranscriptOpen(path)
	return f, path, err
}

func claudeTranscriptWorkingDirCandidates(home, workingDir, sessionID string) []string {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil
	}

	dirs := pathidentity.Candidates(workingDir)

	seen := make(map[string]struct{}, len(dirs))
	paths := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		slug := claudeTranscriptProjectSlug(dir)
		if slug == "" {
			continue
		}
		path := filepath.Join(home, ".claude", "projects", slug, sessionID+".jsonl")
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func claudeTranscriptProjectSlug(workingDir string) string {
	workingDir = filepath.Clean(strings.TrimSpace(workingDir))
	if workingDir == "" || workingDir == "." {
		return ""
	}
	return strings.NewReplacer(
		"/", "-",
		"\\", "-",
		"_", "-",
		".", "-",
		":", "-",
	).Replace(workingDir)
}

func claudeTranscriptCacheKey(home, sessionID, workingDir string) string {
	if physical, e := filepath.EvalSymlinks(workingDir); e == nil {
		workingDir = physical
	}
	return filepath.Clean(home) + "\x00" + sessionID + "\x00" + filepath.Clean(workingDir)
}

func cachedClaudeTranscriptPath(cacheKey string) string {
	claudeTranscriptPathCache.RLock()
	defer claudeTranscriptPathCache.RUnlock()
	return claudeTranscriptPathCache.paths[cacheKey]
}

func cacheClaudeTranscriptPath(cacheKey, path string) {
	claudeTranscriptPathCache.Lock()
	if _, exists := claudeTranscriptPathCache.paths[cacheKey]; !exists && len(claudeTranscriptPathCache.paths) >= maxClaudeTranscriptPathCacheEntries {
		// Paths are cheap to rediscover from workingDir. Bound this compatibility
		// cache so a long-lived server executing unique sessions cannot turn the
		// performance fix into another session-lifetime leak.
		for evictKey := range claudeTranscriptPathCache.paths {
			delete(claudeTranscriptPathCache.paths, evictKey)
			break
		}
	}
	claudeTranscriptPathCache.paths[cacheKey] = path
	claudeTranscriptPathCache.Unlock()
}

func forgetClaudeTranscriptPath(sessionID, path string, accountHome ...string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if len(accountHome) > 0 && accountHome[0] != "" {
		home = accountHome[0]
	}
	prefix := filepath.Clean(home) + "\x00" + sessionID + "\x00"
	claudeTranscriptPathCache.Lock()
	for key, saved := range claudeTranscriptPathCache.paths {
		if strings.HasPrefix(key, prefix) && saved == path {
			delete(claudeTranscriptPathCache.paths, key)
		}
	}
	claudeTranscriptPathCache.Unlock()
}
