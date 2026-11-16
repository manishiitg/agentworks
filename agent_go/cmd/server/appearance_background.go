package server

import (
	"errors"
	"regexp"
)

type appearanceShader struct {
	Code      string  `json:"code"`
	Speed     float64 `json:"speed"`
	Intensity float64 `json:"intensity"`
}
type appearanceDecoration struct {
	ID       string                 `json:"id"`
	Children []appearanceDecoration `json:"children,omitempty"`
}

var appearanceDecorationID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func validAppearanceBackground(t appearanceTheme) error {
	if s := t.BackgroundShader; s != nil {
		if t.BackgroundVideo != nil || t.BackgroundImage != nil {
			return errors.New("Choose one image, video or shader background; clear the others with null.")
		}
		if len(s.Code) == 0 || len(s.Code) > 16<<10 {
			return errors.New("Shader code must be 1–16384 UTF-8 bytes of GLSL, not JavaScript.")
		}
		if s.Speed < 0 || s.Speed > 2 || s.Intensity < 0 || s.Intensity > 1 {
			return errors.New("Shader speed must be 0–2 and intensity 0–1.")
		}
	}
	count := 0
	ids := map[string]bool{}
	var visit func([]appearanceDecoration, int) error
	visit = func(nodes []appearanceDecoration, depth int) error {
		if len(nodes) > 0 && depth > 6 {
			return errors.New("Decorations can nest at most 6 levels.")
		}
		for _, n := range nodes {
			count++
			if count > 64 || !appearanceDecorationID.MatchString(n.ID) || ids[n.ID] {
				return errors.New("Use up to 64 decorations with unique short lowercase IDs (letters, numbers, hyphens or underscores).")
			}
			ids[n.ID] = true
			if err := visit(n.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(t.Decorations, 1)
}
