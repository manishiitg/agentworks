package llmtypes

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// cliBinaryVersionPattern matches the first dotted numeric version in CLI
// output. The dot requirement avoids matching unrelated lone numbers.
var cliBinaryVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// ExtractCLIBinaryVersion returns the first dotted version in raw CLI
// output ("Muse Code 1.3.0 (...)" yields "1.3.0"). Rebuild suffixes are
// dropped so rebuilds of the same release compare equal.
func ExtractCLIBinaryVersion(output string) (string, error) {
	if v := cliBinaryVersionPattern.FindString(output); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no version found in CLI output %q", output)
}

// ProbeCLIBinaryVersion resolves binary on PATH, runs it with args, and
// returns the extracted version. Adapter live tests call this directly —
// adapter packages cannot import the SDK root for the contract-bound
// wrapper, so the dependency-neutral probe lives here.
func ProbeCLIBinaryVersion(ctx context.Context, binary string, args ...string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s version probe failed: %w (output: %s)",
			binary, err, strings.TrimSpace(string(out)))
	}
	version, err := ExtractCLIBinaryVersion(string(out))
	if err != nil {
		return "", fmt.Errorf("%s: %w", binary, err)
	}
	return version, nil
}
