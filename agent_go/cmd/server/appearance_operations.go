package server

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var appearanceOperations = []string{"read", "apply", "save", "delete", "reset", "duplicate", "rename", "import", "export", "mac_wallpapers", "import_mac_wallpaper", "import_image", "list_images", "delete_image"}

func appearanceSavedIndex(doc appearanceDocument, name string) int {
	for i, theme := range doc.Saved {
		if strings.EqualFold(theme.Name, strings.TrimSpace(name)) {
			return i
		}
	}
	return -1
}

func saveAppearanceTheme(doc *appearanceDocument, theme appearanceTheme) (bool, error) {
	if err := validAppearanceTheme(theme); err != nil {
		return false, err
	}
	if i := appearanceSavedIndex(*doc, theme.Name); i >= 0 {
		changed := !reflect.DeepEqual(doc.Saved[i], theme)
		doc.Saved[i] = theme
		return changed, nil
	}
	if len(doc.Saved) >= 20 {
		return false, errors.New("Keep up to 20 saved themes. Remove one first.")
	}
	doc.Saved = append(doc.Saved, theme)
	return true, nil
}

// Compare just the current theme, not every saved CSS document. Derive the
// names from the canonical struct so newly implemented theme fields are included.
func appearanceChangedFields(before, after appearanceTheme) []string {
	a, b := reflect.ValueOf(before), reflect.ValueOf(after)
	fields := a.Type()
	changed := []string{}
	for i := 0; i < fields.NumField(); i++ {
		name := strings.Split(fields.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" && !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			changed = append(changed, name)
		}
	}
	return changed
}

func appearanceMutationSummary(operation, name, newName string, current appearanceTheme) string {
	var summary string
	switch operation {
	case "apply":
		summary = fmt.Sprintf("Applied %q.", current.Name)
	case "save":
		summary = fmt.Sprintf("Saved and applied %q.", current.Name)
	case "import":
		summary = fmt.Sprintf("Imported %q. Current look unchanged; apply it when ready.", name)
	case "duplicate":
		summary = fmt.Sprintf("Saved a copy as %q. Current look unchanged.", newName)
	case "rename":
		summary = fmt.Sprintf("Renamed %q to %q.", name, newName)
	case "delete":
		summary = fmt.Sprintf("Deleted saved theme %q. Current look unchanged.", name)
	case "reset":
		summary = "Restored Default Dark. Saved themes kept."
	}
	return summary + " Your personal settings were saved; the actual appearance has not been visually checked."
}
