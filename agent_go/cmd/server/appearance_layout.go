package server

import (
	"errors"
	"slices"
)

// Optional personal presentation only. Nil restores the app's existing defaults.
type appearanceChat struct {
	UserStyle      string `json:"user_style,omitempty"`
	AssistantStyle string `json:"assistant_style,omitempty"`
	SpacingPX      *int   `json:"spacing_px,omitempty"`
	ReadingWidthPX *int   `json:"reading_width_px,omitempty"`
}
type appearanceLayout struct {
	Density        string `json:"density,omitempty"`
	SidebarWidthPX *int   `json:"sidebar_width_px,omitempty"`
	RightPanelOpen *bool  `json:"right_panel_open,omitempty"`
}

func validAppearanceLayout(t appearanceTheme) error {
	if c := t.Chat; c != nil {
		for _, style := range []string{c.UserStyle, c.AssistantStyle} {
			if style != "" && !slices.Contains([]string{"bubble", "plain", "card"}, style) {
				return errors.New("Choose bubble, plain or card for each message style.")
			}
		}
		if c.SpacingPX != nil && (*c.SpacingPX < 0 || *c.SpacingPX > 32) {
			return errors.New("Message spacing must be 0–32 px.")
		}
		if c.ReadingWidthPX != nil && (*c.ReadingWidthPX < 480 || *c.ReadingWidthPX > 1600) {
			return errors.New("Reading width must be 480–1600 px; clear chat to restore full width.")
		}
	}
	if l := t.Layout; l != nil {
		if l.Density != "" && !slices.Contains([]string{"compact", "normal", "roomy"}, l.Density) {
			return errors.New("Choose compact, normal or roomy density.")
		}
		if l.SidebarWidthPX != nil && (*l.SidebarWidthPX < 220 || *l.SidebarWidthPX > 360) {
			return errors.New("Sidebar width must be 220–360 px.")
		}
	}
	return nil
}
