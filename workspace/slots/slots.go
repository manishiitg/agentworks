// Package slots maps platform users to Linux accounts ("slots") and runs a command as one of them
// through sudo and the small slotctl launcher. See the private slot plan; the design in short:
//
//   - An administrator provisions slot accounts (slot01, slot02, ...) and the table below; signing
//     in never creates one.
//   - The platform account may run exactly one program as a slot account: slotctl.
//   - slotctl runs as the slot (sudo already switched), checks the request against a root-owned
//     allow-list, and starts the program with the request's environment.
//   - The platform reaches a slot's files through a per-slot group it belongs to; other slots are
//     not in that group.
//
// The feature is off unless AGENTWORKS_SLOTS=on (or optin), so hosts and the desktop app are unchanged.
package slots

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// StopGrace is how long a slotted command gets to leave after a stop signal before everything it started is killed.
const StopGrace = 2 * time.Second

const (
	// EnvEnabled turns the feature on ("on").
	EnvEnabled = "AGENTWORKS_SLOTS"
	// EnvTableFile overrides the path of the slot table.
	EnvTableFile = "AGENTWORKS_SLOTS_FILE"
	// EnvSlotctl overrides the path of the slotctl launcher.
	EnvSlotctl = "AGENTWORKS_SLOTCTL"
	// EnvPrefix names this product's slot accounts (default "slot": slot01, slot02, ...). Products that share
	// a host each have their own prefix, launcher, config and table, so one product's service account is
	// never in another product's slot groups.
	EnvPrefix = "AGENTWORKS_SLOT_PREFIX"
	// EnvConfig overrides the path of the slotctl allow-list config.
	EnvConfig = "AGENTWORKS_SLOTCTL_CONFIG"
	// DefaultPrefix is the slot account prefix when a product does not set one.
	DefaultPrefix = "slot"

	// DefaultTableFile is root-owned and group-readable by the platform: the platform can read it
	// and cannot change which user holds which slot.
	DefaultTableFile = "/etc/agentworks/slots.json"
	// DefaultSlotctl is a root-owned program in a root-owned folder.
	DefaultSlotctl = "/usr/local/libexec/agentworks/slotctl"
	// DefaultSudo is the sudo binary.
	DefaultSudo = "/usr/bin/sudo"
)

var (
	prefixMu       sync.Mutex
	configuredPref string
	nameCache      = map[string]*regexp.Regexp{}
)

// SetPrefix sets the slot account prefix for this process (read from a product's slotctl config by the
// programs that run without the service's environment). An empty prefix keeps the current one.
func SetPrefix(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return
	}
	prefixMu.Lock()
	configuredPref = prefix
	prefixMu.Unlock()
}

// Prefix returns the slot account prefix in effect: the config's, else AGENTWORKS_SLOT_PREFIX, else "slot".
func Prefix() string {
	prefixMu.Lock()
	defer prefixMu.Unlock()
	if configuredPref != "" {
		return configuredPref
	}
	if env := strings.TrimSpace(os.Getenv(EnvPrefix)); env != "" {
		return env
	}
	return DefaultPrefix
}

func slotNamePattern() *regexp.Regexp {
	prefix := Prefix()
	prefixMu.Lock()
	defer prefixMu.Unlock()
	re, ok := nameCache[prefix]
	if !ok {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "[0-9]{2,3}$")
		nameCache[prefix] = re
	}
	return re
}

// ValidSlot reports whether name looks like one of this product's slot account names.
func ValidSlot(name string) bool { return slotNamePattern().MatchString(name) }

// Table is the user to slot assignment.
type Table struct {
	// Slots maps a slot name to the user id that holds it.
	Slots map[string]string `json:"slots"`
}

// SlotFor returns the slot a user holds.
func (t *Table) SlotFor(userID string) (string, bool) {
	userID = strings.TrimSpace(userID)
	if t == nil || userID == "" {
		return "", false
	}
	for slot, holder := range t.Slots {
		if holder == userID && ValidSlot(slot) {
			return slot, true
		}
	}
	return "", false
}

// Enabled reports whether the feature is on for this process: "on" (every user needs a slot) or "optin"
// (users who hold a slot run as it; everyone else is unchanged, which lets a host whose workflows and
// shared folders are not slot-aware yet roll slots out user by user).
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvEnabled))) {
	case "on", "optin":
		return true
	}
	return false
}

func optIn() bool { return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvEnabled)), "optin") }

// Slotctl returns the launcher path.
func Slotctl() string {
	if override := strings.TrimSpace(os.Getenv(EnvSlotctl)); override != "" {
		return override
	}
	return DefaultSlotctl
}

func tablePath() string {
	if override := strings.TrimSpace(os.Getenv(EnvTableFile)); override != "" {
		return override
	}
	return DefaultTableFile
}

// LoadTable reads a slot table.
func LoadTable(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var table Table
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("slot table %s: %w", path, err)
	}
	for slot := range table.Slots {
		if !ValidSlot(slot) {
			return nil, fmt.Errorf("slot table %s: %q is not a slot name", path, slot)
		}
	}
	return &table, nil
}

// ErrNoSlot is returned when the feature is on and the user has no slot: an administrator has not
// provisioned them. Callers refuse the request rather than fall back to the platform account.
var ErrNoSlot = errors.New("this account has no slot yet: ask an administrator to provision one")

var (
	cacheMu    sync.Mutex
	cachePath  string
	cacheMTime time.Time
	cacheTable *Table
)

func cachedTable() (*Table, error) {
	path := tablePath()
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheTable != nil && cachePath == path && cacheMTime.Equal(info.ModTime()) {
		return cacheTable, nil
	}
	table, err := LoadTable(path)
	if err != nil {
		return nil, err
	}
	cachePath, cacheMTime, cacheTable = path, info.ModTime(), table
	return table, nil
}

// For returns the slot a user must run as. enabled is false when the feature is off (the caller
// runs as the platform account, as before). When enabled and the user holds no slot, it returns
// ErrNoSlot; a table that cannot be read is an error too, never a silent fallback.
func For(userID string) (slot string, enabled bool, err error) {
	if !Enabled() {
		return "", false, nil
	}
	table, err := cachedTable()
	if err != nil {
		// In opt-in mode a host that has not been provisioned yet has no table: nobody holds a slot, so
		// nothing changes for anyone. Any other failure to read it (permissions, a damaged file) is still an
		// error, never a silent fallback.
		if optIn() && errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("slot table unavailable: %w", err)
	}
	slot, ok := table.SlotFor(userID)
	if !ok {
		if optIn() {
			return "", false, nil
		}
		return "", true, ErrNoSlot
	}
	return slot, true, nil
}
