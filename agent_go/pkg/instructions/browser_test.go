package instructions

import (
	"strings"
	"testing"
)

func TestBuildBrowserInstructionsListsAuthorizedCDPProfiles(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	got := BuildBrowserInstructions(BrowserConfig{
		HasAgentBrowser: true,
		Mode:            "cdp",
		CdpPort:         9222,
		CdpPorts:        []int{9222, 9333},
	})
	for _, want := range []string{
		"http://localhost:9222",
		"http://localhost:9333",
		"multi-login testing",
		"one hour after the final run",
		`browser("tab", ["close", "<owned-label>"])`,
		"Never close a pre-existing user tab",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("browser instructions missing %q", want)
		}
	}
}

func TestBuildBrowserInstructionsKeepsAutoModeDynamic(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	got := BuildBrowserInstructions(BrowserConfig{
		HasAgentBrowser: true,
		Mode:            "auto",
		CdpPort:         9222,
		CdpPorts:        []int{9222},
	})
	for _, want := range []string{"agent_browser", "status", "effective_mode", "http://localhost:9222", "never taken from saved conversation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("auto browser instructions missing %q", want)
		}
	}
	if strings.Contains(got, "Browser Mode: Headless") || strings.Contains(got, "Browser Mode: CDP") {
		t.Fatalf("auto browser instructions must not persist a resolved mode: %s", got)
	}
}

func TestBrowserInstructionsDistinguishStatusProbeFromPageSnapshot(t *testing.T) {
	for name, instructions := range map[string]string{
		"auto": GetAutoBrowserInstructions(9222),
		"cdp":  GetCdpBrowserInstructions(9222),
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range []string{"status", "needs no tab", "Never use `snapshot`"} {
				if !strings.Contains(instructions, want) {
					t.Fatalf("browser instructions missing %q:\n%s", want, instructions)
				}
			}
		})
	}
}

func TestBuildBrowserRuntimeInstructionsIsCompactAndDynamic(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	got := BuildBrowserRuntimeInstructions(BrowserConfig{
		HasAgentBrowser: true,
		Mode:            "auto",
		CdpPort:         9222,
		CdpPorts:        []int{9222, 9333},
	})
	for _, want := range []string{"Configured mode: `auto`", "http://localhost:9222", "http://localhost:9333", "Open agent-browser when browser work is needed", "status reports live availability", "agent-browser/references/playwright-scripted.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact browser runtime instructions missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{"def browser(", "### QA, Network Debugging", "Deterministic-selector priority"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("compact runtime instructions repeated static skill content %q", forbidden)
		}
	}
	if len(got) > 1_500 {
		t.Fatalf("compact browser runtime instructions grew to %d bytes", len(got))
	}
}
