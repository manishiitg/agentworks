package server

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacereads"
)

// CORS middleware
func (api *StreamingAPI) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		originAllowed := origin != "" && isAllowedCORSOrigin(origin, api.config.CORSOrigins) && r.URL.Path != browserPairingPeekPath && r.URL.Path != browserPairingRedeemPath
		if originAllowed {
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		// X-User-ID: the workspace client sends it on /api/wp calls; the gateway overwrites it with
		// the signed-in user (workspace_proxy.go), so allowing it only lets the preflight pass.
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Range, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Session-ID, Idempotency-Key, X-Client-Message-ID, X-Conversation-Continuation, X-Queued-Chat-Delivery, X-Client-Submitted-At, X-Chat-Input, X-Client-Switch, X-User-ID, X-AgentWorks-Desktop")
		w.Header().Set("Access-Control-Expose-Headers", "Server-Timing, Content-Range, X-Queued-Steer-Unavailable, X-Renewed-Token")
		if originAllowed {
			w.Header().Set("Timing-Allow-Origin", origin)
		}

		if r.Method == "OPTIONS" {
			if origin != "" && !originAllowed {
				http.Error(w, `{"error": "CORS origin not allowed"}`, http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAllowedCORSOrigin(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == "loopback" && isLoopbackOrigin(origin) {
			return true
		}
		if allowed == origin {
			return true
		}
	}
	return false
}

func isLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var apiRequestsInFlight int64

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	start  time.Time
}

// stampServerTiming adds time-to-headers as Server-Timing before the headers
// are sent, so the browser shows server time next to network time.
func (w *statusCapturingResponseWriter) stampServerTiming() {
	if w.status == 0 && !w.start.IsZero() && w.Header().Get("Server-Timing") == "" {
		w.Header().Set("Server-Timing", serverTimingValue(time.Since(w.start)))
	}
}

func (w *statusCapturingResponseWriter) WriteHeader(status int) {
	w.stampServerTiming()
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingResponseWriter) Write(data []byte) (int, error) {
	w.stampServerTiming()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

// Hijack forwards to the underlying ResponseWriter when it supports hijacking
// (any real net/http connection does). Required for ANY websocket handler
// reached through apiRequestLogMiddleware: an embedded http.ResponseWriter
// INTERFACE field only promotes the methods that interface declares
// (Header/Write/WriteHeader), not Hijack — so without this,
// gorilla/websocket's Upgrade() type-asserts for http.Hijacker, fails, and
// every upgrade attempt returns "response does not implement http.Hijacker"
// whenever request logging is on, which defaults to true (shouldLogAPIRequests).
// Caught live: this broke /api/voice/stream's very first non-browser test
// client, which (unlike a browser) has no Origin header so it sailed past the
// separate CheckOrigin issue and hit this one instead.
func (w *statusCapturingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusCapturingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusCapturingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (api *StreamingAPI) apiRequestLogMiddleware(next http.Handler) http.Handler {
	startAPILatencySummary()
	workspacereads.StartSummary(time.Minute, log.Printf)
	slow := apiSlowRequestThreshold()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLogAPIRequests() {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		readsCtx, reads := workspacereads.WithRequest(r.Context())
		r = r.WithContext(readsCtx)
		recorder := &statusCapturingResponseWriter{ResponseWriter: w, start: start}
		route := apiRouteTemplate(r)
		stream := isAPIStreamRequest(r, route)
		quiet := isQuietAPIRoute(route)
		traceRequest := shouldTraceAPIRequest(r) && !quiet && !stream
		var inFlight int64
		if traceRequest {
			inFlight = atomic.AddInt64(&apiRequestsInFlight, 1)
			logfWithContext(api.httpRequestLogContext(r), "[API] --> %s %s request_id=%q in_flight=%d", r.Method, requestLogPath(r), requestLogID(r), inFlight)
		}

		defer func() {
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			elapsed := time.Since(start)
			if stream {
				// Streams are long-lived by design: one line when they close,
				// never counted in the latency percentiles.
				logfWithContext(api.httpRequestLogContext(r), "[API] stream closed %s status=%d bytes=%d duration=%s",
					route, status, recorder.bytes, elapsed.Round(time.Millisecond))
				return
			}
			apiLatency.record(route, status, elapsed)
			apiRecent.record(route, int64(recorder.bytes), elapsed)
			if switchID := clientSwitchID(r); switchID != "" {
				logfWithContext(api.httpRequestLogContext(r), "[SWITCH_REQUEST] switch=%q method=%s path=%s status=%d duration=%s workspace_reads=%d workspace_read_ms=%d", switchID, r.Method, requestLogPath(r), status, elapsed.Round(time.Millisecond), reads.Reads(), reads.Millis())
			}

			// The handler marks only an admitted model-availability probe,
			// not arbitrary auth failures. Keep HTTP metrics/status intact.
			if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/model-proxy/") && strings.HasSuffix(r.URL.Path, "/v1/models") && status == http.StatusForbidden && recorder.Header().Get("X-AgentWorks-Model-Offer") == "not_offered" {
				if traceRequest {
					atomic.AddInt64(&apiRequestsInFlight, -1)
				}
				logfWithContext(api.httpRequestLogContext(r), "[MODEL_PROXY] event=not_offered provider=%s status=%d request_id=%q", mux.Vars(r)["provider"], status, requestLogID(r))
				return
			}
			if quiet && (status < http.StatusBadRequest || status == http.StatusConflict) {
				return
			}
			if !traceRequest && status < http.StatusBadRequest && elapsed >= slow {
				logfWithContext(api.httpRequestLogContext(r), "[API] slow %s %s request_id=%q status=%d bytes=%d duration=%s workspace_reads=%d workspace_read_ms=%d",
					r.Method, requestLogPath(r), requestLogID(r), status, recorder.bytes, elapsed.Round(time.Millisecond), reads.Reads(), reads.Millis())
				return
			}
			if traceRequest {
				remaining := atomic.AddInt64(&apiRequestsInFlight, -1)
				logfWithContext(api.httpRequestLogContext(r), "[API] <-- %s %s request_id=%q status=%d bytes=%d duration=%s in_flight=%d",
					r.Method,
					requestLogPath(r),
					requestLogID(r),
					status,
					recorder.bytes,
					time.Since(start).Round(time.Millisecond),
					remaining,
				)
			} else if status >= http.StatusBadRequest {
				// Background reads are quiet when they succeed, but failures remain
				// actionable in the normal server log.
				logfWithContext(api.httpRequestLogContext(r), "[API] <-- %s %s request_id=%q status=%d bytes=%d duration=%s",
					r.Method,
					requestLogPath(r),
					requestLogID(r),
					status,
					recorder.bytes,
					time.Since(start).Round(time.Millisecond),
				)
			}
		}()

		next.ServeHTTP(recorder, r)
	})
}

// shouldTraceAPIRequest keeps normal logs focused on user actions and writes.
// The frontend makes frequent successful GET requests for state refreshes; their
// failures are still logged by apiRequestLogMiddleware. Set
// API_REQUEST_LOG_INCLUDE_GET=true to temporarily trace GET/HEAD too, e.g. to
// measure real polling frequency from server logs instead of the browser
// Network tab — remove this override once the investigation is done.
func shouldTraceAPIRequest(r *http.Request) bool {
	if r.URL.Path == "/api/client-telemetry/chat-delivery" {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return apiRequestLogIncludeGET()
	default:
		return true
	}
}

func apiRequestLogIncludeGET() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("API_REQUEST_LOG_INCLUDE_GET")))
	return value == "true" || value == "1"
}

func shouldLogAPIRequests() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("API_REQUEST_LOG")))
	return value != "false" && value != "0" && value != "off"
}

func requestLogID(r *http.Request) string {
	if r == nil {
		return "-"
	}
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-ID")); requestID != "" {
		return requestID
	}
	return "-"
}

func requestLogPath(r *http.Request) string {
	path := r.URL.Path
	// A company account's id on the Mac works like a credential for its local model proxy: never logged.
	for _, prefix := range []string{"/api/desktop-model-proxy/", "/api/personal-model-proxy/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			if _, tail, found := strings.Cut(rest, "/"); found {
				path = prefix + "[account]/" + tail
			} else {
				path = prefix + "[account]"
			}
		}
	}
	if r.URL.RawQuery == "" {
		return path
	}
	query := r.URL.Query()
	// Sign-in tokens, and the one-time code and state an OAuth provider returns, are credentials.
	for _, key := range []string{"token", "access_token", "auth_token", "code", "state", "refresh_token", "id_token", "client_secret"} {
		if query.Has(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	return path + "?" + query.Encode()
}
