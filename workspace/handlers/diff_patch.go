package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	// "github.com/sergi/go-diff/diffmatchpatch" // Available for future use
	"github.com/spf13/viper"
)

const (
	diffPatchSubprocessTimeout = 5 * time.Second
	noNewlineMarker            = `\ No newline at end of file`
)

// DiffPatchDocument handles PATCH /api/documents/*filepath/diff
func DiffPatchDocument(c *gin.Context) {
	started := time.Now()
	filePathParam := c.Param("filepath")
	var req models.DiffPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")

	// Sanitize input path to ensure it's relative
	filePathParam = utils.SanitizeInputPath(filePathParam, docsDir)
	log.Printf("[DIFF_PATCH] start path=%s diff_bytes=%d", filePathParam, len(req.Diff))

	filePath := filepath.Join(docsDir, filePathParam)

	// Validate file path for security
	if !utils.IsValidFilePath(filePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file path",
			Error:   "File path contains invalid characters or attempts directory traversal",
		})
		return
	}

	// A missing target is represented as empty input until the patch has been
	// validated and applied. Do not pre-create the file: a /dev/null creation
	// diff handed to patch(1) with an already-existing target makes BSD patch
	// open /dev/tty and ask whether the patch should be reversed.
	createdNewFile := false
	var currentContent []byte
	if _, err := os.Stat(filePath); os.IsNotExist(err) && req.DryRun {
		createdNewFile = true
		currentContent = []byte{}
	} else if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(filePath), 0755); mkErr != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to create directory",
				Error:   mkErr.Error(),
			})
			return
		}
		createdNewFile = true
		currentContent = []byte{}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to inspect document",
			Error:   err.Error(),
		})
		return
	} else {
		currentContent, err = os.ReadFile(filePath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to read document",
				Error:   err.Error(),
			})
			return
		}
	}

	// Apply diff patch - try flexible approach first, fallback to strict patch command
	patchCtx, cancelPatch := context.WithTimeout(c.Request.Context(), diffPatchSubprocessTimeout)
	defer cancelPatch()
	newContent, err := applyDiffPatchFlexibleContext(patchCtx, string(currentContent), req.Diff)
	if err != nil {
		// Provide comprehensive error details with suggestions
		errorDetails := map[string]interface{}{
			"error":        err.Error(),
			"filepath":     filePathParam,
			"diff_bytes":   len(req.Diff),
			"diff_preview": diffPatchErrorPreview(req.Diff),
		}
		log.Printf("[DIFF_PATCH] apply failed path=%s diff_bytes=%d current_bytes=%d duration=%s error=%v", filePathParam, len(req.Diff), len(currentContent), time.Since(started), err)

		// Add helpful suggestions based on common errors
		var suggestions []string
		if strings.Contains(err.Error(), "malformed patch") {
			suggestions = []string{
				"Read the target file first with an available file-read mechanism",
				"Context lines (starting with SPACE) must exactly match the file",
				"Hunk headers (@@) must show correct line numbers",
				"Use proper unified diff format with ---/+++ headers",
				"Generate diffs like 'diff -U0' would produce",
				"Ensure diff ends with a newline character",
				"CRITICAL: Context lines must start with SPACE ( ), not minus (-)!",
			}
		} else if strings.Contains(err.Error(), "unexpected end") {
			suggestions = []string{
				"All context lines are included",
				"The diff ends properly with a newline",
				"No truncated lines in the diff",
				"Generate complete unified diff format",
				"Use exact current file content from an available read tool",
			}
		} else if strings.Contains(err.Error(), "diff validation failed") {
			suggestions = []string{
				"Diff has proper headers (--- a/file, +++ b/file)",
				"At least one hunk header (@@ -start,count +start,count @@)",
				"Diff ends with a newline character",
				"Read exact current content before generating the diff",
			}
		} else if strings.Contains(err.Error(), "patch hunk failed to apply") {
			suggestions = []string{
				"Read the target file first with an available file-read mechanism",
				"Copy context lines EXACTLY from the file (including spaces/tabs)",
				"Verify line numbers in hunk headers match actual file",
				"Ensure no extra whitespace or missing characters",
				"Test with a simple single-line addition first",
			}
		} else {
			suggestions = []string{
				"Read the target file first with an available file-read mechanism",
				"Ensure diff format follows unified diff standard",
				"Check that context lines match file content exactly",
				"Verify hunk headers have correct line numbers",
			}
		}

		errorDetails["suggestions"] = suggestions

		c.JSON(http.StatusBadRequest, models.APIResponse[models.DiffPatchResponse]{
			Success: false,
			Message: "Failed to apply diff patch",
			Error:   fmt.Sprintf("Failed to apply diff patch: %s", err.Error()),
			Data: models.DiffPatchResponse{
				Applied:      false,
				Suggestions:  suggestions,
				ErrorDetails: errorDetails,
			},
		})
		return
	}

	// The subprocess only edits a temporary file. Honor cancellation once more
	// before committing so a timed-out/disconnected request cannot mutate the
	// real workspace file after the caller has already been told it failed.
	if err := patchCtx.Err(); err != nil {
		log.Printf("[DIFF_PATCH] canceled before commit path=%s duration=%s error=%v", filePathParam, time.Since(started), err)
		return
	}

	// Apply-path-agnostic safety net: every strategy above (strict patch,
	// corrected-diff retry, the agent-generated-diff fallback) can return
	// successfully while still silently dropping part of a multi-hunk patch —
	// confirmed live on confida-login: applied:true was reported while a
	// hunk's changes were left unapplied and unrelated trailing content was
	// deleted. Refuse to report success, and refuse to write, if the actual
	// line-count change doesn't match what the diff's own +/- lines claim.
	if verifyErr := verifyDiffApplied(string(currentContent), req.Diff, newContent); verifyErr != nil {
		log.Printf("[DIFF_PATCH] verification failed path=%s duration=%s error=%v", filePathParam, time.Since(started), verifyErr)
		c.JSON(http.StatusBadRequest, models.APIResponse[models.DiffPatchResponse]{
			Success: false,
			Message: "Failed to apply diff patch",
			Error:   fmt.Sprintf("Failed to apply diff patch: %s", verifyErr.Error()),
			Data: models.DiffPatchResponse{
				Applied: false,
				Suggestions: []string{
					"Re-read the current file content and retry with a single, precisely-scoped diff",
					"Split a large multi-hunk patch into separate diff_patch_workspace_file calls, one hunk at a time, and verify each before the next",
				},
			},
		})
		return
	}

	if req.DryRun {
		log.Printf("[DIFF_PATCH] dry run ok path=%s would_create=%t diff_bytes=%d duration=%s", filePathParam, createdNewFile, len(req.Diff), time.Since(started))
		c.JSON(http.StatusOK, models.APIResponse[models.DiffPatchResponse]{
			Success: true,
			Message: "Diff patch would apply",
			Data:    models.DiffPatchResponse{Applied: false, DryRun: true, WouldCreate: createdNewFile},
		})
		return
	}

	// Write updated content back to file
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to update document",
			Error:   err.Error(),
		})
		return
	}
	log.Printf("[DIFF_PATCH] success path=%s created_new=%t diff_bytes=%d current_bytes=%d new_bytes=%d duration=%s", filePathParam, createdNewFile, len(req.Diff), len(currentContent), len(newContent), time.Since(started))

	// Return simple success response
	c.JSON(http.StatusOK, models.APIResponse[models.DiffPatchResponse]{
		Success: true,
		Message: "Document diff-patched successfully",
		Data:    models.DiffPatchResponse{Applied: true},
	})
}

