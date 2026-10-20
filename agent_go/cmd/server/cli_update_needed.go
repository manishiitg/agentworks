package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Which attached agents run an older CLI than this server offers (PLAT-861). The CLI says its build when it attaches;
// the server compares it with the build in downloads/version.json. Development builds and attachments that did not
// say are never "behind".

var clientBuildPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func cleanClientBuild(v string) string {
	if clientBuildPattern.MatchString(v) {
		return v
	}
	return ""
}

var servedCLIBuild struct {
	sync.Mutex
	build string
	at    time.Time
}

// latestServedCLIBuild is the CLI build this server offers ("" when it offers none), read at most once a minute.
func latestServedCLIBuild() string {
	servedCLIBuild.Lock()
	defer servedCLIBuild.Unlock()
	if time.Since(servedCLIBuild.at) < time.Minute {
		return servedCLIBuild.build
	}
	servedCLIBuild.at = time.Now()
	servedCLIBuild.build = ""
	if dir := cliDownloadsDir(); dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "version.json")); err == nil {
			var v struct {
				CLIBuild string `json:"cli_build"`
			}
			if json.Unmarshal(raw, &v) == nil {
				servedCLIBuild.build = cleanClientBuild(v.CLIBuild)
			}
		}
	}
	return servedCLIBuild.build
}

// cliUpdateNotice is what an agent is told, in its own message results, when the CLI it runs through is older than the
// one this server offers. It reaches every app that reads messages (with or without hooks), including older CLIs, because
// it rides in the server's reply. The agent runs the update when the person has asked, or passes it to the person.
const cliUpdateNotice = "A newer AgentWorks CLI is available on this server. Run `agentworks update` (or ask the person to), then `agentworks setup <app>` for their coding app, and restart the app. The old `login --token-stdin` option is gone; use `agentworks login --no-browser` and approve the link in any browser."

func agentUpdateNeeded(a namedMessageAgent) bool {
	if a.Attachment == nil || a.Attachment.ClientBuild == "" || a.Attachment.ClientBuild == "dev" {
		return false
	}
	latest := latestServedCLIBuild()
	return latest != "" && latest != a.Attachment.ClientBuild
}
