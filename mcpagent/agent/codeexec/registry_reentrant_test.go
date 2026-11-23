package codeexec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A route can run a child script over HTTP and clean up its registry before
// returning. Neither the parent executor nor another session may hold it up.
func TestToolExecutorAllowsNestedSessionCleanup(t *testing.T) {
	for _, kind := range []string{"custom", "virtual"} {
		t.Run(kind, func(t *testing.T) {
			resetRegistryForTest(t)
			cleanupStarted := make(chan struct{})
			probeDone := make(chan error, 1)
			InitRegistryForSession("child", map[string]func(context.Context, map[string]interface{}) (string, error){
				"read": func(context.Context, map[string]interface{}) (string, error) { return "child-result", nil },
			}, nil)
			InitRegistryForSession("other", map[string]func(context.Context, map[string]interface{}) (string, error){
				"read": func(context.Context, map[string]interface{}) (string, error) { return "other-result", nil },
			}, nil)
			child := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result, err := CallCustomToolWithSession(r.Context(), "child", "read", nil)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				close(cleanupStarted)
				CleanupSession("child")
				_, _ = io.WriteString(w, result)
			}))
			defer child.Close()
			executor := func(ctx context.Context, _ map[string]interface{}) (string, error) {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, child.URL, nil)
				resp, err := child.Client().Do(req)
				if err != nil {
					return "", err
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				return string(b), err
			}
			tools := map[string]func(context.Context, map[string]interface{}) (string, error){"route": executor}
			call := CallCustomToolWithSession
			if kind == "virtual" {
				InitRegistryVirtualToolsForSession("parent", tools, nil)
				call = CallVirtualToolWithSession
			} else {
				InitRegistryForSession("parent", tools, nil)
			}
			go func() {
				<-cleanupStarted
				result, err := CallCustomToolWithSession(context.Background(), "other", "read", nil)
				if err == nil && result != "other-result" {
					err = fmt.Errorf("unexpected other session result %q", result)
				}
				probeDone <- err
			}()
			result, err := call(context.Background(), "parent", "route", nil)
			if err != nil || result != "child-result" {
				t.Fatalf("nested call = %q, %v", result, err)
			}
			if err := <-probeDone; err != nil {
				t.Fatal(err)
			}
			if _, err := CallCustomToolWithSession(context.Background(), "child", "read", nil); err == nil {
				t.Fatal("cleaned child retained its executor")
			}
		})
	}
}
