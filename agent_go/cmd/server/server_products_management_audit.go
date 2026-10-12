package server

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Management receipts live in the host's protected auth database, outside all
// workspace/agent file trees. They contain names and status only, never args,
// output, URLs, secrets or raw credentials. Started rows survive a process crash.
func startServerProductManagementAudit(ctx context.Context, actor, client, product, tool string) (*mcpOAuthStore, string, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, "", err
	}
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS server_product_management_audit (
 id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, client_id TEXT NOT NULL,
 product TEXT NOT NULL, tool TEXT NOT NULL, started_at INTEGER NOT NULL,
 finished_at INTEGER NOT NULL DEFAULT 0, status INTEGER NOT NULL DEFAULT 0)`)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `DELETE FROM server_product_management_audit WHERE started_at<? OR id IN (
 SELECT id FROM server_product_management_audit WHERE finished_at>0 ORDER BY started_at DESC,id DESC LIMIT -1 OFFSET 99999)`, time.Now().Add(-30*24*time.Hour).UnixMilli())
	}
	id := uuid.NewString()
	if err == nil {
		_, err = s.db.ExecContext(ctx, `INSERT INTO server_product_management_audit (id,actor_id,client_id,product,tool,started_at) VALUES (?,?,?,?,?,?)`, id, actor, client, product, tool, time.Now().UnixMilli())
	}
	if err != nil {
		s.Close()
		return nil, "", err
	}
	return s, id, nil
}

func finishServerProductManagementAudit(s *mcpOAuthStore, id string, status int) error {
	// Complete the receipt even after a disconnected caller cancels its request.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `UPDATE server_product_management_audit SET finished_at=?,status=? WHERE id=?`, time.Now().UnixMilli(), status, id)
	return err
}
