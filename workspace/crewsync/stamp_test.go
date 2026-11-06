package crewsync

import (
	"testing"
	"time"
)

// PLAT-856-552: size/mtime equality alone hid edits; a read's own timestamp
// second and unavailable identity must never bless a cached version either.
func TestStampRequiresStableIdentityAndNonRacyTimestamps(t *testing.T) {
	readAt := time.Unix(100, 500)
	base := Stamp{Size: 4, Mtime: time.Unix(90, 0).UnixNano(), Ctime: time.Unix(90, 0).UnixNano(), Device: 1, Inode: 2, Mode: 0600}
	if !base.Reusable(base, readAt) {
		t.Fatal("stable complete stamp was not reusable")
	}
	for _, change := range []func(*Stamp){
		func(s *Stamp) { s.Ctime++ },
		func(s *Stamp) { s.Inode++ },
		func(s *Stamp) { s.Mode = 0700 },
	} {
		current := base
		change(&current)
		if base.Reusable(current, readAt) {
			t.Fatalf("changed identity/ctime/mode was reused: %+v", current)
		}
	}
	for _, change := range []func(*Stamp){
		func(s *Stamp) { s.Ctime = 0 },
		func(s *Stamp) { s.Inode = 0 },
		func(s *Stamp) { s.Ctime = time.Unix(100, 0).UnixNano() },
		func(s *Stamp) { s.Mtime = time.Unix(100, 0).UnixNano() },
	} {
		racy := base
		change(&racy)
		if racy.Reusable(racy, readAt) {
			t.Fatalf("unknown/racy stamp was reused: %+v", racy)
		}
	}
}
