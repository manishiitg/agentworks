package server

import "testing"

// PLAT-746: the receipt repeated the project and Builder tab inside each other.
func TestCrewMessageSenderDoesNotRepeatProjectAndDefaultTab(t *testing.T) {
	for _, tc := range []struct{ crew, name, want string }{
		{"Reports", "Builder", "Reports · Main"},
		{"Reports", "Research", "Reports · Research"},
		{"Research", "Research", "Research"},
		{"", "Research", "Research"},
	} {
		if got := crewMessageSenderName(tc.crew, tc.name); got != tc.want {
			t.Errorf("crew=%q name=%q: got %q, want %q", tc.crew, tc.name, got, tc.want)
		}
	}
}
