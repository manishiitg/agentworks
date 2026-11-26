package dashboards

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// projectRoot keeps dashboard paths logical while using the same trusted Crew
// storage bindings as file reads. A dashboard can have code in the Crew folder
// and reports beside an adopted database; neither gets a second storage copy.
type projectRoot struct {
	primary       *os.Root
	docs, virtual string
	anchors       map[string]*os.Root
	mapped        bool
}

func openProjectRoot(docs, virtual string) (*projectRoot, error) {
	r := &projectRoot{docs: docs, virtual: virtual, anchors: map[string]*os.Root{}}
	physical, mapped, err := crewroot.Resolve(docs, virtual)
	if err != nil {
		return nil, err
	}
	r.mapped = mapped
	if mapped {
		crew, found, err := crewroot.Lookup(docs, virtual)
		if err != nil {
			return nil, err
		}
		if !found {
			layout, found, err := crewroot.LookupExecutable(docs, virtual)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fail(404, "project unavailable")
			}
			crew = layout.Crew
		}
		for _, dir := range []string{crew.Files, crew.Resources, crew.Private, physical} {
			if _, exists := r.anchors[dir]; exists {
				continue
			}
			opened, err := os.OpenRoot(dir)
			if err != nil {
				r.Close()
				return nil, err
			}
			r.anchors[dir] = opened
		}
		r.primary = r.anchors[physical]
		return r, nil
	}
	base, err := os.OpenRoot(docs)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	current := ""
	for _, part := range strings.Split(virtual, "/") {
		current = path.Join(current, part)
		info, err := base.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fail(403, "unsafe project root")
		}
	}
	r.primary, err = base.OpenRoot(virtual)
	if err != nil {
		return nil, err
	}
	r.anchors[filepath.Join(docs, filepath.FromSlash(virtual))] = r.primary
	return r, nil
}

func (r *projectRoot) route(name string) (*os.Root, string, error) {
	clean, err := wf.CleanRelative(name)
	if err != nil || clean != name {
		return nil, "", fail(403, "unsafe dashboard path")
	}
	if !r.mapped || name == "." {
		return r.primary, name, nil
	}
	target, found, err := crewroot.Resolve(r.docs, path.Join(r.virtual, name))
	if err != nil {
		return nil, "", fail(403, "unsafe dashboard storage path")
	}
	if !found {
		return nil, "", fail(404, "project unavailable")
	}
	// Prefer the closest admitted root when resource and file roots overlap.
	base := ""
	for dir := range r.anchors {
		if crewroot.Within(dir, target) && len(dir) > len(base) {
			base = dir
		}
	}
	if base == "" {
		return nil, "", fail(403, "dashboard path leaves project storage")
	}
	rel, err := filepath.Rel(base, target)
	return r.anchors[base], rel, err
}

func (r *projectRoot) Close() error {
	var errs []error
	for _, root := range r.anchors {
		errs = append(errs, root.Close())
	}
	return errors.Join(errs...)
}
func (r *projectRoot) Open(name string) (*os.File, error) {
	root, rel, err := r.route(name)
	if err != nil {
		return nil, err
	}
	return root.Open(rel)
}
func (r *projectRoot) OpenFile(name string, flags int, mode os.FileMode) (*os.File, error) {
	root, rel, err := r.route(name)
	if err != nil {
		return nil, err
	}
	return root.OpenFile(rel, flags, mode)
}
func (r *projectRoot) Stat(name string) (os.FileInfo, error) {
	root, rel, err := r.route(name)
	if err != nil {
		return nil, err
	}
	return root.Stat(rel)
}
func (r *projectRoot) Lstat(name string) (os.FileInfo, error) {
	root, rel, err := r.route(name)
	if err != nil {
		return nil, err
	}
	return root.Lstat(rel)
}
func (r *projectRoot) Mkdir(name string, mode os.FileMode) error {
	root, rel, err := r.route(name)
	if err != nil {
		return err
	}
	return root.Mkdir(rel, mode)
}
func (r *projectRoot) Chmod(name string, mode os.FileMode) error {
	root, rel, err := r.route(name)
	if err != nil {
		return err
	}
	return root.Chmod(rel, mode)
}
func (r *projectRoot) Remove(name string) error {
	root, rel, err := r.route(name)
	if err != nil {
		return err
	}
	return root.Remove(rel)
}
func (r *projectRoot) Rename(old, next string) error {
	from, a, err := r.route(old)
	if err != nil {
		return err
	}
	to, b, err := r.route(next)
	if err != nil {
		return err
	}
	if from != to {
		return fail(403, "dashboard rename leaves its storage root")
	}
	return from.Rename(a, b)
}
