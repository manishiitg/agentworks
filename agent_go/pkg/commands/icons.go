package commands

import (
	_ "embed"
	"fmt"
	"strings"
)

// lucide_icons.txt lists the icon names the app can draw (lucide-react's
// dynamicIconImports keys); its first line names the source version.
//
//go:embed lucide_icons.txt
var lucideIconList string

var lucideIcons = func() map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(lucideIconList, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			names[line] = true
		}
	}
	return names
}()

// IconDescription is the icon parameter text shared by the command tools.
const IconDescription = `Optional icon: any Lucide icon name in kebab-case, for example "ticket", "git-branch", "bug" or "rocket" (see lucide.dev/icons). Default "terminal".`

// ValidateIcon accepts an empty icon (the default) or a Lucide icon name.
func ValidateIcon(icon string) error {
	if icon == "" || lucideIcons[icon] {
		return nil
	}
	return fmt.Errorf("icon %q is not a Lucide icon name. Use a kebab-case name from lucide.dev/icons, for example \"ticket\", \"git-branch\", \"bug\" or \"rocket\"", icon)
}