// diffClaimedLineDelta counts the net line-count change every hunk's own
// +/- prefixed body lines claim, independent of the @@ header counts (an
// LLM-supplied header count can itself be wrong — that's exactly why
// correctAgentGeneratedDiff exists). Only lines inside a hunk (after an @@
// line) are counted.
func diffClaimedLineDelta(diffContent string) int {
	if isApplyPatchFormat(diffContent) {
		return applyPatchClaimedLineDelta(diffContent)
	}
	delta := 0
	inHunk := false
	for _, line := range strings.Split(diffContent, "\n") {
		if strings.HasPrefix(line, "@@") {
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case line == noNewlineMarker:
			// Metadata about the preceding line, not a line itself.
		case strings.HasPrefix(line, "+"):
			delta++
		case strings.HasPrefix(line, "-"):
			delta--
		}
	}
	return delta
}

// countContentLines counts real lines the way a unified diff's +/- counting
// does: an empty string is 0 lines, and a single trailing newline does not
// count as an extra blank line. strings.Split(content, "\n") alone gets this
// wrong at both ends — "" splits to one empty element (implying 1 line for
// 0 real lines), and a trailing "\n" splits to one extra empty element (implying
// one more line than the file actually has).
func countContentLines(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

// verifyDiffApplied is a cheap, apply-path-agnostic safety net, not a full
// hunk-by-hunk re-verification. It checks one invariant: the actual
// line-count change between originalContent and newContent must equal what
// diffContent's own +/- lines claim, regardless of which internal strategy
// (strict patch, corrected-diff retry, the agent-generated-diff fallback)
// produced newContent. Every one of those strategies can return successfully
// while still silently dropping part of a multi-hunk patch or deleting
// unrelated content — this catches that failure shape without needing to
// know which strategy ran. It is necessary, not sufficient: a corruption
// that happens to preserve the total line count (e.g. two unrelated lines
// swapped) is not caught by this check alone.
func verifyDiffApplied(originalContent, diffContent, newContent string) error {
	expectedDelta := diffClaimedLineDelta(diffContent)
	actualDelta := countContentLines(normalizeLineEndings(newContent)) - countContentLines(normalizeLineEndings(originalContent))
	if actualDelta != expectedDelta {
		return fmt.Errorf("diff apply produced an unexpected line-count change (diff claims %+d lines, result changed by %+d) — refusing to report success; part of the patch was likely silently dropped or corrupted",
			expectedDelta, actualDelta)
	}
	return nil
}

func diffPatchErrorPreview(diff string) string {
	const maxPreviewBytes = 4000
	if len(diff) <= maxPreviewBytes {
		return diff
	}
	return diff[:maxPreviewBytes] + fmt.Sprintf("\n... truncated %d bytes ...", len(diff)-maxPreviewBytes)
}

// boundedContextMismatchHint reports the file content at the closest position
// the fallback matcher actually found, so a failed hunk carries a corrected
// retry's worth of evidence instead of only telling the caller to go read the
// whole file again.
//
// The fallback matcher already scans every candidate position computing
// mismatches — bestMatchIndex and minMismatches are that scan's own output,
// discarded once no position met the zero-mismatch bar. On 2026-08-04 the one
// occurrence with full evidence, a 150KB file, took 4 extra calls to recover:
// one to learn the match failed, then more to re-read the file and locate the
// real content by hand. That cost is what this closes — not by relaxing the
// zero-mismatch rule that protects against corrupting a structured file, only
// by not throwing away evidence the matcher already had.
//
// The window is capped at both the hunk's own expected line count (comparing
// like-for-like — no reason to show more than what the hunk itself claimed)
// and a hard byte budget, so one pathologically long line cannot make the
// error message itself the next problem.
func boundedContextMismatchHint(resultLines []string, bestMatchIndex, minMismatches, expectedLineCount int) string {
	if bestMatchIndex < 0 || bestMatchIndex >= len(resultLines) {
		return ""
	}
	const maxHintBytes = 2000
	end := bestMatchIndex + expectedLineCount
	if end > len(resultLines) {
		end = len(resultLines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Closest match: %d of %d expected lines differ, starting at file line %d. Current content there:\n",
		minMismatches, expectedLineCount, bestMatchIndex+1)
	for i := bestMatchIndex; i < end; i++ {
		line := fmt.Sprintf("%6d| %s\n", i+1, resultLines[i])
		if b.Len()+len(line) > maxHintBytes {
			fmt.Fprintf(&b, "... truncated, %d more line(s) in this window ...\n", end-i)
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// normalizeLineEndings converts all line endings to LF for consistent patch processing
func normalizeLineEndings(content string) string {
	// Replace CRLF (\r\n) with LF (\n)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	// Replace CR (\r) with LF (\n)
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}

// validateDiffFormat performs basic validation on the diff format
func validateDiffFormat(diffContent string) error {
	lines := strings.Split(diffContent, "\n")
	if len(lines) < 3 {
		return fmt.Errorf("diff too short - must have at least headers and one hunk")
	}

	// Check for proper headers
	if !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") {
		return fmt.Errorf("missing or malformed diff headers (---/+++)")
	}

	// Check for at least one hunk header
	foundHunk := false
	inHunk := false
	for i, line := range lines {
		if strings.HasPrefix(line, "@@") && strings.HasSuffix(line, "@@") {
			foundHunk = true
			inHunk = true
			continue
		}
		if line == noNewlineMarker {
			if !inHunk {
				return fmt.Errorf("malformed diff line %d: no-newline marker is outside a hunk", i+1)
			}
			continue
		}

		// Check diff lines within hunks
		if inHunk && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) {
			// This is a valid diff line
			continue
		} else if inHunk && line == "" && i == len(lines)-1 {
			// strings.Split exposes the required trailing newline as one final
			// empty element. A blank line inside a hunk must still carry a space
			// context prefix; accepting it here can silently truncate the patch.
			inHunk = false
		} else if inHunk && line == "" {
			return fmt.Errorf("malformed diff line %d: blank context lines must start with a space", i+1)
		} else if inHunk && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "+") && line != "" {
			// Invalid line in hunk
			return fmt.Errorf("malformed diff line %d: %q - diff lines must start with space (context), - (removal), or + (addition)", i+1, line)
		}
	}

	if !foundHunk {
		return fmt.Errorf("no hunk headers found (lines starting with @@)")
	}

	// Check that diff ends with newline
	if !strings.HasSuffix(diffContent, "\n") {
		return fmt.Errorf("diff must end with a newline character")
	}

	return nil
}

// applyDiffPatch applies a unified diff to the file content using the standard patch command
func applyDiffPatch(ctx context.Context, currentContent, diffContent string) (string, error) {
	// Normalize line endings for consistent processing
	currentContent = normalizeLineEndings(currentContent)
	diffContent = normalizeLineEndings(diffContent)

	// Ensure diff ends with a newline
	if !strings.HasSuffix(diffContent, "\n") {
		diffContent += "\n"
	}

	// Validate diff format before applying
	if err := validateDiffFormat(diffContent); err != nil {
		return "", fmt.Errorf("diff validation failed: %w", err)
	}

	fmt.Printf("🔍 Applying diff patch with normalized line endings\n")

	// Create temporary files for the patch command
	tempFile, err := os.CreateTemp("", "file_*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	patchFile, err := os.CreateTemp("", "patch_*.diff")
	if err != nil {
		return "", fmt.Errorf("failed to create temp patch file: %w", err)
	}
	defer os.Remove(patchFile.Name())

	// Write current content to temp file
	if _, err := tempFile.WriteString(currentContent); err != nil {
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}
	tempFile.Close()

	// Write diff content to patch file
	if _, err := patchFile.WriteString(diffContent); err != nil {
		return "", fmt.Errorf("failed to write to patch file: %w", err)
	}
	patchFile.Close()

	// Apply only against complete context. The exact-match fallback below can
	// recover stale line numbers without allowing patch(1) to discard context
	// and place an otherwise ambiguous hunk under the wrong duplicate heading.
	// -f is the portable non-interactive mode and, unlike -t/--batch, assumes
	// the supplied diff is forward rather than reversed. Stdin alone is not a
	// safety boundary: BSD patch opens /dev/tty directly when it wants an answer.
	cmd := exec.CommandContext(ctx, "patch", "-f", "-u", "-F", "0", tempFile.Name(), patchFile.Name())
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("patch command canceled: %w", ctxErr)
		}
		// Provide more specific error messages based on patch output
		outputStr := string(output)
		if strings.Contains(outputStr, "malformed patch") {
			return "", fmt.Errorf("malformed patch: %s", outputStr)
		} else if strings.Contains(outputStr, "unexpected end") {
			return "", fmt.Errorf("unexpected end of file in patch: %s", outputStr)
		} else if strings.Contains(outputStr, "Hunk") && strings.Contains(outputStr, "FAILED") {
			return "", fmt.Errorf("patch hunk failed to apply: %s", outputStr)
		}
		return "", fmt.Errorf("patch command failed: %w, output: %s", err, outputStr)
	}

	// Read the patched content
	patchedContent, err := os.ReadFile(tempFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read patched file: %w", err)
	}

	return string(patchedContent), nil
}

// safeMalformedContextLines identifies context lines where an agent omitted the
// unified-diff prefix. A line is repairable only when the hunk's complete
// old-side sequence has an explicit context/removal anchor and occurs exactly
// once as a contiguous block in the current file.
func safeMalformedContextLines(diffLines, currentLines []string) map[int]bool {
	safe := make(map[int]bool)
	for start := 0; start < len(diffLines); start++ {
		if !strings.HasPrefix(diffLines[start], "@@") {
			continue
		}

		var oldSide []string
		var candidates []int
		hasAnchor := false
		end := start + 1
		for ; end < len(diffLines); end++ {
			line := diffLines[end]
			if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
				break
			}
			if line == "" {
				if end == len(diffLines)-1 {
					break
				}
				oldSide = append(oldSide, "")
				candidates = append(candidates, end)
				continue
			}
			if line == noNewlineMarker {
				continue
			}
			switch line[0] {
			case ' ':
				oldSide = append(oldSide, line[1:])
				hasAnchor = true
			case '-':
				payload := line[1:]
				payloadExists := slices.Contains(currentLines, payload)
				if strings.HasPrefix(line, "- ") && !payloadExists && slices.Contains(currentLines, line) {
					oldSide = append(oldSide, line)
					candidates = append(candidates, end)
				} else {
					oldSide = append(oldSide, payload)
					hasAnchor = true
				}
			case '+':
				// Additions do not participate in old-side matching.
			default:
				if slices.Contains(currentLines, line) {
					oldSide = append(oldSide, line)
					candidates = append(candidates, end)
				} else {
					oldSide = nil
					candidates = nil
					end = len(diffLines)
				}
			}
		}
		if !hasAnchor || len(candidates) == 0 || len(oldSide) == 0 {
			continue
		}

		matches := 0
		for i := 0; i+len(oldSide) <= len(currentLines); i++ {
			if slices.Equal(currentLines[i:i+len(oldSide)], oldSide) {
				matches++
			}
		}
		if matches == 1 {
			for _, candidate := range candidates {
				safe[candidate] = true
			}
		}
		start = end - 1
	}
	return safe
}

