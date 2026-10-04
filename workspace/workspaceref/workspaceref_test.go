package workspaceref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		owner   string
		logical string
		ok      bool
	}{
		{"", "", "", true},
		{".", "", "", true},
		{"/", "", "", true},
		{"  Chats/Code/projects/p  ", "", "Chats/Code/projects/p", true},
		{"Chats/Code/projects/p", "", "Chats/Code/projects/p", true},
		{"/Chats/Code/projects/p/", "", "Chats/Code/projects/p", true},
		{"Chats\\Code\\projects\\p", "", "Chats/Code/projects/p", true},
		{"./Chats//Code/./projects/p", "", "Chats/Code/projects/p", true},
		{"Chats/Code/x/../projects/p", "", "Chats/Code/projects/p", true},
		{"../Chats", "", "", false},
		{"Chats/../../x", "", "", false},
		{"..", "", "", false},
		{"_users/alice/Chats/Code/projects/p", "alice", "Chats/Code/projects/p", true},
		{"/_users/alice/Chats/Code/projects/p/", "alice", "Chats/Code/projects/p", true},
		{"_users\\alice\\Chats", "alice", "Chats", true},
		{"_users/alice", "alice", "", true},
		{"_users/alice/", "alice", "", true},
		{"_users", "", "", true},
		{"/_users/", "", "", true},
		{"_users/_system_global_secrets/x/y", "_system_global_secrets", "x/y", true},
		{"_users/alice/../bob/Chats", "bob", "Chats", true},
		{"_users/alice/Chats/../../../x", "", "x", true},
		{"_users/alice/Chats/../../../../x", "", "", false},
		{"/Users/me/docs/_users/alice/Chats/Work/projects/c", "alice", "Chats/Work/projects/c", true},
		{"/Users/me/docs/_users/alice", "alice", "", true},
		{"/var/x_users/alice/Chats", "", "var/x_users/alice/Chats", true},
		{"x_users/alice/Chats", "", "x_users/alice/Chats", true},
		{"Chats/foo/_users/bob/x", "", "Chats/foo/_users/bob/x", true},
		{"/Chats/foo/_users/bob/x", "bob", "x", true},
		{"Chats/x_users/y", "", "Chats/x_users/y", true},
		{"/var/x_users/y/_users/alice/Chats", "alice", "Chats", true},
		{"var/x_users/y/_users/alice/Chats", "", "var/x_users/y/_users/alice/Chats", true},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok {
			t.Errorf("Parse(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Owner() != c.owner || got.Logical() != c.logical {
			t.Errorf("Parse(%q) = (%q,%q) want (%q,%q)", c.in, got.Owner(), got.Logical(), c.owner, c.logical)
		}
		if got.HasOwner() != (c.owner != "") {
			t.Errorf("Parse(%q).HasOwner = %v", c.in, got.HasOwner())
		}
	}
}

func TestUsersRootOnly(t *testing.T) {
	for _, in := range []string{"_users", "/_users/", "_users\\"} {
		r := MustParse(in)
		if !r.IsUsersRoot() || !r.IsEmpty() || r.HasOwner() {
			t.Errorf("%q: %+v", in, r)
		}
	}
	if MustParse("_users/alice").IsUsersRoot() {
		t.Error("owner dir is not the users root")
	}
}

