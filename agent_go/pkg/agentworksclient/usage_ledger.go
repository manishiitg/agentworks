package agentworksclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
)

type UsageLedger struct {
	Generation string            `json:"generation"`
	Identity   string            `json:"file_identity"`
	Ledger     agentusage.Ledger `json:"ledger"`
}

func usageLedgerPath(root, key string) (string, error) {
	if len(key) != 64 || strings.ContainsFunc(key, func(r rune) bool { return !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') }) {
		return "", errors.New("invalid native ledger key")
	}
	return filepath.Join(root, "usage-ledgers", key+".json"), nil
}

func LoadUsageLedger(root, key string) (UsageLedger, error) {
	var l UsageLedger
	p, e := usageLedgerPath(root, key)
	if e != nil {
		return l, e
	}
	e = readLiveJSON(p, &l)
	if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	return l, e
}

func SaveUsageLedger(root, key string, l UsageLedger) error {
	return withUsageLock(root, func() error {
		cfg, e := LocalUsageSettings(root)
		if e != nil {
			return e
		}
		if !cfg.Enabled || cfg.Generation != l.Generation {
			return errors.New("usage setting changed")
		}
		p, e := usageLedgerPath(root, key)
		if e != nil {
			return e
		}
		if e = privateLiveDir(filepath.Dir(p)); e != nil {
			return e
		}
		return atomicLiveJSON(p, l)
	})
}
