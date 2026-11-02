package nativersync

import (
	"debug/macho"
	"path/filepath"
	"strings"
)

// A go-run backend has no bundled lib directory. Resolve the dylibs its main
// Mach-O image actually imports, using only that image's own LC_RPATH entries.
// Rpath directories are never granted, even if one names HOME, /, a module
// cache root or a credential parent. Every result is an existing Mach-O dylib
// file; Command gives it a literal read rule. DYLD_* is never consulted.
func executableLibraryFiles(executable string) ([]string, error) {
	main, err := macho.Open(executable)
	if err != nil {
		return nil, err
	}
	defer main.Close()
	base := filepath.Dir(executable)
	var roots []string
	for _, load := range main.Loads {
		if rpath, ok := load.(*macho.Rpath); ok {
			p := rpath.Path
			switch {
			case strings.HasPrefix(p, "@executable_path/"):
				p = filepath.Join(base, strings.TrimPrefix(p, "@executable_path/"))
			case strings.HasPrefix(p, "@loader_path/"):
				p = filepath.Join(base, strings.TrimPrefix(p, "@loader_path/"))
			}
			if !filepath.IsAbs(p) {
				continue
			}
			if real, e := filepath.EvalSymlinks(p); e == nil {
				p = real
			}
			roots = append(roots, filepath.Clean(p))
		}
	}
	withinRoots := func(p string) bool {
		for _, root := range roots {
			rel, e := filepath.Rel(root, p)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	type imported struct{ name, loader string }
	imports, err := main.ImportedLibraries()
	if err != nil {
		return nil, err
	}
	queue := make([]imported, 0, len(imports))
	for _, name := range imports {
		queue = append(queue, imported{name, base})
	}
	var files []string
	seen := map[string]bool{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		var candidates []string
		switch {
		case strings.HasPrefix(item.name, "@rpath/"):
			for _, root := range roots {
				candidates = append(candidates, filepath.Join(root, strings.TrimPrefix(item.name, "@rpath/")))
			}
		case strings.HasPrefix(item.name, "@loader_path/"):
			candidates = []string{filepath.Join(item.loader, strings.TrimPrefix(item.name, "@loader_path/"))}
		case strings.HasPrefix(item.name, "@executable_path/"):
			candidates = []string{filepath.Join(base, strings.TrimPrefix(item.name, "@executable_path/"))}
		case filepath.IsAbs(item.name):
			candidates = []string{item.name}
		}
		for _, p := range candidates {
			p, e := filepath.EvalSymlinks(p)
			if e != nil || !withinRoots(p) {
				continue
			}
			if seen[p] {
				break
			}
			library, e := macho.Open(p)
			if e != nil {
				continue
			}
			if library.Type != macho.TypeDylib || library.Cpu != main.Cpu {
				library.Close()
				continue
			}
			dependencies, e := library.ImportedLibraries()
			library.Close()
			if e != nil {
				return nil, e
			}
			seen[p] = true
			files = append(files, p)
			for _, name := range dependencies {
				queue = append(queue, imported{name, filepath.Dir(p)})
			}
			break
		}
	}
	return files, nil
}
