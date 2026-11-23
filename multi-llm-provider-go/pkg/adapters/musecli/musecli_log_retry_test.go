package musecli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-746-961: 250ms polls of absent journals traversed all dated directories.
func TestMissingMuseLogSearchBacksOffAndResetsForNewAccount(t *testing.T) {
	var retry museLogPathRetry
	now := time.Unix(0, 0)
	calls := 0
	lookup := func() string { calls++; return "" }
	for i := 0; i < 240; i++ {
		retry.resolveAt(now.Add(time.Duration(i)*250*time.Millisecond), "account-a/native", lookup)
	}
	// Searches at 0,1,3,7,15,31 seconds, rather than all 240 UI polls.
	if calls != 6 || retry.delay != 30*time.Second {
		t.Fatalf("searches=%d delay=%s", calls, retry.delay)
	}
	if got := retry.resolveAt(now.Add(60*time.Second), "account-b/native", func() string { calls++; return "/account-b/log" }); got != "/account-b/log" {
		t.Fatal("new account delayed by previous account's missing log")
	}
	if retry.delay != 0 || !retry.next.IsZero() {
		t.Fatal("found log did not reset backoff")
	}
	t.Logf("missing log: before=240 directory searches/minute; after=%d in first minute, then at most2/minute", calls-1)
}

func BenchmarkMissingMuseLogPolling(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 60; i++ {
		p := filepath.Join(root, "muse", "sessions", fmt.Sprintf("%04d/%02d/%02d", 2020+i/36, 1+i%12, 1+i%28), fmt.Sprintf("session-%d", i))
		if err := os.MkdirAll(p, 0700); err != nil {
			b.Fatal(err)
		}
	}

	for _, backoff := range []bool{false, true} {
		b.Run(fmt.Sprintf("backoff_%t", backoff), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				var retry museLogPathRetry
				now := time.Unix(0, 0)
				calls := 0
				for i := 0; i < 240; i++ {
					if backoff {
						retry.resolveAt(now.Add(time.Duration(i)*250*time.Millisecond), "account/native", func() string { calls++; return museSessionLogPath("missing-session", root) })
					} else {
						calls++
						museSessionLogPath("missing-session", root)
					}
				}
				if backoff && calls != 6 {
					b.Fatal(calls)
				}
			}
		})
	}
}
