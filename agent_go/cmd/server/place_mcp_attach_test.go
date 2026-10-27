package server

import (
	"testing"
)

func TestPlaceRootOfAndCleanAttachRoot(t *testing.T) {
	cases := map[string]string{
		"Workflow/w":                                      "Workflow/w",
		"Workflow/w/runs/iteration-3/logs":                "Workflow/w",
		"/Workflow/w/":                                    "Workflow/w",
		"Workflow/.relay_releases/x":                      "",
		"Crew/abc/files/x":                                "Crew/abc",
		"_users/alice/Chats/Work/projects/p/db":           "_users/alice/Chats/Work/projects/p",
		"_users/alice/Chats/Code/projects/p":              "_users/alice/Chats/Code/projects/p",
		"_users/alice/Chats/Code/projects/p/code/main.go": "_users/alice/Chats/Code/projects/p",
		"_users/alice/Chats/Other/projects/p":             "",
		"Chats/Work/projects/p":                           "",
		"Workflow/../Workflow/w":                          "",
		"Downloads/x":                                     "",
	}
	for in, want := range cases {
		if got := placeRootOf(in); got != want {
			t.Errorf("placeRootOf(%q) = %q, want %q", in, got, want)
		}
	}
}
