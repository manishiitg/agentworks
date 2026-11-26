package server

import (
	"errors"
	"fmt"
	"slices"
)

// These fields style existing glyphs and borders, never interaction or work state.
type appearanceIcons struct {
	Style       string   `json:"style,omitempty"`
	StrokeWidth *float64 `json:"stroke_width,omitempty"`
	Scale       *float64 `json:"scale,omitempty"`
}

type appearanceBorder struct {
	WidthPX  *int   `json:"width_px,omitempty"`
	RadiusPX *int   `json:"radius_px,omitempty"`
	Style    string `json:"style,omitempty"`
}

var appearanceBorderSurfaces = []string{"panel", "card", "composer", "input", "selected_agent", "running_agent"}

func validAppearanceControls(t appearanceTheme) error {
	if icons := t.Icons; icons != nil {
		if icons.Style != "" && icons.Style != "round" && icons.Style != "square" {
			return errors.New("Choose round or square icon strokes. Both use the app's existing outline icons.")
		}
		if icons.StrokeWidth != nil && (*icons.StrokeWidth < 0.75 || *icons.StrokeWidth > 3) {
			return errors.New("Icon stroke width must be between 0.75 and 3.")
		}
		if icons.Scale != nil && (*icons.Scale < 0.75 || *icons.Scale > 1.5) {
			return errors.New("Icon scale must be between 0.75 and 1.5; 1 keeps the current size.")
		}
	}
	for surface, border := range t.Borders {
		if !slices.Contains(appearanceBorderSurfaces, surface) {
			return errors.New("Choose a border for panel, card, composer, input, selected_agent or running_agent.")
		}
		if border.WidthPX != nil && (*border.WidthPX < 0 || *border.WidthPX > 4) {
			return fmt.Errorf("Border width for %s must be between 0 and 4 px.", surface)
		}
		if border.RadiusPX != nil && (*border.RadiusPX < 0 || *border.RadiusPX > 9999) {
			return fmt.Errorf("Border radius for %s must be between 0 and 9999 px (0 is sharp; 9999 is pill-shaped).", surface)
		}
		if border.Style != "" && !slices.Contains([]string{"solid", "dashed", "dotted"}, border.Style) {
			return errors.New("Choose a solid, dashed or dotted border.")
		}
	}
	return nil
}
