package tmuxsize

import (
	"os"
	"strconv"
	"sync"
)

const (
	EnvColumns = "CODING_AGENT_TMUX_COLUMNS"
	EnvRows    = "CODING_AGENT_TMUX_ROWS"

	DefaultColumns = 120
	DefaultRows    = 36

	minColumns = 80
	minRows    = 24
	maxColumns = 300
	maxRows    = 120
)

// preferred holds a runtime-set tmux size that overrides the env/default
// fallback. The frontend updates this via the resize HTTP route whenever its
// terminal viewer pane is measured or resized, so newly-launched coding-agent
// tmux sessions match what the operator can actually see without wrapping.
var preferred struct {
	sync.RWMutex
	columns int
	rows    int
}

// SetPreferredSize records the operator's last-observed viewport for the
// coding-agent tmux sessions. Subsequent Size()/Args() calls return these
// values (clamped to [min..max]). A non-positive value clears that axis.
func SetPreferredSize(columns, rows int) {
	preferred.Lock()
	defer preferred.Unlock()
	preferred.columns = clamp(columns, minColumns, maxColumns)
	preferred.rows = clamp(rows, minRows, maxRows)
}

func Args() []string {
	columns, rows := Size()
	return []string{"-x", strconv.Itoa(columns), "-y", strconv.Itoa(rows)}
}

func Size() (columns, rows int) {
	preferred.RLock()
	pc, pr := preferred.columns, preferred.rows
	preferred.RUnlock()
	if pc > 0 {
		columns = pc
	} else {
		columns = parseBoundedEnv(EnvColumns, DefaultColumns, minColumns, maxColumns)
	}
	if pr > 0 {
		rows = pr
	} else {
		rows = parseBoundedEnv(EnvRows, DefaultRows, minRows, maxRows)
	}
	return columns, rows
}

func parseBoundedEnv(name string, fallback, min, max int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return clamp(parsed, min, max)
}

func clamp(v, min, max int) int {
	if v <= 0 {
		return 0
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
