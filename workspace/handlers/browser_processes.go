package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BrowserProcess represents a running chromium browser process
type BrowserProcess struct {
	PID       int     `json:"pid"`
	CPU       float64 `json:"cpu"`
	Memory    float64 `json:"mem_mb"`
	StartedAt string  `json:"started_at"`
	UserData  string  `json:"user_data_dir"`
	Type      string  `json:"type"` // "main" or "helper"
}

// ListBrowserProcesses returns all running chromium processes
// GET /api/browser/processes
func ListBrowserProcesses(c *gin.Context) {
	processes, err := getBrowserProcesses()
	if err != nil {
		log.Printf("[BROWSER] ERROR: Failed to list browser processes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"processes": processes,
		"count":     len(processes),
	})
}

// KillBrowserProcesses kills chromium processes
// POST /api/browser/cleanup
// Body: {"pids": [123, 456]} or {"all": true}
func KillBrowserProcesses(c *gin.Context) {
	var req struct {
		PIDs []int `json:"pids"`
		All  bool  `json:"all"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	if req.All {
		// Kill every browser process the list shows (Chromium, Chrome, headless shell), not a name pattern: the managed Chrome on
		// the servers is /opt/google/chrome/chrome, which `pkill -f chromium` never matched ("stop" did nothing).
		current, listErr := getBrowserProcesses()
		if listErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": listErr.Error()})
			return
		}
		if len(current) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"killed":  0,
				"message": "No browser processes found",
			})
			return
		}
		for _, process := range current {
			if process.PID > 1 {
				_ = exec.Command("kill", "-9", strconv.Itoa(process.PID)).Run()
			}
		}

		// Wait briefly for processes to die, then count remaining
		time.Sleep(500 * time.Millisecond)
		remaining, _ := getBrowserProcesses()

		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"message":   "All chromium processes killed",
			"remaining": len(remaining),
		})
		return
	}

	if len(req.PIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Provide 'pids' array or set 'all' to true",
		})
		return
	}

	// Only a browser process may be killed here. Without this check any caller could send the
	// id of the agent, the gateway or another user's work and stop it with kill -9.
	current, _ := getBrowserProcesses()
	allowed := filterBrowserPIDs(req.PIDs, current)

	killed := 0
	for _, pid := range allowed {
		err := exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
		if err == nil {
			killed++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"killed":  killed,
		"message": fmt.Sprintf("Killed %d of %d requested processes", killed, len(req.PIDs)),
	})
}

// filterBrowserPIDs keeps the requested ids that are in the current list of browser processes.
func filterBrowserPIDs(requested []int, current []BrowserProcess) []int {
	known := make(map[int]bool, len(current))
	for _, process := range current {
		known[process.PID] = true
	}
	var allowed []int
	for _, pid := range requested {
		if pid > 1 && known[pid] {
			allowed = append(allowed, pid)
		}
	}
	return allowed
}

// getBrowserProcesses parses ps output to find chromium processes
func getBrowserProcesses() ([]BrowserProcess, error) {
	// Use ps to get chromium processes with details
	out, err := exec.Command("sh", "-c",
		`ps aux | grep -E '[c]hromium|[g]oogle/chrome/chrome|[g]oogle-chrome|[c]hrome-headless-shell' | grep -v 'grep'`,
	).Output()
	if err != nil {
		// grep returns 1 if no matches — that means no processes
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []BrowserProcess{}, nil
		}
		return nil, fmt.Errorf("failed to list processes: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var processes []BrowserProcess
	self := ""
	if current, userErr := user.Current(); userErr == nil {
		self = current.Username
	}

	for _, line := range lines {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}

		// Only this account's browsers: `ps aux` lists every account on a shared host, and listing another account's
		// command lines (or offering them for cleanup) is neither useful nor safe.
		if owner := strings.TrimSuffix(fields[0], "+"); self != "" && fields[0] != self && !(owner != fields[0] && strings.HasPrefix(self, owner)) {
			continue
		}

		pid, _ := strconv.Atoi(fields[1])
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		memKB, _ := strconv.ParseFloat(fields[5], 64)
		memMB := memKB / 1024
		startedAt := fields[8]

		// Determine process type
		cmdLine := strings.Join(fields[10:], " ")
		procType := "main"
		if strings.Contains(cmdLine, "--type=renderer") {
			procType = "renderer"
		} else if strings.Contains(cmdLine, "--type=gpu") {
			procType = "gpu"
		} else if strings.Contains(cmdLine, "--type=utility") {
			procType = "utility"
		}

		// Extract user-data-dir to identify sessions
		userDataDir := ""
		for _, f := range fields[10:] {
			if strings.HasPrefix(f, "--user-data-dir=") {
				userDataDir = strings.TrimPrefix(f, "--user-data-dir=")
				// Shorten the path for display
				parts := strings.Split(userDataDir, "/")
				if len(parts) > 0 {
					last := parts[len(parts)-1]
					if strings.HasPrefix(last, "agent-browser-chrome-") {
						userDataDir = last[len("agent-browser-chrome-"):]
					}
				}
				break
			}
		}

		processes = append(processes, BrowserProcess{
			PID:       pid,
			CPU:       cpu,
			Memory:    memMB,
			StartedAt: startedAt,
			UserData:  userDataDir,
			Type:      procType,
		})
	}

	return processes, nil
}
