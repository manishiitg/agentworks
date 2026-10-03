package pii

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func BenchmarkPIIScanJSON(b *testing.B) {
	for _, size := range []int{1024, 4096, 65536} {
		b.Run(fmt.Sprintf("%d_bytes", size), func(b *testing.B) {
			args := map[string]any{"message": strings.Repeat("ordinary text. ", size/15) + " alice@example.com"}
			scope := Scope{WorkspaceID: "benchmark", Direction: Input}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			samples := make([]time.Duration, b.N)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				_, decision, err := ScanJSON(args, scope, nil)
				samples[i] = time.Since(start)
				if err != nil || decision.Action != Mask {
					b.Fatal("unexpected scan decision", err)
				}
			}
			b.StopTimer()
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			if len(samples) > 0 {
				b.ReportMetric(float64(samples[(len(samples)-1)*95/100].Nanoseconds())/1000, "p95-us")
				b.ReportMetric(float64(samples[(len(samples)-1)*99/100].Nanoseconds())/1000, "p99-us")
			}
		})
	}
}
