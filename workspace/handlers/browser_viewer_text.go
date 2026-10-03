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
	network, address := "unix", filepath.Join(dir, session+".sock")
	if runtime.GOOS == "windows" {
		data, err := os.ReadFile(filepath.Join(dir, session+".port"))
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid browser port")
		}
		network, address = "tcp", fmt.Sprintf("127.0.0.1:%d", port)
	} else {
		info, err := os.Lstat(address)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("browser IPC unavailable")
		}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	// The same fixed operation used by agent-browser keyboard inserttext.
	if err := json.NewEncoder(conn).Encode(map[string]any{"id": "viewer-paste", "action": "keyboard", "subaction": "insertText", "text": text}); err != nil {
		return err
	}
	var response struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
		Data    struct {
			Inserted bool `json:"inserted"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bufio.NewReader(io.LimitReader(conn, 16<<10))).Decode(&response); err != nil {
		return err
	}
	if response.ID != "viewer-paste" || !response.Success || !response.Data.Inserted {
		return fmt.Errorf("browser rejected text")
	}
	return nil
}
