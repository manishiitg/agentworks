package server

import (
	"context"
	"strings"
	"testing"
)

func TestServerProductManagementAuditFinishesAfterDisconnect(t *testing.T) {
	t.Setenv("AUTH_SECRET", strings.Repeat("a", 32))
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	store, receipt, err := startServerProductManagementAudit(ctx, "actor", "client", "llm-gateway", "set_model_access")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cancel()
	if err := finishServerProductManagementAudit(store, receipt, 502); err != nil {
		t.Fatal("disconnection lost receipt", err)
	}
	var status int
	var finished int64
	if err := store.db.QueryRow(`SELECT status,finished_at FROM server_product_management_audit WHERE id=?`, receipt).Scan(&status, &finished); err != nil || status != 502 || finished == 0 {
		t.Fatal("receipt was not finalized", status, finished, err)
	}
}