// correctAgentGeneratedDiff repairs hunk counts and only the malformed bullet
// context pattern that can be proven against the current file.
func correctAgentGeneratedDiff(diffContent, currentContent string) string {
	lines := strings.Split(diffContent, "\n")
	corrected := make([]string, 0, len(lines))
	currentLines := strings.Split(currentContent, "\n")
	safeContext := safeMalformedContextLines(lines, currentLines)

	type hunkInfo struct {
		index    int
		oldStart string
		newStart string
		oldCount int
		newCount int
	}

	var currentHunk *hunkInfo

	for i, line := range lines {
		// Check if we're entering a hunk
		if strings.HasPrefix(line, "@@") {
			// Finalize previous hunk if any
			if currentHunk != nil {
				corrected[currentHunk.index] = fmt.Sprintf("@@ -%s,%d +%s,%d @@",
					currentHunk.oldStart, currentHunk.oldCount,
					currentHunk.newStart, currentHunk.newCount)
			}

			// Parse new hunk header
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				oldRange := strings.TrimPrefix(parts[1], "-")
				newRange := strings.TrimPrefix(parts[2], "+")

				oldStart := oldRange
				if commaIdx := strings.Index(oldRange, ","); commaIdx != -1 {
					oldStart = oldRange[:commaIdx]
				}
				newStart := newRange
				if commaIdx := strings.Index(newRange, ","); commaIdx != -1 {
					newStart = newRange[:commaIdx]
				}

				// Fix invalid line references like "last", "end", etc.
				if oldStart == "last" || oldStart == "end" || oldStart == "start" {
					oldStart = "1"
				}
				if newStart == "last" || newStart == "end" || newStart == "start" {
					newStart = "1"
				}

				currentHunk = &hunkInfo{
					index:    len(corrected),
					oldStart: oldStart,
					newStart: newStart,
					oldCount: 0,
					newCount: 0,
				}
				corrected = append(corrected, line) // Placeholder to be updated
			} else {
				corrected = append(corrected, line)
				currentHunk = nil
			}
			continue
		}

		if currentHunk != nil {
			if line == noNewlineMarker {
				// This standard unified-diff metadata describes the preceding
				// content line and does not contribute to either hunk count.
				corrected = append(corrected, line)
			} else if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				if strings.HasPrefix(line, " ") {
					currentHunk.oldCount++
					currentHunk.newCount++
				} else if strings.HasPrefix(line, "-") {
					if safeContext[i] {
						line = " " + line
						currentHunk.oldCount++
						currentHunk.newCount++
					} else {
						currentHunk.oldCount++
					}
				} else if strings.HasPrefix(line, "+") {
					currentHunk.newCount++
				}
				corrected = append(corrected, line)
			} else if safeContext[i] {
				currentHunk.oldCount++
				currentHunk.newCount++
				corrected = append(corrected, " "+line)
			} else {
				// Non-diff line ends the hunk
				corrected[currentHunk.index] = fmt.Sprintf("@@ -%s,%d +%s,%d @@",
					currentHunk.oldStart, currentHunk.oldCount,
					currentHunk.newStart, currentHunk.newCount)
				currentHunk = nil
				corrected = append(corrected, line)
			}
		} else {
			corrected = append(corrected, line)
		}
	}

	// Finalize last hunk
	if currentHunk != nil {
		corrected[currentHunk.index] = fmt.Sprintf("@@ -%s,%d +%s,%d @@",
			currentHunk.oldStart, currentHunk.oldCount,
			currentHunk.newStart, currentHunk.newCount)
	}

	result := strings.Join(corrected, "\n")
	// Ensure the result ends with a newline
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

