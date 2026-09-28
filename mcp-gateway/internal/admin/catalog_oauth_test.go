package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
)

func TestOAuthCatalogProviderIsNotOfferedAsConnectable(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range cat.Providers {
		if !provider.OAuth {
			continue
		}
		adm := &Admin{Catalog: cat}
		_, err := adm.AddConnectorFromCatalog(context.Background(), provider.Name, "", "")
		if err == nil || !strings.Contains(err.Error(), "requires upstream OAuth") {
			t.Fatalf("OAuth provider %q was not rejected clearly: %v", provider.Name, err)
		}
		return
	}
	t.Fatal("catalog has no OAuth providers")
}
