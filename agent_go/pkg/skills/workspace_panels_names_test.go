package skills

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"
)

var (
	panelWordRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	panelActionRe = regexp.MustCompile("`?([a-z][a-z0-9_]*) action=([a-z][a-z0-9_]*)")
)

// A chat shows a catalogued tool as its public `family action=name`; the
// member name is a private registration identity that the model is not given
// (live Crew chats that followed a raw member name got tools_unavailable). Panel
// text must therefore never tell the model to call a member, and every
// `family action=name` it quotes must be a real catalog action. The front-matter
// panel-tools lists are registration names and are not checked here.
func TestPanelSkillBodiesNamePublicToolsOnly(t *testing.T) {
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
			if strings.Contains(n, "_") {
				private[n] = a.Family + " action=" + a.Name
			}
		}
	}
	for n := range private {
		if families[n] != nil {
			delete(private, n)
		}
	}
	err := fs.WalkDir(workspacePanelFiles, "panels", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(file, ".md") {
			return err
		}
		raw, err := workspacePanelFiles.ReadFile(file)
		if err != nil {
			return err
		}
		body := string(raw)
		if parts := strings.SplitN(body, "\n---", 2); strings.HasPrefix(body, "---") && len(parts) == 2 {
			body = parts[1]
		}
		for _, m := range panelActionRe.FindAllStringSubmatch(body, -1) {
			if actions, ok := families[m[1]]; ok && !actions[m[2]] {
				t.Errorf("%s: `%s action=%s` is not a catalog action", file, m[1], m[2])
			}
		}
		for _, loc := range panelWordRe.FindAllStringIndex(body, -1) {
			word := body[loc[0]:loc[1]]
			if public, ok := private[word]; ok && !strings.HasSuffix(body[:loc[0]], "action=") && !strings.HasSuffix(body[:loc[0]], "operation=") {
				t.Errorf("%s: names private tool %q; write `%s`", file, word, public)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
