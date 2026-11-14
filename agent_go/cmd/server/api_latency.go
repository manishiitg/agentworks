package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// apiLatencyStats keeps a bounded latency sample per route template so the
// server log can report p50/p95 for every API route, including the successful
// GETs that are not written as individual lines.
type apiLatencyStats struct {
	mu     sync.Mutex
	routes map[string]*apiRouteSamples
}

type apiRouteSamples struct {
	count     int
	errors    int
	durations []time.Duration // ring buffer of recent samples
	next      int
	max       time.Duration
}

const apiLatencySampleCap = 512

var apiLatency = &apiLatencyStats{routes: map[string]*apiRouteSamples{}}

func (s *apiLatencyStats) record(route string, status int, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.routes[route]
	if r == nil {
		r = &apiRouteSamples{}
		s.routes[route] = r
	}
	r.count++
	if status >= http.StatusInternalServerError {
		r.errors++
	}
	if d > r.max {
		r.max = d
	}
	if len(r.durations) < apiLatencySampleCap {
		r.durations = append(r.durations, d)
	} else {
		r.durations[r.next] = d
		r.next = (r.next + 1) % apiLatencySampleCap
	}
}

type apiRouteSummary struct {
	Route         string
	Count, Errors int
	P50, P95, Max time.Duration
}

// drain returns per-route summaries ordered by total server time (count*p50,
// so both hot and slow routes surface) and resets the window.
func (s *apiLatencyStats) drain() []apiRouteSummary {
	s.mu.Lock()
	routes := s.routes
	s.routes = map[string]*apiRouteSamples{}
	s.mu.Unlock()
	out := make([]apiRouteSummary, 0, len(routes))
	for route, r := range routes {
		sorted := append([]time.Duration(nil), r.durations...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		out = append(out, apiRouteSummary{Route: route, Count: r.count, Errors: r.errors,
			P50: percentile(sorted, 0.50), P95: percentile(sorted, 0.95), Max: r.max})
	}
	sort.Slice(out, func(i, j int) bool {
		wi, wj := time.Duration(out[i].Count)*out[i].P50, time.Duration(out[j].Count)*out[j].P50
		if wi != wj {
			return wi > wj
		}
		return out[i].Route < out[j].Route
	})
	return out
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

var apiLatencySummaryOnce sync.Once

func startAPILatencySummary() {
	apiLatencySummaryOnce.Do(func() {
		interval := envDuration("API_LATENCY_SUMMARY_INTERVAL", 5*time.Minute)
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				logAPILatencySummary(interval, apiLatency.drain(), 12)
			}
		}()
	})
}

func logAPILatencySummary(window time.Duration, routes []apiRouteSummary, top int) {
	if len(routes) == 0 {
		return
	}
	total := 0
	for _, r := range routes {
		total += r.Count
	}
	log.Printf("[API_LATENCY] window=%s requests=%d routes=%d", window, total, len(routes))
	for i, r := range routes {
		if i >= top {
			break
		}
		log.Printf("[API_LATENCY] route=%q n=%d p50=%dms p95=%dms max=%dms err5xx=%d",
			r.Route, r.Count, r.P50.Milliseconds(), r.P95.Milliseconds(), r.Max.Milliseconds(), r.Errors)
	}
}

var idLikeSegment = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f-]{27,}|[0-9a-f]{16,}|[0-9]+|work:project:.+|[a-z]+:[a-z]+:.+)$`)

// apiRouteTemplate names the route without raw ids so stats aggregate per
// endpoint: the gorilla template when routing matched, otherwise the path with
// id-like segments collapsed.
func apiRouteTemplate(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tpl, err := route.GetPathTemplate(); err == nil && tpl != "" {
			return r.Method + " " + tpl
		}
	}
	parts := strings.Split(r.URL.Path, "/")
	for i, part := range parts {
		if idLikeSegment.MatchString(part) {
			parts[i] = "{id}"
		}
	}
	return r.Method + " " + strings.Join(parts, "/")
}

func isAPIStreamRequest(r *http.Request, route string) bool {
	return strings.HasSuffix(route, "/stream") || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// Quiet routes are high-frequency and self-healing: their successes are only
// counted in the summary; failures other than expected lease conflicts still log.
func isQuietAPIRoute(route string) bool {
	return strings.HasSuffix(route, "/ui-control") || strings.HasSuffix(route, "/client-telemetry/chat-delivery") || strings.HasSuffix(route, "/health")
}

func apiSlowRequestThreshold() time.Duration {
	if ms, err := strconv.Atoi(strings.TrimSpace(os.Getenv("API_SLOW_REQUEST_MS"))); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 500 * time.Millisecond
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name))); err == nil && d > 0 {
		return d
	}
	return fallback
}

func serverTimingValue(d time.Duration) string {
	return fmt.Sprintf("app;dur=%.1f", float64(d.Microseconds())/1000)
}

// apiRecentRoutes is what each route served since the last [MEM] line: a heap jump has no owner in the heap numbers,
// so the line names the routes that moved the most bytes and kept handlers busy longest in the same minute.
type apiRecentRoutes struct {
	mu     sync.Mutex
	routes map[string]*apiRecentRoute
}
type apiRecentRoute struct {
	count int
	bytes int64
	busy  time.Duration
}

var apiRecent = &apiRecentRoutes{routes: map[string]*apiRecentRoute{}}

func (a *apiRecentRoutes) record(route string, bytes int64, busy time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.routes[route]
	if r == nil {
		r = &apiRecentRoute{}
		a.routes[route] = r
	}
	r.count++
	r.bytes += bytes
	r.busy += busy
}

// topAndReset returns "route:requests/KB/busy_ms" for the routes with the most bytes (ties: most busy time) and starts a new window.
func (a *apiRecentRoutes) topAndReset(n int) []string {
	a.mu.Lock()
	routes := a.routes
	a.routes = map[string]*apiRecentRoute{}
	a.mu.Unlock()
	names := make([]string, 0, len(routes))
	for name := range routes {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := routes[names[i]], routes[names[j]]
		if a.bytes != b.bytes {
			return a.bytes > b.bytes
		}
		if a.busy != b.busy {
			return a.busy > b.busy
		}
		return names[i] < names[j]
	})
	if len(names) > n {
		names = names[:n]
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		r := routes[name]
		out = append(out, fmt.Sprintf("%s:%dreq/%dKB/%dms", strings.ReplaceAll(name, " ", "_"), r.count, r.bytes>>10, r.busy.Milliseconds()))
	}
	return out
}
