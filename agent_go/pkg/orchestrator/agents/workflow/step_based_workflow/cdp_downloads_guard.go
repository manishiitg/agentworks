package step_based_workflow

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func (hcpo *StepBasedWorkflowOrchestrator) effectiveBrowserModeForHostDownloads() string {
	mode := strings.ToLower(strings.TrimSpace(hcpo.GetBrowserMode()))
	if mode != "" && mode != "auto" {
		return mode
	}
	if hcpo.GetCdpPort() > 0 {
		return "cdp"
	}
	return ""
}

func (hcpo *StepBasedWorkflowOrchestrator) appendCDPHostDownloadsPaths(readPaths, writePaths []string) ([]string, []string) {
	hostDownloads := common.CDPHostDownloadsPath(hcpo.effectiveBrowserModeForHostDownloads())
	if hostDownloads == "" {
		return common.DeduplicateStrings(readPaths), common.DeduplicateStrings(writePaths)
	}
	return common.DeduplicateStrings(append(readPaths, hostDownloads)), common.DeduplicateStrings(append(writePaths, hostDownloads))
}

// appendCDPHostDownloadsReadPath preserves the existing read-only call sites
// while newer workflow sessions opt into the explicit read-write helper.
//
//nolint:unused // Used by the already-pushed read-only callers on a clean main checkout.
func (hcpo *StepBasedWorkflowOrchestrator) appendCDPHostDownloadsReadPath(readPaths []string) []string {
	hostDownloads := common.CDPHostDownloadsReadPath(hcpo.effectiveBrowserModeForHostDownloads())
	if hostDownloads == "" {
		return common.DeduplicateStrings(readPaths)
	}
	return common.DeduplicateStrings(append(readPaths, hostDownloads))
}

func (hcpo *StepBasedWorkflowOrchestrator) grantSessionCDPHostDownloadsReadWrite(sessionID string) {
	hostDownloads := common.GrantSessionCDPHostDownloadsReadWrite(sessionID, hcpo.effectiveBrowserModeForHostDownloads())
	if hostDownloads != "" && hcpo.BaseOrchestrator != nil && hcpo.GetLogger() != nil {
		hcpo.GetLogger().Info("Added read-write CDP host Downloads grant: " + hostDownloads)
	}
}
