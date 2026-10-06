package agentworksclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// ServeExecutor creates an authenticated outbound socket. No local listener is
// opened, and the local executor independently checks every file request.
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
			return &APIError{Status: response.StatusCode, Code: "executor_connection_failed", Message: "Executor connection was rejected; sign in with devices:connect permission."}
		}
		return fmt.Errorf("executor connection failed: %w", err)
	}
	defer conn.Close()
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
		if websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
			return &APIError{Status: 409, Code: "executor_rejected", Message: "Device ID is already connected or the executor handshake was rejected."}
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
	for {
		var request localfiles.Request
		if err = conn.ReadJSON(&request); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result := executor.Execute(operationCtx, request)
		cancel()
		writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		err = conn.WriteJSON(result)
		writeMu.Unlock()
		if err != nil {
			return err
		}
	}
}
