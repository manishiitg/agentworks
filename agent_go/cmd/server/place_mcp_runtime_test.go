package server

import "testing"

// connectCodeForTest adds a server to a Code as its owner's connection.
func connectCodeForTest(t *testing.T, owner, root, name string) string {
	t.Helper()
	store := placeMCPStoreID(owner, root)
	if _, err := addPlaceMCPServer(store, placeMCPServer{Name: name, URL: "https://mcp.deepwiki.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := recordPlaceMCP(owner, name, root); err != nil {
		t.Fatal(err)
	}
	internal, _, err := placeMCPServerConfig(store, name)
	if err != nil {
		t.Fatal(err)
	}
	return internal
}
