package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// ServeExecutor creates an authenticated outbound socket. No local listener is
// opened, and the local executor independently checks every file and shell request.
func (c *Client) ServeExecutor(ctx context.Context, executor *localfiles.Executor, connected func()) error {
	token, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	if strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid token")
	}
	address := strings.Replace(strings.Replace(c.baseURL, "https://", "wss://", 1), "http://", "ws://", 1) + "/api/external/v1/devices/connect"
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, response, err := dialer.DialContext(ctx, address, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			return executorConnectionError(response)
		}
		return fmt.Errorf("executor connection failed: %w", err)
	}
	defer conn.Close()
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	conn.SetReadLimit(localfiles.MaxMessageBytes)
	conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err = conn.WriteJSON(executor.Hello); err != nil {
		return err
	}
	var ack struct {
		Connected bool `json:"connected"`
	}
	if err = conn.ReadJSON(&ack); err != nil || !ack.Connected {
		if websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
			return &APIError{Status: 409, Code: "device_busy", Message: "The previous device connection is still closing; reconnect with backoff."}
		}
		if websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				switch closeErr.Text {
				case "invalid executor handshake":
					return &APIError{Status: 409, Code: "invalid_executor_handshake", Message: "The local folder connection could not be verified. Update AgentWorks from the app's connection setup, then try sharing the folder again."}
				case "device connection is no longer authorized":
					return &APIError{Status: 403, Code: "device_authorization_lost", Message: "Access to this local folder connection is no longer approved. Open Local CLI connection in the app and follow its setup to reconnect."}
				}
			}
			return &APIError{Status: 409, Code: "executor_rejected", Message: "The server rejected the local folder connection. Open Local CLI connection in the app to check its setup."}
		}
		return fmt.Errorf("executor handshake rejected")
	}
	if connected != nil {
		connected()
	}
	done := make(chan struct{})
	defer close(done)
	var writeMu sync.Mutex
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-done:
				return
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				writeMu.Unlock()
				if err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	// Keep reading while commands run, so heartbeats and disconnects can
	// cancel the operation instead of waiting for the shell timeout.
	slots := make(chan struct{}, 8)
	var operations sync.WaitGroup
	var activeMu sync.Mutex
	active := map[string]context.CancelFunc{}
	defer func() { cancelConnection(); operations.Wait() }()
	send := func(result localfiles.Response) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(result)
	}
	for {
		var request localfiles.Request
		if err = conn.ReadJSON(&request); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if request.Operation == "cancel" {
			activeMu.Lock()
			if cancel := active[request.ID]; cancel != nil {
				cancel()
			}
			activeMu.Unlock()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			if err = send(localfiles.Response{ID: request.ID, Status: 429, Error: "executor is busy"}); err != nil {
				return err
			}
			continue
		}
		timeout := 30 * time.Second
		if request.Operation == "patch" {
			timeout = 180 * time.Second
		}
		if request.Operation == "shell" && request.ValidateShell() == nil {
			timeout = time.Duration(request.ShellTimeout()+5) * time.Second
		}
		operationCtx, cancel := context.WithTimeout(connectionCtx, timeout)
		activeMu.Lock()
		active[request.ID] = cancel
		activeMu.Unlock()
		operations.Add(1)
		go func(request localfiles.Request) {
			defer operations.Done()
			defer func() { <-slots }()
			defer func() { cancel(); activeMu.Lock(); delete(active, request.ID); activeMu.Unlock() }()
			began := time.Now()
			result := executor.Execute(operationCtx, request)
			if c.executorTrace != nil {
				c.executorTrace(request, result, time.Since(began))
			}
			if err := send(result); err != nil {
				conn.Close()
			}
		}(request)
	}
}

// Only the server's structured error is suitable for display. A proxy's HTML,
// plain response body or authentication headers are not a connection reason.
func executorConnectionError(response *http.Response) *APIError {
	e := &APIError{Status: response.StatusCode, Code: "executor_connection_failed", Message: "The server could not accept the local folder connection. Try again; if it continues, contact your administrator."}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		e.Message = "Your AgentWorks sign-in could not be verified. Open Local CLI connection in the app and follow its setup to sign in again."
	case http.StatusForbidden:
		e.Message = "The server did not allow this local folder connection. Open Local CLI connection in the app to check its setup, or contact your administrator."
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(data) > 8192 {
		return e
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error.Message != "" && len(body.Error.Message) <= 2048 {
		e.Message = body.Error.Message
		if body.Error.Code != "" && len(body.Error.Code) <= 100 {
			e.Code = body.Error.Code
		}
	}
	return e
}
