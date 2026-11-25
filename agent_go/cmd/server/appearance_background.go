package server

import (
	"errors"
	"regexp"
)

const appearanceScriptMaxBytes = 64 << 10

type appearanceScript struct {
	Code string `json:"code"`
	FPS  int    `json:"fps"`
	Mode string `json:"mode,omitempty"`
}

type appearanceShader struct {
	Code      string  `json:"code"`
	Speed     float64 `json:"speed"`
	Intensity float64 `json:"intensity"`
	Mode      string  `json:"mode,omitempty"`
}
type appearanceDecoration struct {
	ID       string                 `json:"id"`
	Children []appearanceDecoration `json:"children,omitempty"`
}

var appearanceDecorationID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func appearanceBackgroundMode(s *appearanceScript) string {
	if s == nil {
		return ""
	}
	return s.Mode
}

func appearanceShaderMode(s *appearanceShader) string {
	if s == nil {
		return ""
	}
	return s.Mode
}

func validAppearanceBackground(t appearanceTheme) error {
	backgrounds := 0
	for _, present := range []bool{t.BackgroundScript != nil, t.BackgroundShader != nil, t.BackgroundImage != nil, t.BackgroundVideo != nil} {
		if present {
			backgrounds++
		}
	}
	if backgrounds > 1 {
		return errors.New("Choose one image, video, shader or script background; clear the others with null.")
	}
	for _, mode := range []string{appearanceBackgroundMode(t.BackgroundScript), appearanceShaderMode(t.BackgroundShader)} {
		if mode != "" && mode != "dark" && mode != "light" && mode != "both" {
			return errors.New("Background mode must be dark, light or both (omit for both).")
		}
	}
	if s := t.BackgroundScript; s != nil {
		if len(s.Code) == 0 || len(s.Code) > appearanceScriptMaxBytes || s.FPS < 1 || s.FPS > 30 {
			return errors.New("Script code must be 1–65536 UTF-8 bytes; fps must be 1–30. Define function render(frame).")
		}
	}
	if s := t.BackgroundShader; s != nil {
		if t.BackgroundVideo != nil || t.BackgroundImage != nil {
			return errors.New("Choose one image, video or shader background; clear the others with null.")
		}
		if len(s.Code) == 0 || len(s.Code) > appearanceShaderMaxBytes {
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
