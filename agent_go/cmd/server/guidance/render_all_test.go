package guidance

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"
)

// TestAllGuidanceTemplatesRender renders every template in both registries with
// empty caller context. A template that references a tmplData field that does
// not exist (or has a malformed action) only fails at execute time, which
// previously panicked at materialize time in production (buildMegaSkill). This
// guards that whole class of bug at test time.
func TestAllGuidanceTemplatesRender(t *testing.T) {
	for kind := range allKinds {
		if _, err := renderFromRegistry(kind, tmplData{}, allKinds); err != nil {
			t.Errorf("allKinds/%s failed to render: %v", kind, err)
		}
	}
	for kind := range referenceKinds {
		if _, err := renderFromRegistry(kind, tmplData{}, referenceKinds); err != nil {
			t.Errorf("referenceKinds/%s failed to render: %v", kind, err)
		}
	}
}

// A chat shows a catalogued tool as its public `family action=name`; the catalog
// member name is a private registration identity (live chats that followed a raw
// member got tools_unavailable). Guidance must name the public form, and every
// `family action=name` it quotes must be a catalog action. A name after a colon is a
// config key such as human_tools:send_email, which keeps its registration name. The kinds below still
// name members: bridge and step-agent text that describes route names, or text
// whose serving surface has not been classified yet (PLAT-746-415). Remove a kind
// from the list when its text is converted; do not add one.
// Bridge text is valid as written: configureScriptBridgeSession keeps every step member
// executor under its member name and adds the family names beside it, so
// $MCP_CUSTOM/<member> routes still work for a saved script (mcp-bridge,
// secure-share-links, scripted). The other kinds mix chat and script text and have
// not been split; browser-usage and optimize-playbook name agent_browser, whose
// public browser arguments are unconfirmed.
var guidancePendingPublicNames = map[string]bool{
	"browser-usage":         true,
	"llm-provider-config":   true,
	"mcp-bridge":            true,
	"playwright-scripted":   true,
	"secure-share-links":    true,
	"workspace-media-tools": true,
}

func TestGuidanceNamesPublicToolsOnly(t *testing.T) {
	families := map[string]map[string]bool{}
	private := map[string]string{}
	for _, a := range catalog.Actions {
		if families[a.Family] == nil {
			families[a.Family] = map[string]bool{}
		}
		families[a.Family][a.Name] = true
		names := append([]string{a.Member}, a.Aliases...)
		names = append(names, strings.Split(a.ExternalMembers, "|")...)
		for _, n := range names {
			if strings.Contains(n, "_") && n != a.Name {
				private[n] = a.Family + " action=" + a.Name
			}
		}
	}
	for n := range private {
		if families[n] != nil {
			delete(private, n)
		}
	}
	word := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	action := regexp.MustCompile("`?([a-z][a-z0-9_]*) action=([a-z][a-z0-9_]*)")
	used := map[string]bool{}
	err := fs.WalkDir(templatesFS, "templates", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(file, ".md") {
			return walkErr
		}
		raw, err := templatesFS.ReadFile(file)
		if err != nil {
			return err
		}
		body := string(raw)
		if parts := strings.SplitN(body, "\n---", 2); strings.HasPrefix(body, "---") && len(parts) == 2 {
			body = parts[1]
		}
		kind := strings.TrimSuffix(path.Base(file), ".md")
		for _, m := range action.FindAllStringSubmatch(body, -1) {
			if actions, ok := families[m[1]]; ok && !actions[m[2]] {
				t.Errorf("%s: `%s action=%s` is not a catalog action", file, m[1], m[2])
			}
		}
		for _, loc := range word.FindAllStringIndex(body, -1) {
			name := body[loc[0]:loc[1]]
			before := body[:loc[0]]
			public, ok := private[name]
			if !ok || strings.HasSuffix(before, "action=") || strings.HasSuffix(before, "operation=") || strings.HasSuffix(before, ":") || strings.HasPrefix(body[loc[1]:], "=") {
				continue
			}
			if guidancePendingPublicNames[kind] {
				used[kind] = true
				continue
			}
			t.Errorf("%s: names private tool %q; write `%s`", file, name, public)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for kind := range guidancePendingPublicNames {
		if !used[kind] {
			t.Errorf("%s no longer names a private tool; remove it from guidancePendingPublicNames", kind)
		}
	}
}
