package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref/reftest"
)

// PLAT-435: every site that used to strip or test "_users/" by hand is run
// under both spellings, plus the physical path of another user.

const (
	refUser    = "alice"
	crewLogic  = "Chats/Work/projects/c1"
	codeLogic  = "Chats/Code/projects/p1"
	crewDeeper = "Chats/Work/projects/c1/db/x.json"
)

func TestRefSitesCleanAgentProfileWorkspace(t *testing.T) {
	reftest.BothSpellings(t, refUser, crewLogic, func(t *testing.T, spelling string) {
		if got, err := cleanAgentProfileWorkspace(spelling, refUser); err != nil || got != spelling {
			t.Fatalf("%q: %q %v", spelling, got, err)
		}
	})
	for _, bad := range []string{reftest.OtherUser(refUser, crewLogic), "_users", "_users/", "_users/default/x"} {
		if _, err := cleanAgentProfileWorkspace(bad, refUser); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestRefSitesProductConversationRuntimeWorkspace(t *testing.T) {
	want := workspaceref.MustParse(crewLogic).Physical(refUser)
	reftest.BothSpellings(t, refUser, crewLogic, func(t *testing.T, spelling string) {
		if got := productConversationRuntimeWorkspace(refUser, spelling); got != want {
			t.Fatalf("%q -> %q want %q", spelling, got, want)
		}
	})
	other := reftest.OtherUser(refUser, crewLogic)
	if got := productConversationRuntimeWorkspace(refUser, other); got != other {
		t.Fatalf("another user's path must be kept, got %q", got)
	}
}

func TestRefSitesCrewOwnership(t *testing.T) {
	reftest.BothSpellings(t, refUser, crewDeeper, func(t *testing.T, spelling string) {
		if !crewProjectOwnedByCaller(refUser, spelling) || !isCrewProjectPath(spelling) {
			t.Fatalf("%q not owned crew project", spelling)
		}
	})
	other := reftest.OtherUser(refUser, crewDeeper)
	if crewProjectOwnedByCaller(refUser, other) || !isCrewProjectPath(other) {
		t.Fatalf("%q must be a crew path but not owned", other)
	}
	if crewProjectOwnedByCaller(refUser, "/docs/"+other) {
		t.Fatal("absolute path of another user must not be owned")
	}
	if owner, ok := crewProjectOwnerID(other); !ok || owner != "other-"+refUser {
		t.Fatalf("owner = %q %v", owner, ok)
	}
	if _, ok := crewProjectOwnerID(crewLogic); ok {
		t.Fatal("a logical path has no owner")
	}
	if isCrewProjectPath(codeLogic) {
		t.Fatal("a Code path is not a Crew path")
	}
}

func TestRefSitesWorkspaceProxyOtherUser(t *testing.T) {
	reftest.BothSpellings(t, refUser, crewDeeper, func(t *testing.T, spelling string) {
		if workspaceProxyPathIsOtherUser(spelling, refUser) {
			t.Fatalf("%q is the caller's own", spelling)
		}
	})
	for _, bad := range []string{reftest.OtherUser(refUser, crewDeeper), "_users", "/_users/", "x/../_users/bob/y"} {
		if !workspaceProxyPathIsOtherUser(bad, refUser) {
			t.Errorf("%q must be refused", bad)
		}
	}
	if workspaceProxyPathIsOtherUser("Chats/foo/_users/bob/x", refUser) {
		t.Error("a folder named _users inside the caller's tree is not another user")
	}
}

func TestRefSitesOwnedWorkProjectWorkspacePath(t *testing.T) {
	want := workspaceref.MustParse(crewLogic).Physical(refUser)
	reftest.BothSpellings(t, refUser, crewLogic, func(t *testing.T, spelling string) {
		if got, ok := ownedWorkProjectWorkspacePath(refUser, spelling); !ok || got != want {
			t.Fatalf("%q -> %q %v", spelling, got, ok)
		}
	})
	other := reftest.OtherUser(refUser, crewLogic)
	if got, ok := ownedWorkProjectWorkspacePath(refUser, other); ok || got != other {
		t.Fatalf("another user's project must not be mapped into the caller's tree: %q %v", got, ok)
	}
}

func TestRefSitesRestoredConversationPath(t *testing.T) {
	file := "/builder/conversation/2026-10-04/s.json"
	reftest.BothSpellings(t, refUser, crewLogic, func(t *testing.T, spelling string) {
		_, ok := normalizeRestoredChatHistoryConversationPath(refUser, spelling+file)
		// the logical spelling is not a physical file path: only the caller's own
		// physical spelling restores, and only the physical one passes.
		if want := spelling == workspaceref.MustParse(crewLogic).Physical(refUser); ok != want {
			t.Fatalf("%q ok=%v want %v", spelling, ok, want)
		}
	})
	other := reftest.OtherUser(refUser, crewLogic) + file
	if _, ok := normalizeRestoredChatHistoryConversationPath(refUser, other); ok {
		t.Fatal("another user's transcript must not restore")
	}
}

func TestRefSitesPlaceAttachRoots(t *testing.T) {
	phys := workspaceref.MustParse(codeLogic).Physical(refUser)
	reftest.BothSpellings(t, refUser, codeLogic+"/src/a.go", func(t *testing.T, spelling string) {
		if got := placeRootOf(spelling); spelling == phys+"/src/a.go" && got != phys {
			t.Fatalf("placeRootOf(%q) = %q", spelling, got)
		}
		if got := attachRootForCaller(refUser, workspaceref.MustParse(spelling).Logical()); got != phys && got != "" {
			t.Fatalf("attachRootForCaller = %q", got)
		}
	})
	if got := attachRootForCaller(refUser, codeLogic); got != phys {
		t.Fatalf("logical -> %q", got)
	}
	if got := attachRootForCaller(refUser, phys); got != phys {
		t.Fatalf("physical -> %q", got)
	}
	if got := attachRootForCaller(refUser, reftest.OtherUser(refUser, codeLogic)); got != reftest.OtherUser(refUser, codeLogic) {
		t.Fatalf("another user's root stays theirs: %q", got)
	}
	if !isCodePlaceRoot(phys) || isCodePlaceRoot(workspaceref.MustParse(crewLogic).Physical(refUser)) || isCodePlaceRoot(codeLogic) {
		t.Fatal("isCodePlaceRoot")
	}
	for _, bad := range []string{
		phys + "/src", "_users/" + refUser + "/Chats/Other/projects/x", "_users/" + refUser, codeLogic,
	} {
		if got := cleanAttachRoot(bad); got != "" {
			t.Errorf("cleanAttachRoot(%q) = %q", bad, got)
		}
	}
}

func TestRefSitesUIControlOwnership(t *testing.T) {
	reftest.BothSpellings(t, refUser, codeLogic, func(t *testing.T, spelling string) {
		ref := workspaceref.MustParse(spelling)
		if !ref.OwnedByOrUnowned(refUser) || !ref.IsProject() {
			t.Fatalf("%q", spelling)
		}
	})
	if workspaceref.MustParse(reftest.OtherUser(refUser, codeLogic)).OwnedByOrUnowned(refUser) {
		t.Fatal("another user's project is not owned")
	}
}

func TestRefSitesCrewRootsAndRefs(t *testing.T) {
	phys := workspaceref.MustParse(crewLogic).Physical(refUser)
	other := reftest.OtherUser(refUser, crewLogic)
	if !externalIsCrewRoot(phys) || !externalIsCrewRoot(other) || externalIsCrewRoot(crewLogic) || externalIsCrewRoot(phys+"/db") ||
		externalIsCrewRoot("/docs/"+phys) {
		t.Fatal("externalIsCrewRoot")
	}
	if !isOtherOwnerCrewPath(splitPath(other)) || isOtherOwnerCrewPath(splitPath(crewLogic)) || isOtherOwnerCrewPath(splitPath(phys+"/db")) ||
		isOtherOwnerCrewPath(splitPath("_users/x/../y/Chats/Work/projects/c")) {
		t.Fatal("isOtherOwnerCrewPath")
	}
	reftest.BothSpellings(t, refUser, crewDeeper, func(t *testing.T, spelling string) {
		ref, ok := parseCrewPath(refUser, spelling)
		if !ok || ref.OwnerID != refUser || ref.Root != phys || ref.Rest != "db/x.json" {
			t.Fatalf("%q -> %+v %v", spelling, ref, ok)
		}
	})
	if ref, ok := parseCrewPath(refUser, other+"/db"); !ok || ref.OwnerID != "other-"+refUser || ref.Rest != "db" {
		t.Fatalf("other user's crew = %+v %v", ref, ok)
	}
	if _, ok := parseCrewPath(refUser, "_users/a/../b/Chats/Work/projects/c"); ok {
		t.Fatal("traversal accepted")
	}
}

func splitPath(p string) []string { return strings.Split(p, "/") }

func TestRefSitesBrowserKeyAndCosts(t *testing.T) {
	want := workspaceref.MustParse(crewLogic).Physical(refUser)
	reftest.BothSpellings(t, refUser, crewLogic, func(t *testing.T, spelling string) {
		if got := browserProjectKey(refUser, spelling); got != want {
			t.Fatalf("%q -> %q", spelling, got)
		}
	})
	other := reftest.OtherUser(refUser, crewLogic)
	if got := browserProjectKey(refUser, other); got != other {
		t.Fatalf("other user's key = %q", got)
	}
	if browserProjectKey(refUser, "") != "" {
		t.Fatal("empty workspace")
	}
	codePhys := workspaceref.MustParse(codeLogic).Physical(refUser)
	if root, kind, name, owner := costOverviewRoot(codePhys + "/x"); root != codePhys || kind != costOverviewKindProduct ||
		name != "Code · p1" || owner != refUser {
		t.Fatalf("costOverviewRoot = %q %q %q %q", root, kind, name, owner)
	}
	if !costOverviewIsCode(codePhys) || costOverviewIsCode(want) || costOverviewIsCode(codeLogic) {
		t.Fatal("costOverviewIsCode")
	}
}

func TestRefSitesFolderPolicies(t *testing.T) {
	reftest.BothSpellings(t, refUser, "Chats/Code/projects", func(t *testing.T, spelling string) {
		if !codeFilesDeletionProtected(spelling) {
			t.Fatalf("%q must be protected", spelling)
		}
	})
	reftest.BothSpellings(t, refUser, codeLogic, func(t *testing.T, spelling string) {
		if !codeFilesDeletionProtected(spelling) {
			t.Fatalf("project root %q must be protected", spelling)
		}
	})
	reftest.BothSpellings(t, refUser, "Chats/Code/projects/p1/src/a.go", func(t *testing.T, spelling string) {
		if codeFilesDeletionProtected(spelling) {
			t.Fatalf("%q is an ordinary file", spelling)
		}
	})
	reftest.BothSpellings(t, refUser, "Chats/notes", func(t *testing.T, spelling string) {
		if !isChatsWriteFolder(spelling) {
			t.Fatalf("%q is a chats folder", spelling)
		}
	})
	if isChatsWriteFolder("_users/" + refUser + "/Downloads") {
		t.Fatal("Downloads is not a chats folder")
	}
}
