package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

func cliOAuthTestServer(handler http.Handler, accessToken string) *httptest.Server {
	var address string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/cli/device":
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device_fixture", "verification_uri_complete": address + "/oauth/cli?code=verification_fixture", "user_code": "EXAMPLE", "expires_in": 30, "interval": 1})
		case "/api/oauth/cli/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessToken, "refresh_token": "aw_cli_refresh_fixture", "expires_in": 3600})
		case "/api/oauth/cli/revoke":
			_ = json.NewEncoder(w).Encode(map[string]any{"revoked": true})
		default:
			handler.ServeHTTP(w, r)
		}
	}))
	address = server.URL
	return server
}