func isExplicitCreationDiff(diffContent string) bool {
	lines := strings.Split(normalizeLineEndings(diffContent), "\n")
	if len(lines) == 0 {
		return false
	}
	fields := strings.Fields(lines[0])
	return len(fields) >= 2 && fields[0] == "---" && fields[1] == "/dev/null"
}

// hasZeroLineInsertionHunk reports a hunk whose old side is empty at line 0.
func hasZeroLineInsertionHunk(diffContent string) bool {
	for _, line := range strings.Split(normalizeLineEndings(diffContent), "\n") {
		match := creationHunkHeader.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if match[1] == "0" && (match[2] == "" || match[2] == "0") {
			return true
		}
	}
	return false
}

var creationHunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@$`)

// applyExplicitCreationDiff handles the one case patch(1) cannot be allowed to
// decide interactively. A creation patch has no old-side content, so its result
// is deterministic and does not require an external process.
func applyExplicitCreationDiff(currentContent, diffContent string) (string, error) {
	diffContent = normalizeLineEndings(diffContent)
	if !strings.HasSuffix(diffContent, "\n") {
		diffContent += "\n"
	}
	if err := validateDiffFormat(diffContent); err != nil {
		return "", fmt.Errorf("creation diff validation failed: %w", err)
	}

	lines := strings.Split(diffContent, "\n")
	hunkCount := 0
	declaredNewCount := -1
	var additions []string
	inHunk := false
	noTrailingNewline := false
	for index, line := range lines[2:] {
		lineNumber := index + 3
		if strings.HasPrefix(line, "@@") {
			hunkCount++
			if hunkCount > 1 {
				return "", fmt.Errorf("creation diff must contain exactly one hunk")
			}
			match := creationHunkHeader.FindStringSubmatch(line)
			if match == nil {
				return "", fmt.Errorf("malformed creation hunk header on line %d", lineNumber)
			}
			oldCount := 1
			if match[2] != "" {
				if _, err := fmt.Sscanf(match[2], "%d", &oldCount); err != nil {
					return "", fmt.Errorf("invalid old-line count on line %d: %w", lineNumber, err)
				}
			}
			if match[1] != "0" || oldCount != 0 {
				return "", fmt.Errorf("creation diff hunk must have an empty old side")
			}
			declaredNewCount = 1
			if match[4] != "" {
				if _, err := fmt.Sscanf(match[4], "%d", &declaredNewCount); err != nil {
					return "", fmt.Errorf("invalid new-line count on line %d: %w", lineNumber, err)
				}
			}
			inHunk = true
			continue
		}
		if !inHunk {
			if line == "" && index == len(lines[2:])-1 {
				continue
			}
			return "", fmt.Errorf("unexpected creation diff content on line %d", lineNumber)
		}
		if line == noNewlineMarker {
			if len(additions) == 0 || index != len(lines[2:])-2 {
				return "", fmt.Errorf("creation diff no-newline marker must follow the final addition")
			}
			noTrailingNewline = true
			continue
		}
		if line == "" && index == len(lines[2:])-1 {
			continue
		}
		if !strings.HasPrefix(line, "+") {
			return "", fmt.Errorf("creation diff line %d must be an addition", lineNumber)
		}
		additions = append(additions, line[1:])
	}
	if hunkCount != 1 {
		return "", fmt.Errorf("creation diff must contain exactly one hunk")
	}
	if declaredNewCount != len(additions) {
		return "", fmt.Errorf("creation diff declares %d new lines but contains %d", declaredNewCount, len(additions))
	}

	createdContent := strings.Join(additions, "\n")
	if !noTrailingNewline {
		createdContent += "\n"
	}
	if strings.TrimSpace(currentContent) == "" {
		return createdContent, nil
	}
	if normalizeLineEndings(currentContent) == createdContent {
		return currentContent, nil
	}
	return "", fmt.Errorf("creation diff targets a file that already exists with different content; read the current file and send an update diff")
}

// applyDiffPatchFlexible tries multiple approaches to apply diffs
func applyDiffPatchFlexibleContext(ctx context.Context, currentContent, diffContent string) (string, error) {
	fmt.Printf("🔍 Attempting flexible diff patch approach\n")
	currentContent = normalizeLineEndings(currentContent)
	diffContent = normalizeLineEndings(diffContent)
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Codex/Cursor's native "*** Begin Patch" envelope has no line numbers.
	if isApplyPatchFormat(diffContent) {
		return applyApplyPatchFormat(currentContent, diffContent)
	}

	var result string
	var err error

	// First, try to correct common agent-generated patterns
	correctedDiff := correctAgentGeneratedDiff(diffContent, currentContent)
	if isExplicitCreationDiff(correctedDiff) {
		result, creationErr := applyExplicitCreationDiff(currentContent, correctedDiff)
		if creationErr != nil {
			return "", creationErr
		}
		return result, nil
	}
	// A pure-insertion hunk at line 0 ("@@ -0,0 +1,N @@") with ordinary
	// headers is a creation in disguise; patch(1) would prepend it to an
	// existing file (PUL-4AD362CD: a degraded rerun doubled daily_report.json).
	if strings.TrimSpace(currentContent) != "" && hasZeroLineInsertionHunk(correctedDiff) {
		return "", fmt.Errorf("diff inserts new content before line 1 of a file that already exists; read the current file and send an update diff that replaces its lines")
	}
	if correctedDiff != diffContent {
		fmt.Printf("🔧 Applied automatic corrections to agent-generated diff\n")
		fmt.Printf("🔍 Corrected diff:\n%s\n", diffPatchErrorPreview(correctedDiff))
		// Try the corrected diff first
		result, err = applyDiffPatch(ctx, currentContent, correctedDiff)
		if err == nil {
			fmt.Printf("✅ Corrected diff applied successfully\n")
			return result, nil
		}
		fmt.Printf("⚠️ Corrected diff failed strict patch, trying exact fallback: %v\n", err)
		result, fallbackErr := applyAgentGeneratedDiffFallback(currentContent, correctedDiff)
		if fallbackErr == nil {
			fmt.Printf("✅ Corrected diff applied through exact fallback\n")
			return result, nil
		}
		fmt.Printf("⚠️ Corrected diff fallback failed, trying original: %v\n", fallbackErr)
	}

	// Try the original diff
	result, err = applyDiffPatch(ctx, currentContent, diffContent)
	if err == nil {
		fmt.Printf("✅ Original diff applied successfully\n")
		return result, nil
	}

	fmt.Printf("⚠️ Original diff failed, trying fallback: %v\n", err)

	// Fallback approach
	result, err = applyAgentGeneratedDiffFallback(currentContent, diffContent)
	if err != nil {
		return "", fmt.Errorf("fallback approach failed: %w", err)
	}

	fmt.Printf("✅ Fallback approach succeeded\n")
	return result, nil
}

// applyDiffPatchFlexible retains the direct test/helper contract while the HTTP
// handler uses its request-scoped context above.
func applyDiffPatchFlexible(currentContent, diffContent string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diffPatchSubprocessTimeout)
	defer cancel()
	return applyDiffPatchFlexibleContext(ctx, currentContent, diffContent)
}

// applyAgentGeneratedDiffFallback handles agent-generated diffs by parsing the intent

func applyAgentGeneratedDiffFallback(currentContent, diffContent string) (string, error) {

	fmt.Printf("🔍 Trying fallback approach for agent-generated diffs\n")

	lines := strings.Split(diffContent, "\n")
	hasOldHeader := false
	hasNewHeader := false
	for _, line := range lines {
		hasOldHeader = hasOldHeader || strings.HasPrefix(line, "--- ")
		hasNewHeader = hasNewHeader || strings.HasPrefix(line, "+++ ")
	}
	if !hasOldHeader || !hasNewHeader {
		return "", fmt.Errorf("missing or malformed diff headers (---/+++)")
	}

	resultLines := strings.Split(currentContent, "\n")

	type hunk struct {
		lines []string
	}

	var hunks []hunk

	var currentHunk *hunk

	for lineIndex, line := range lines {

		if strings.HasPrefix(line, "@@") {

			if currentHunk != nil {

				hunks = append(hunks, *currentHunk)

			}

			currentHunk = &hunk{}

			continue

		}

		if currentHunk != nil {

			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {

				currentHunk.lines = append(currentHunk.lines, line)

			} else if line == "" && len(currentHunk.lines) > 0 && lineIndex < len(lines)-1 {

				// Empty line inside hunk is treated as context

				currentHunk.lines = append(currentHunk.lines, " ")

			} else if line != "" {

				return "", fmt.Errorf("malformed hunk line %q: context, removals, and additions must use a diff prefix", line)

			}

		}

	}

	if currentHunk != nil {

		hunks = append(hunks, *currentHunk)

	}

	if len(hunks) == 0 {

		return "", fmt.Errorf("no hunks found in diff")

	}

	// Apply hunks one by one

	for _, h := range hunks {

		// For each hunk, try to find a match in the current resultLines

		// A match is where all ' ' and '-' lines in the hunk match the lines in the file

		matchIndex := -1

		// Collect expected lines (context and removals)

		var expectedLines []string

		for _, hl := range h.lines {

			if !strings.HasPrefix(hl, "+") {

				expectedLines = append(expectedLines, hl[1:])

			}

		}

		if len(expectedLines) == 0 {
			return "", fmt.Errorf("pure-addition fallback hunk has no context; provide an exact unified diff so insertion location is unambiguous")
		}

		fmt.Printf("🔍 Attempting to match hunk with %d expected lines against %d file lines\n", len(expectedLines), len(resultLines))

		// Fuzzy match: find position with minimum mismatches

		bestMatchIndex := -1
		bestMatchCount := 0
		exactMatchCount := 0

		minMismatches := len(expectedLines) + 1

		// Fallback ignores line numbers, so require an exact content match.
		// Whitespace is normalized below, but content mismatches must never be
		// guessed because removals could otherwise target the wrong block.
		maxAllowedMismatches := 0

		for i := 0; i <= len(resultLines)-len(expectedLines); i++ {

			mismatches := 0

			for j, el := range expectedLines {

				if strings.TrimSpace(resultLines[i+j]) != strings.TrimSpace(el) {

					mismatches++

					// Breaking at "worse than the best candidate found so far"
					// (not at maxAllowedMismatches, which is 0) still finds every
					// exact match and never changes whether the hunk applies —
					// but it lets a candidate that is genuinely close (say, one
					// line off in a twenty-line hunk) keep counting instead of
					// being capped at exactly 1 like every other non-matching
					// position. Capping every miss at 1 made bestMatchIndex
					// effectively arbitrary — the first position scanned, not
					// the closest one — which made a "here is the closest match"
					// hint on failure meaningless. This is what makes it real.
					if mismatches > minMismatches {

						break

					}

				}

			}

			if mismatches == 0 {
				exactMatchCount++
			}

			if mismatches < minMismatches {

				minMismatches = mismatches

				bestMatchIndex = i
				bestMatchCount = 1

			} else if mismatches == minMismatches {

				bestMatchCount++

			}

		}
		if exactMatchCount > 1 {
			return "", fmt.Errorf("fallback hunk context is ambiguous: %d exact matches; provide current line numbers or more unique context", exactMatchCount)
		}

		if minMismatches <= maxAllowedMismatches {

			matchIndex = bestMatchIndex

			fmt.Printf("✅ Found exact fallback match at line %d\n", matchIndex+1)

		}

		if matchIndex != -1 {

			// Found a match! Apply changes

			var newResultLines []string

			newResultLines = append(newResultLines, resultLines[:matchIndex]...)

			// Track which expected line we are on

			expIdx := 0

			for _, hl := range h.lines {

				if strings.HasPrefix(hl, " ") {

					// Context line, keep it

					newResultLines = append(newResultLines, resultLines[matchIndex+expIdx])

					expIdx++

				} else if strings.HasPrefix(hl, "-") {

					// Removal line, skip it

					expIdx++

				} else if strings.HasPrefix(hl, "+") {

					// Addition line, add it

					newResultLines = append(newResultLines, hl[1:])

				}

			}

			newResultLines = append(newResultLines, resultLines[matchIndex+expIdx:]...)

			resultLines = newResultLines

			fmt.Printf("✅ Applied hunk with %d lines via fallback match\n", len(h.lines))

		} else {

			// No match found — return an error instead of blindly appending
			// additions to the bottom, which corrupts structured files (JSON, etc.)

			fmt.Printf("❌ Could not find match for hunk — refusing to apply to prevent corruption\n")

			if bestMatchCount > 1 {
				return "", fmt.Errorf("patch hunk failed to apply: could not find matching context lines in the file. %d locations are equally close (%d of %d expected lines differ); refusing to recommend an arbitrary location. Read the current file content and retry with more unique context", bestMatchCount, minMismatches, len(expectedLines))
			}
			hint := boundedContextMismatchHint(resultLines, bestMatchIndex, minMismatches, len(expectedLines))
			if hint == "" {
				return "", fmt.Errorf("patch hunk failed to apply: could not find matching context lines in the file (file has fewer lines than the hunk expects). Read the current file content with an available tool and retry with an accurate diff")
			}
			return "", fmt.Errorf("patch hunk failed to apply: could not find matching context lines in the file. %sRetry with a diff whose context matches the content above exactly", hint)

		}

	}

	result := strings.Join(resultLines, "\n")
	return result, nil
}

// isJSON is a helper to check if content is valid JSON

func isJSON(content string) bool {

	var js interface{}

	return json.Unmarshal([]byte(content), &js) == nil

}

// applyAdditionsToBottom appends additions to the end of the content,

// but tries to be smart about JSON structure.

func applyAdditionsToBottom(content string, additions []string) (string, error) {

	if len(additions) == 0 {

		return content, nil

	}

	result := content

	// Check if it's JSON

	var js interface{}

	isJSON := json.Unmarshal([]byte(result), &js) == nil

	if isJSON {

		trimmedResult := strings.TrimSpace(result)

		if strings.HasSuffix(trimmedResult, "}") {

			// Insert before the last closing brace

			lastBraceIndex := strings.LastIndex(result, "}")

			prefix := result[:lastBraceIndex]

			suffix := result[lastBraceIndex:]

			// Try to see if we need a comma

			trimmedPrefix := strings.TrimSpace(prefix)

			firstAddition := ""

			if len(additions) > 0 {

				firstAddition = strings.TrimSpace(additions[0])

			}

			if len(trimmedPrefix) > 0 && !strings.HasSuffix(trimmedPrefix, "{") && !strings.HasSuffix(trimmedPrefix, ",") && !strings.HasSuffix(trimmedPrefix, "[") && !strings.HasPrefix(firstAddition, ",") {

				prefix += ","

			}

			if !strings.HasSuffix(prefix, "\n") {

				prefix += "\n"

			}

			for i, addition := range additions {

				prefix += addition

				// Add comma between additions if needed

				if i < len(additions)-1 {

					trimmedAddition := strings.TrimSpace(addition)

					if !strings.HasSuffix(trimmedAddition, ",") && !strings.HasSuffix(trimmedAddition, "{") && !strings.HasSuffix(trimmedAddition, "[") {

						prefix += ","

					}

				}

				prefix += "\n"

			}

			result = prefix + suffix

			fmt.Printf("🔧 Inserted %d lines before last '}' for JSON fallback\n", len(additions))

		} else if strings.HasSuffix(trimmedResult, "]") {

			// Similar for array

			lastBracketIndex := strings.LastIndex(result, "]")

			prefix := result[:lastBracketIndex]

			suffix := result[lastBracketIndex:]

			// Try to see if we need a comma

			trimmedPrefix := strings.TrimSpace(prefix)

			firstAddition := ""

			if len(additions) > 0 {

				firstAddition = strings.TrimSpace(additions[0])

			}

			if len(trimmedPrefix) > 0 && !strings.HasSuffix(trimmedPrefix, "[") && !strings.HasSuffix(trimmedPrefix, ",") && !strings.HasSuffix(trimmedPrefix, "{") && !strings.HasPrefix(firstAddition, ",") {

				prefix += ","

			}

			if !strings.HasSuffix(prefix, "\n") {

				prefix += "\n"

			}

			for i, addition := range additions {

				prefix += addition

				// Add comma between additions if needed

				if i < len(additions)-1 {

					trimmedAddition := strings.TrimSpace(addition)

					if !strings.HasSuffix(trimmedAddition, ",") && !strings.HasSuffix(trimmedAddition, "{") && !strings.HasSuffix(trimmedAddition, "[") {

						prefix += ","

					}

				}

				prefix += "\n"

			}

			result = prefix + suffix

			fmt.Printf("🔧 Inserted %d lines before last ']' for JSON fallback\n", len(additions))

		} else {

			// Fallback to appending

			if !strings.HasSuffix(result, "\n") {

				result += "\n"

			}

			for _, addition := range additions {

				result += addition + "\n"

			}

			fmt.Printf("🔧 Appended %d lines to non-object/array JSON via fallback approach\n", len(additions))

		}

		// Final attempt to validate and pretty-print if it's JSON

		var finalJs interface{}

		if err := json.Unmarshal([]byte(result), &finalJs); err == nil {

			if indented, err := json.MarshalIndent(finalJs, "", "  "); err == nil {

				result = string(indented) + "\n"

				fmt.Printf("✅ Re-formatted fallback result as valid JSON\n")

			}

		} else {
			// Try to repair common JSON issues
			reTrailingComma := regexp.MustCompile(`,\s*([}\]])`)
			repaired := reTrailingComma.ReplaceAllString(result, "$1")

			// Try to add missing commas between lines that look like they should be separated by commas
			// This matches a line ending in a value (not comma, brace, or bracket)
			// followed by a line starting with a new value or key (not closing brace or bracket)
			reMissingComma := regexp.MustCompile(`([^,\[{\s])\n\s*([^}\]\s])`)
			repaired = reMissingComma.ReplaceAllString(repaired, "$1,\n$2")

			if err := json.Unmarshal([]byte(repaired), &finalJs); err == nil {
				if indented, err := json.MarshalIndent(finalJs, "", "  "); err == nil {
					result = string(indented) + "\n"
					fmt.Printf("✅ Repaired and re-formatted fallback result as valid JSON\n")
				}
			} else {
				fmt.Printf("⚠️ Failed to repair JSON: %v\n", err)
			}
		}

	} else {

		// Not JSON, just append

		if !strings.HasSuffix(result, "\n") {

			result += "\n"

		}

		for _, addition := range additions {

			result += addition + "\n"

		}

		fmt.Printf("🔧 Added %d lines via fallback approach\n", len(additions))

	}

	return result, nil

}

// ApplyDiffPatchDirect is an exported function for testing that applies a diff patch directly
// without going through the HTTP API. This allows tests to use the same diff patching logic.
func ApplyDiffPatchDirect(currentContent, diffContent string) (string, error) {
	return applyDiffPatchFlexible(currentContent, diffContent)
}
