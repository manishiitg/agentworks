package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Two computers whose saved server connection was copied from one to the other share its ID. The relay keys a Mac's
// connection by that ID, so they kept replacing each other every half minute. desktopRelayDeviceID adds this machine's
// own ID, so each computer has its own connection.

var desktopMachineIDOnce struct {
	sync.Once
	id string
}

// desktopRelayDeviceID is this computer's relay device ID for one saved connection.
func desktopRelayDeviceID(connection string) string {
	if machine := desktopMachineID(); machine != "" {
		return connection + "." + machine
	}
	return connection
}

// desktopMachineID is a short hash of the computer's hardware or OS install ID (never the raw value), else an ID saved
// in the app's state folder; "" when neither is available.
func desktopMachineID() string {
	desktopMachineIDOnce.Do(func() {
		raw := desktopHardwareID()
		if raw == "" {
			raw = desktopSavedInstallID()
		}
		if raw != "" {
			sum := sha256.Sum256([]byte("agentworks-relay:" + raw))
			desktopMachineIDOnce.id = hex.EncodeToString(sum[:])[:16]
		}
	})
	return desktopMachineIDOnce.id
}

var desktopHardwareUUID = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)

func desktopHardwareID() string {
	var out []byte
	switch runtime.GOOS {
	case "darwin":
		out, _ = exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				return desktopHardwareUUID.FindString(line)
			}
		}
	case "windows":
		out, _ = exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
		return desktopHardwareUUID.FindString(string(out))
	case "linux":
		out, _ = os.ReadFile("/etc/machine-id")
		return strings.TrimSpace(string(out))
	}
	return ""
}

// desktopSavedInstallID is a random ID kept in the app's state folder (a copied folder copies it too, so it is only
// the fallback).
func desktopSavedInstallID() string {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return ""
	}
	path := filepath.Join(root, "relay-install-id")
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	id := uuid.NewString()
	if os.MkdirAll(root, 0o700) != nil || os.WriteFile(path, []byte(id+"\n"), 0o600) != nil {
		return ""
	}
	return id
}
