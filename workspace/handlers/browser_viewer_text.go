package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BrowserViewerText is service-only, like BrowserLiveStream. The agent service
// checks scope and the exclusive manual-control lease before each request.
// Text goes to the existing daemon over its private IPC channel, never shell
// arguments, disk, an OS clipboard or a caller-selected executable/endpoint.
func BrowserViewerText(c *gin.Context) {
	var request struct {
		Text string `json:"text"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if c.ShouldBindJSON(&request) != nil || len(request.Text) == 0 || len(request.Text) > 8<<10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid browser text"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	if err := insertBrowserViewerText(ctx, c.Param("session"), request.Text); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Unable to paste into this browser"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func insertBrowserViewerText(ctx context.Context, session, text string) error {
	_, dir, err := browserLiveEndpoint(session)
	if err != nil {
		return err
	}
	out, err := existingBrowserCommand(ctx, dir, session, map[string]any{"id": "viewer-paste", "action": "keyboard", "subaction": "insertText", "text": text})
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			Inserted bool `json:"inserted"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &response) != nil || !response.Data.Inserted {
		return fmt.Errorf("browser rejected text")
	}
	return nil
}

// Only trusted fixed service operations call this helper. It connects to an
// existing daemon; it cannot spawn, upgrade or restart a user's browser.
func existingBrowserCommand(ctx context.Context, dir, session string, request map[string]any) ([]byte, error) {
	if !browserLiveSessionName.MatchString(session) {
		return nil, fmt.Errorf("invalid browser session")
	}
	network, address := "unix", filepath.Join(dir, session+".sock")
	if runtime.GOOS == "windows" {
		data, err := os.ReadFile(filepath.Join(dir, session+".port"))
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid browser port")
		}
		network, address = "tcp", fmt.Sprintf("127.0.0.1:%d", port)
	} else {
		info, err := os.Lstat(address)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("browser IPC unavailable")
		}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, err
	}
	var out json.RawMessage
	if err := json.NewDecoder(bufio.NewReader(io.LimitReader(conn, 2<<20))).Decode(&out); err != nil {
		return nil, err
	}
	var response struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
	}
	if json.Unmarshal(out, &response) != nil || response.ID != request["id"] || !response.Success {
		return nil, fmt.Errorf("browser operation failed")
	}
	return out, nil
}
