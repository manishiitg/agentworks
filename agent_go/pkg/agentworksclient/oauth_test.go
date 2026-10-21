package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceAuthorizationAcceptsLocalFrontendAndRejectsRemoteOrigin(t *testing.T) {
	var verification atomic.Value
	verification.Store("http://127.0.0.1:51733/oauth/cli?code=cli_verify_" + strings.Repeat("a", 64))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(DeviceAuthorization{DeviceCode: "cli_device_example", VerificationURIComplete: verification.Load().(string), UserCode: "AAAAAAAA", ExpiresIn: 600, Interval: 3})
	}))
	defer s.Close()
	client, err := New(s.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartDeviceAuthorization(context.Background()); err != nil {
		t.Fatalf("local frontend approval rejected: %v", err)
	}
	verification.Store("https://example.com/oauth/cli?code=cli_verify_" + strings.Repeat("a", 64))
	if _, err := client.StartDeviceAuthorization(context.Background()); err == nil {
		t.Fatal("remote approval origin accepted for local login")
	}
}

func TestConfigTokenProviderSerializesRefreshAcrossProviders(t *testing.T) {
	var refreshes atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/cli/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "aw_cli_refresh_original" {
			t.Errorf("unexpected refresh credential")
		}
		refreshes.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"access_token":"aw_cli_rotated","refresh_token":"aw_cli_refresh_rotated","expires_in":3600}`))
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{Server: s.URL, Token: "aw_cli_expired", RefreshToken: "aw_cli_refresh_original", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	providers := []func(context.Context) (string, error){ConfigTokenProvider(s.URL, path), ConfigTokenProvider(s.URL, path)}
	var wg sync.WaitGroup
	for _, provider := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := provider(context.Background())
			if err != nil || got != "aw_cli_rotated" {
				t.Errorf("provider result %q: %v", got, err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refresh requests=%d, want 1", refreshes.Load())
	}
}

func TestConfigTokenProviderRecoversSavedLoginAfterRefreshRefusal(t *testing.T) {
	for _, scenario := range []string{"fresh login", "refresh new login", "unchanged", "signed out", "different server"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			var calls int
			var serverURL string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = r.ParseForm()
				if calls == 2 {
					if r.Form.Get("refresh_token") != "aw_cli_refresh_new" {
						t.Error("retried the rejected credential")
					}
					_, _ = w.Write([]byte(`{"access_token":"aw_cli_recovered","refresh_token":"aw_cli_refresh_recovered","expires_in":3600}`))
					return
				}
				latest := Config{Server: serverURL, Token: "aw_cli_new", RefreshToken: "aw_cli_refresh_new", ExpiresAt: time.Now().Add(time.Hour)}
				switch scenario {
				case "refresh new login":
					latest.ExpiresAt = time.Now().Add(-time.Minute)
				case "unchanged":
					latest.RefreshToken = "aw_cli_refresh_old"
				case "signed out":
					latest.Token, latest.RefreshToken = "", ""
				case "different server":
					latest.Server = "https://example.com"
				}
				if err := SaveConfig(path, latest); err != nil {
					t.Error(err)
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			}))
			defer s.Close()
			serverURL = s.URL
			if err := SaveConfig(path, Config{Server: s.URL, Token: "aw_cli_expired", RefreshToken: "aw_cli_refresh_old", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			got, err := ConfigTokenProvider(s.URL, path)(context.Background())
			want, wantCalls := "", 1
			switch scenario {
			case "fresh login":
				want = "aw_cli_new"
			case "refresh new login":
				want, wantCalls = "aw_cli_recovered", 2
			default:
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Code != "invalid_grant" {
					t.Fatalf("expected original refusal, got %v", err)
				}
			}
			if got != want || calls != wantCalls || want != "" && err != nil {
				t.Fatalf("token recovered=%t, calls=%d, error=%v; want recovered=%t, calls=%d", got != "", calls, err, want != "", wantCalls)
			}
			cfg, loadErr := LoadConfig(path)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if cfg.Token == "aw_cli_expired" || scenario == "different server" && cfg.Server != "https://example.com" || scenario == "signed out" && cfg.RefreshToken != "" {
				t.Fatal("overwrote the saved login after refusal")
			}
		})
	}
}
