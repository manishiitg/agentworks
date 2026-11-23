package llmproviders

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// cliVersionComponents splits s on non-digit runs and parses the numeric
// components. It tolerates semver ("1.3.0"), prefixed output ("codex-cli
// 0.155.1"), and calver ("2026.09.18-9a7762b") uniformly.
func cliVersionComponents(s string) []int {
	var out []int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// CompareCodingAgentCLIVersions compares two CLI version strings by numeric
// components, returning -1, 0, or 1. Missing trailing components count as
// zero, so "1.3" equals "1.3.0". Non-numeric decoration is ignored.
func CompareCodingAgentCLIVersions(a, b string) int {
	ca, cb := cliVersionComponents(a), cliVersionComponents(b)
	for i := 0; i < len(ca) || i < len(cb); i++ {
		var va, vb int
		if i < len(ca) {
			va = ca[i]
		}
		if i < len(cb) {
			vb = cb[i]
		}
		if va != vb {
			if va < vb {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ExtractCodingAgentCLIVersion returns the first dotted version in raw CLI
// output ("Muse Code 1.3.0 (...)" yields "1.3.0"). Hash/date suffixes are
// dropped so rebuilds of the same release compare equal.
func ExtractCodingAgentCLIVersion(output string) (string, error) {
	return llmtypes.ExtractCLIBinaryVersion(output)
}

// CodingAgentCLIVersion probes the installed CLI version for provider by
// running its contract RuntimeBinary with VersionProbeArgs. A missing
// binary fails with the contract InstallCommand so the caller can tell the
// user exactly how to fix it.
func CodingAgentCLIVersion(ctx context.Context, provider Provider) (string, error) {
	contract, ok := GetCodingAgentProviderContract(provider, "")
	if !ok {
		return "", fmt.Errorf("unknown coding agent provider %s", provider)
	}
	if strings.TrimSpace(contract.RuntimeBinary) == "" {
		return "", fmt.Errorf("coding agent provider %s declares no runtime binary", provider)
	}
	version, err := llmtypes.ProbeCLIBinaryVersion(ctx, contract.RuntimeBinary, contract.VersionProbeArgs...)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%s CLI %q not found on PATH; install it with: %s",
				provider, contract.RuntimeBinary, contract.InstallCommand)
		}
		return "", fmt.Errorf("%s: %w", provider, err)
	}
	return version, nil
}

// CheckCodingAgentCLIVersion reports whether the installed CLI meets the
// contract MinCLIVersion floor. Below-floor versions fail with an explicit
// update message; hosts surface this on the providers page instead of
// running an uncertified CLI into undefined adapter behavior.
func CheckCodingAgentCLIVersion(ctx context.Context, provider Provider) error {
	contract, ok := GetCodingAgentProviderContract(provider, "")
	if !ok {
		return fmt.Errorf("unknown coding agent provider %s", provider)
	}
	installed, err := CodingAgentCLIVersion(ctx, provider)
	if err != nil {
		return err
	}
	if CompareCodingAgentCLIVersions(installed, contract.MinCLIVersion) < 0 {
		return fmt.Errorf("%s CLI version %s is below the certified floor %s; update with: %s",
			provider, installed, contract.MinCLIVersion, contract.InstallCommand)
	}
	return nil
}
