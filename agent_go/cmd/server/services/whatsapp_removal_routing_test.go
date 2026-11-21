package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-886: previewing or removing an agent link must not start a phone.
func TestAgentRemovalRoutingDoesNotStartWhatsApp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	manager := NewWhatsAppServiceManager(root)
	routes, err := manager.RoutingForUser(ctx, "person")
	if err != nil || len(routes) != 0 {
		t.Fatalf("empty routing: %v %v", routes, err)
	}
	if _, err = os.Stat(manager.devicePath("person")); !os.IsNotExist(err) {
		t.Fatal("preview created a phone store")
	}
	path := manager.devicePath("person")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	svc := NewWhatsAppService(path)
	if err = svc.openMetaStore(ctx); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetRouting(WhatsAppRouting{"helper": {ProfileID: "work", ConversationKey: "crew:chat:agent"}}); err != nil {
		t.Fatal(err)
	}
	svc.closeMetaStore()
	routes, err = manager.RoutingForUser(ctx, "person")
	if err != nil || routes["helper"].ConversationKey != "crew:chat:agent" {
		t.Fatalf("saved routing: %v %v", routes, err)
	}
	delete(routes, "helper")
	if err = manager.SetRoutingForUser(ctx, "person", routes); err != nil {
		t.Fatal(err)
	}
	saved, err := manager.RoutingForUser(ctx, "person")
	if err != nil || len(saved) != 0 {
		t.Fatalf("removed routing: %v %v", saved, err)
	}
	if len(manager.services) != 0 {
		t.Fatal("route inspection/change started a phone service")
	}
}
