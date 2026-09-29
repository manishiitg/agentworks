package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docs/bugs/pulse_platform_issue_register.md is the one shared index that every
// PLAT ticket links from, and every parallel agent session edits it. That makes
// it a genuine concurrent-write collision point, with a failure mode that is
// silent by construction:
//
//   - the ticket FILE is its own file, so two sessions never conflict on it and
//     it always survives;
//   - the ROW lives in the shared register, so a session that rewrites a region
//     of that file can drop another session's row;
//   - the result is an orphaned ticket — still on disk, invisible from the index,
//     and indistinguishable from "no such ticket" to anyone who goes looking.
//
// This happened on 2026-08-17: commit 29b1241e (filing PLAT-119) deleted the
// PLAT-123 row from both the detail table and the quick index while leaving
// plat-123.md in place. Nothing caught it. It surfaced only because an unrelated
// rebase happened to touch the same lines and raised a conflict — had that
// session edited a different region, git would have merged cleanly and kept the
// deletion.
//
// So this asserts the invariant directly rather than relying on that luck.
//
// Ticket files live in per-category subdirectories of docs/bugs/pulse_platform/
// (see docs/bugs/pulse_platform_categories.md); the check walks the whole tree
// and matches register hrefs with their category prefix.
func TestEveryPulsePlatformTicketIsLinkedFromTheRegister(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	registerPath := filepath.Join(repoRoot, "docs", "bugs", "pulse_platform_issue_register.md")
	ticketsDir := filepath.Join(repoRoot, "docs", "bugs", "pulse_platform")

	registerBytes, err := os.ReadFile(registerPath)
	if err != nil {
		// The register is a docs artifact, not a build input. A checkout that
		// does not ship docs/ should skip rather than fail.
		t.Skipf("register not readable at %s: %v", registerPath, err)
	}
	register := string(registerBytes)

	var onDisk []string
	// A PLAT number identifies one issue even when files live in different
	// categories. The former chat-reliability/plat-339.md pointer collided
	// with an unrelated security-sandbox ticket and made references ambiguous.
	byID := map[string]string{}
	issueName := regexp.MustCompile(`^plat-(\d+)(?:-[A-Za-z0-9._-]+)?\.(?:md|html)$`)
	err = filepath.WalkDir(ticketsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".md") || strings.HasSuffix(entry.Name(), ".html")) {
			return nil
		}
		rel, relErr := filepath.Rel(ticketsDir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if match := issueName.FindStringSubmatch(entry.Name()); match != nil {
			if previous, exists := byID[match[1]]; exists {
				t.Errorf("PLAT-%s has two ticket files: %s and %s", match[1], previous, rel)
			} else {
				byID[match[1]] = rel
			}
		}
		onDisk = append(onDisk, rel)
		return nil
	})
	if err != nil {
		t.Skipf("tickets dir not readable at %s: %v", ticketsDir, err)
	}

	// Match the link target rather than the display text: a row may render its
	// id differently (PLAT-073 links plat-073-remaining-board.md), but the href
	// is exactly the path under pulse_platform/ and is what actually has to resolve.
	linked := map[string]bool{}
	for _, m := range regexp.MustCompile(`pulse_platform/((?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+\.(?:md|html))`).FindAllStringSubmatch(register, -1) {
		linked[m[1]] = true
	}

	var orphaned []string
	for _, name := range onDisk {
		if !linked[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(orphaned)
	if len(orphaned) > 0 {
		t.Fatalf("ticket file(s) exist but are not linked from docs/bugs/pulse_platform_issue_register.md: %v\n"+
			"An unlinked ticket is invisible from the index. Most often this means a concurrent edit dropped the row "+
			"while leaving the file — restore the row rather than deleting the ticket.", orphaned)
	}

	// The reverse direction: a link whose file was never created (or was renamed
	// without updating the row) is a dead link in the index.
	var dangling []string
	for name := range linked {
		if _, statErr := os.Stat(filepath.Join(ticketsDir, name)); statErr != nil {
			dangling = append(dangling, name)
		}
	}
	sort.Strings(dangling)
	if len(dangling) > 0 {
		t.Fatalf("register links ticket file(s) that do not exist: %v", dangling)
	}
}