func TestSanitizeAndPhysical(t *testing.T) {
	for in, want := range map[string]string{
		"alice": "alice", "": "default", "a b": "default", "../x": "default", "A_b-9": "A_b-9",
	} {
		if got := SanitizeUserID(in); got != want {
			t.Errorf("Sanitize(%q) = %q want %q", in, got, want)
		}
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if SanitizeUserID(string(long)) != "default" {
		t.Error("over-long id must fall back")
	}
	logical := MustParse("Chats/Code/projects/p")
	if got := logical.Physical("alice"); got != "_users/alice/Chats/Code/projects/p" {
		t.Errorf("Physical = %q", got)
	}
	if got := logical.Physical("bad id"); got != "_users/default/Chats/Code/projects/p" {
		t.Errorf("Physical bad id = %q", got)
	}
	// Physical re-places a path under the given user, ignoring its owner.
	if got := MustParse("_users/bob/Chats").Physical("alice"); got != "_users/alice/Chats" {
		t.Errorf("Physical replace = %q", got)
	}
	if got := MustParse("_users/bob/Chats").PhysicalKeepOwner("alice"); got != "_users/bob/Chats" {
		t.Errorf("KeepOwner = %q", got)
	}
	if got := MustParse("Chats").PhysicalKeepOwner("alice"); got != "_users/alice/Chats" {
		t.Errorf("KeepOwner logical = %q", got)
	}
	if got := MustParse("").Physical("alice"); got != "_users/alice" {
		t.Errorf("empty Physical = %q", got)
	}
	if UserRoot("alice") != "_users/alice" {
		t.Error("UserRoot")
	}
	// Round trip.
	for _, in := range []string{"Chats/Code/projects/p", "_users/alice/Chats/Code/projects/p"} {
		r := MustParse(in)
		if !MustParse(r.Physical("alice")).SameFor("alice", MustParse(in)) {
			t.Errorf("round trip %q", in)
		}
	}
}

func TestIdentity(t *testing.T) {
	const logical = "Chats/Code/projects/p"
	pub := MustParse(logical)
	alice := MustParse("_users/alice/" + logical)
	bob := MustParse("_users/bob/" + logical)
	abs := MustParse("/docs/_users/alice/" + logical)

	// Strict identity never equates physical and logical.
	if pub.SameAs(alice) || alice.SameAs(bob) || !alice.SameAs(abs) || !pub.SameAs(MustParse("/"+logical+"/")) {
		t.Error("SameAs")
	}
	if MustParse("_users").SameAs(MustParse("")) {
		t.Error("users root is not the empty path")
	}

	cases := []struct {
		user string
		a, b Ref
		want bool
	}{
		{"alice", pub, alice, true},
		{"alice", alice, pub, true},
		{"alice", abs, pub, true},
		{"alice", pub, bob, false},
		{"alice", bob, pub, false},
		{"bob", pub, alice, false},
		{"bob", pub, bob, true},
		{"alice", alice, alice, true},
		{"carol", alice, alice, true}, // equal strings stay equal for anyone
		{"carol", alice, bob, false},
		{"alice", pub, MustParse("Chats/Code/projects/q"), false},
		{"", pub, MustParse("_users/default/" + logical), true},
		{"bad id", pub, MustParse("_users/default/" + logical), true},
		{"alice", MustParse("_users"), MustParse(""), false},
	}
	for i, c := range cases {
		if got := c.a.SameFor(c.user, c.b); got != c.want {
			t.Errorf("case %d SameFor(%q, %+v, %+v) = %v want %v", i, c.user, c.a, c.b, got, c.want)
		}
	}
}

func TestOwnedBy(t *testing.T) {
	alice := MustParse("_users/alice/Chats")
	pub := MustParse("Chats")
	if !alice.OwnedBy("alice") || alice.OwnedBy("bob") || pub.OwnedBy("alice") {
		t.Error("OwnedBy")
	}
	if !alice.OwnedByOrUnowned("alice") || alice.OwnedByOrUnowned("bob") || !pub.OwnedByOrUnowned("bob") {
		t.Error("OwnedByOrUnowned")
	}
	if !MustParse("_users/default/x").OwnedBy("") {
		t.Error("empty user is default")
	}
	if MustParse("_users/_system_global_secrets/x").OwnedByOrUnowned("alice") {
		t.Error("reserved user is not alice's")
	}
}

func TestProject(t *testing.T) {
	cases := []struct {
		in      string
		root    string
		project string
		ok      bool
	}{
		{"Chats/Work/projects/c", CrewProjectsRoot, "c", true},
		{"_users/alice/Chats/Work/projects/c/db/x", CrewProjectsRoot, "c", true},
		{"Chats/Code/projects/p/", CodeProjectsRoot, "p", true},
		{"/docs/_users/bob/Chats/Code/projects/p/src", CodeProjectsRoot, "p", true},
		{"Chats/Code/projects", "", "", false},
		{"_users/alice/Chats/Code/projects", "", "", false},
		{"Chats/Code/projects/", "", "", false},
		{"Chats/Other/projects/p", "", "", false},
		{"Workflow/w", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		root, project, ok := MustParse(c.in).Project()
		if root != c.root || project != c.project || ok != c.ok {
			t.Errorf("Project(%q) = (%q,%q,%v)", c.in, root, project, ok)
		}
		if MustParse(c.in).IsProject() != c.ok {
			t.Errorf("IsProject(%q)", c.in)
		}
	}
}

func TestWithLogicalKeepsOwner(t *testing.T) {
	r := MustParse("_users/alice/Chats/Code/projects/p/src").WithLogical("Chats/Code/projects/p")
	if r.Owner() != "alice" || r.Logical() != "Chats/Code/projects/p" {
		t.Errorf("%+v", r)
	}
}

func TestProjectRootStringAndPhysicalPathOf(t *testing.T) {
	cases := map[string]bool{
		"_users/a/Chats/Work/projects/c":    true,
		"Chats/Code/projects/p":             true,
		"_users/a/Chats/Work/projects/c/db": false,
		"_users/a/Chats/Work/projects":      false,
		"_users/a/Chats/Other/projects/x":   false,
	}
	for in, want := range cases {
		if _, _, ok := MustParse(in).ProjectRoot(); ok != want {
			t.Errorf("ProjectRoot(%q) = %v", in, ok)
		}
	}
	for _, in := range []string{"_users/a/Chats", "Chats/x", "_users", "", "_users/a"} {
		if got := MustParse(in).String(); got != in {
			t.Errorf("String(%q) = %q", in, got)
		}
	}
	if MustParse("/_users/a//Chats/").String() != "_users/a/Chats" {
		t.Error("String must be canonical")
	}
	if PhysicalPathOf("bob", "Chats", "x") != "_users/bob/Chats/x" {
		t.Error("PhysicalPathOf")
	}
}

// PLAT-442 step 4: a shared Crew path names no owner and is never "the caller's own" or "public".
func TestSharedCrewRootSemantics(t *testing.T) {
	cases := []struct {
		in          string
		shared      bool
		project     string
		projectRoot bool
	}{
		{"Crew", true, "", false},
		{"Crew/", true, "", false},
		{"Crew/sde-1a2b", true, "sde-1a2b", true},
		{"/Crew/sde-1a2b/db/x.html", true, "sde-1a2b", false},
		{"Crew\\sde-1a2b\\db", true, "sde-1a2b", false},
		{"./Crew/x/../sde-1a2b", true, "sde-1a2b", true},
		{"Crew/.hidden", true, "", false},
		{"Crew/.migration/journal.json", true, "", false},
		{"CrewX/a", false, "", false},
		{"crew/a", false, "", false},
		{"Chats/Work/projects/a", false, "", false},
		{"_users/alice/Crew/a", false, "", false},
		{"_users/alice/Chats/Work/projects/a", false, "", false},
		{"Workflow/Crew/a", false, "", false},
	}
	for _, c := range cases {
		r := MustParse(c.in)
		if r.IsShared() != c.shared {
			t.Errorf("IsShared(%q) = %v", c.in, r.IsShared())
		}
		project, ok := r.SharedProject()
		if ok != (c.project != "") || project != c.project {
			t.Errorf("SharedProject(%q) = %q %v", c.in, project, ok)
		}
		if _, ok := r.SharedProjectRoot(); ok != c.projectRoot {
			t.Errorf("SharedProjectRoot(%q) = %v", c.in, ok)
		}
		if c.shared {
			// A shared path has no owner and is nobody's own tree, whoever asks.
			if r.HasOwner() || r.Owner() != "" || r.OwnedBy("alice") || r.OwnedByOrUnowned("alice") {
				t.Errorf("%q: shared path claims an owner", c.in)
			}
			if r.IsProject() {
				t.Errorf("%q: a shared Crew must not be a per-user project (Project() callers would treat it as the caller's own)", c.in)
			}
			// Physical never puts it under a user.
			if got := r.Physical("alice"); got != r.Logical() {
				t.Errorf("%q: Physical = %q", c.in, got)
			}
			if got := r.PhysicalKeepOwner("alice"); got != r.Logical() {
				t.Errorf("%q: PhysicalKeepOwner = %q", c.in, got)
			}
			if got := CanonicalFor("alice", c.in); got != r.Logical() || got != CanonicalFor("bob", c.in) {
				t.Errorf("%q: CanonicalFor = %q", c.in, got)
			}
		}
	}
	if SharedProjectPath("sde-1a2b", "db", "x") != "Crew/sde-1a2b/db/x" {
		t.Error("SharedProjectPath")
	}
	// A shared path never equals anything in a user's tree.
	if MustParse("Crew/a").SameFor("alice", MustParse("Chats/Work/projects/a")) {
		t.Error("shared path equals the owner's legacy path")
	}
	for in, want := range map[string]struct {
		project string
		shared  bool
		ok      bool
	}{
		"Crew/a/db":                         {"a", true, true},
		"_users/o/Chats/Work/projects/a/db": {"a", false, true},
		"Chats/Work/projects/a":             {"a", false, true},
		"Chats/Code/projects/a":             {"", false, false},
		"Crew":                              {"", false, false},
	} {
		project, shared, ok := MustParse(in).AnyCrewProject()
		if project != want.project || shared != want.shared || ok != want.ok {
			t.Errorf("AnyCrewProject(%q) = %q %v %v", in, project, shared, ok)
		}
	}
}
