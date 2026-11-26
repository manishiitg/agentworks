package musecli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type museSkillRow struct {
	Selector string `json:"selector"`
	Source   string `json:"source"`
}

// museSkillLister is the one serve call the skill part needs; tests stub it.
type museSkillLister func(ctx context.Context, sessionID string) ([]museSkillRow, error)

func (s *museMSPSession) listSkills(ctx context.Context, sessionID string) ([]museSkillRow, error) {
	var out struct {
		Skills []museSkillRow `json:"skills"`
	}
	if err := s.rpc.call(ctx, "skill/list", map[string]any{"sessionId": sessionID}, &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}

// museSkillInput turns the skill picked for this message into Muse's own
// `skill` input part. Muse resolves a skill by its name in the session's
// catalog, not by path, so the name is checked against `skill/list` first: a
// name Muse does not know would fail the turn and end the serve session.
func museSkillInput(ctx context.Context, owner, sessionID string, list museSkillLister) (map[string]any, error) {
	skill, ok := llmtypes.ClaimNativeSkillForSession(ctx, owner)
	if !ok {
		return nil, nil
	}
	if err := llmtypes.ValidateNativeSkill(skill); err != nil {
		return nil, err
	}
	rows, err := list(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("Muse could not list its skills for this chat: %w", err)
	}
	want := museSkillSource(skill.Path)
	var found []museSkillRow
	for _, row := range rows {
		if row.Selector == skill.Name {
			found = append(found, row)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("Muse does not have the skill %q in this chat. Refresh skills or remove the selection.", skill.Name)
	}
	part := map[string]any{"type": "skill", "selector": skill.Name, "source": found[0].Source}
	for _, row := range found {
		if row.Source == want {
			part["source"] = want
		}
	}
	return part, nil
}

// museSkillSource names the copy the person picked: their own skills folder
// is Muse's "user" source, anything else came from the project.
func museSkillSource(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(filepath.Clean(path), filepath.Join(home, ".agents", "skills")+string(filepath.Separator)) {
		return "user"
	}
	return "project"
}
