package voicestt

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const maxTranscribeBodyBytes = 256 << 20 // 256 MiB (~66 minutes of 16kHz float32 PCM)

// NewHelperHandler returns an HTTP handler exposing a voicestt Service over
// localhost, authenticated with X-Voice-Token. Used by cmd/voice-helper and
// unit tests.
func NewHelperHandler(svc Service, token string) http.Handler {
	mux := http.NewServeMux()
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 4 * 1024,
		CheckOrigin:     func(*http.Request) bool { return true },
	}

	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, svc.Status())
	})

	mux.HandleFunc("/warm", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		svc.Warm()
		writeJSON(w, http.StatusAccepted, svc.Status())
	})

	mux.HandleFunc("/unload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		released := svc.Unload()
		writeJSON(w, http.StatusOK, helperUnloadResponse{
			Released: released,
			Status:   svc.Status(),
		})
	})

	mux.HandleFunc("/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, helperTranscribeResponse{Error: "method not allowed"})
			return
		}
		limited := io.LimitReader(r.Body, maxTranscribeBodyBytes+1)
		raw, err := io.ReadAll(limited)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, helperTranscribeResponse{Error: err.Error()})
			return
		}
		if len(raw) > maxTranscribeBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, helperTranscribeResponse{Error: "audio payload too large"})
			return
		}
		if len(raw)%4 != 0 {
			writeJSON(w, http.StatusBadRequest, helperTranscribeResponse{Error: "invalid float32 PCM length"})
			return
		}
		text, err := svc.Transcribe(r.Context(), decodeFloat32LE(raw))
		if err != nil {
			code := http.StatusInternalServerError
			if !svc.Available() {
				code = http.StatusServiceUnavailable
			}
			writeJSON(w, code, helperTranscribeResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, helperTranscribeResponse{Text: text})
	})

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		svc.ServeStream(upgrader, w, r)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := r.Header.Get(helperTokenHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// RunHelperServer binds 127.0.0.1:0, announces {"port":N} on readyOut, serves
// svc until ctx is canceled, and then shuts down cleanly.
func RunHelperServer(ctx context.Context, svc Service, token string, readyOut io.Writer) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen loopback: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return errors.New("unexpected listener address type")
	}

	srv := &http.Server{
		Handler:           NewHelperHandler(svc, token),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	if readyOut != nil {
		line, err := json.Marshal(helperReadyMessage{Port: tcpAddr.Port})
		if err != nil {
			_ = srv.Close()
			return err
		}
		if _, err := fmt.Fprintf(readyOut, "%s\n", line); err != nil {
			_ = srv.Close()
			return fmt.Errorf("write ready line: %w", err)
		}
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = svc.Close()
		return nil
	case serveErr := <-errCh:
		_ = svc.Close()
		return serveErr
	}
}
