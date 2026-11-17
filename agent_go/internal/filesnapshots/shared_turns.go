package filesnapshots

// Several chats may work in the same checkout at once; Git cannot tell their
// edits apart, so one reply's card may include another's (#2922 task 1). The
// card says so only when it really happened: when a reply finishes, every
// other reply of this Crew that finished after it started and changed the
// same checkout (or files outside Git) is marked on both records. A reply
// still running is marked when it finishes itself. The caller holds the
// store lock.
import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (s Store) markSharedTurns(r *Record) {
	if r.Review == nil || (len(r.Review.ChangedRoots) == 0 && !r.Review.ChangedLoose) {
		return
	}
	entries, err := os.ReadDir(filepath.Join(s.dir(), "turns"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || id == r.ID {
			continue
		}
		// A record is saved when its reply finishes; older files cannot overlap.
		if info, err := entry.Info(); err != nil || info.ModTime().Before(r.StartedAt) {
			continue
		}
		other, err := s.read(id)
		if err != nil || other.Review == nil || other.CompletedAt == nil || !other.CompletedAt.After(r.StartedAt) {
			continue
		}
		var roots []string
		for _, root := range r.Review.ChangedRoots {
			if slices.Contains(other.Review.ChangedRoots, root) {
				roots = append(roots, root)
			}
		}
		loose := r.Review.ChangedLoose && other.Review.ChangedLoose
		if len(roots) == 0 && !loose {
			continue
		}
		r.Review.SharedRoots = mergeRoots(r.Review.SharedRoots, roots)
		r.Review.SharedLoose = r.Review.SharedLoose || loose
		other.Review.SharedRoots = mergeRoots(other.Review.SharedRoots, roots)
		other.Review.SharedLoose = other.Review.SharedLoose || loose
		if err := s.save(other); err != nil {
			log.Printf("[FILE_SNAPSHOT] phase=shared_turns turn=%s other=%s err=%v", r.ID, other.ID, err)
			continue
		}
		log.Printf("[FILE_SNAPSHOT] phase=shared_turns turn=%s other=%s roots=%q loose=%t", r.ID, other.ID, roots, loose)
	}
}

func mergeRoots(have, add []string) []string {
	for _, root := range add {
		if !slices.Contains(have, root) {
			have = append(have, root)
		}
	}
	slices.Sort(have)
	return have
}
