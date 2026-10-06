package knowledgebase

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReserveIntegrationRequest shares the public tool/request-ID namespace with
// ordinary content mutations. Integration effects are checkpointed by their
// server-owned receipt; a reservation never claims the effect has completed.
func (s *Service) ReserveIntegrationRequest(ctx context.Context, p Principal, tool string, args map[string]any) (string, error) {
	// Same key as CallTool, which converts a legacy tool name first (PLAT-608).
	tool = CanonicalToolName(tool)
	unlock, err := s.lock(ctx, true)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := s.recover(); err != nil {
		return "", err
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		return "", kbErr("FORBIDDEN", "Identity is unavailable.")
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return "", err
		}
	}
	path, hash, err := s.requestPath(p, tool, args)
	if err != nil {
		return "", err
	}
	if _, _, err := s.cachedRequest(p, tool, args, path, hash); err != nil {
		return "", err
	}
	intent := filepath.Join(s.private, "integration-requests", filepath.Base(path))
	if err := s.checkIntegrationRequest(intent, hash); err != nil {
		return "", err
	}
	if b, readErr := os.ReadFile(intent); readErr == nil {
		var record requestRecord
		if json.Unmarshal(b, &record) == nil {
			at, _ := time.Parse(time.RFC3339Nano, record.At)
			if time.Since(at) <= 7*24*time.Hour {
				return strings.TrimSuffix(filepath.Base(path), ".json"), nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(intent), 0700); err != nil {
		return "", err
	}
	if err := s.transact([]fileChange{jsonChange(intent, requestRecord{Hash: hash, At: stamp()})}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(filepath.Base(path), ".json"), nil
}

func (s *Service) checkIntegrationRequest(path, hash string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var record requestRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, record.At)
	if err != nil {
		return err
	}
	if time.Since(at) <= 7*24*time.Hour && record.Hash != hash {
		return kbErr("REQUEST_ID_REUSE", "Request ID was reused with different arguments.")
	}
	return nil
}
