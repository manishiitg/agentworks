package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

func main() {
	priorityFlag := flag.String("priority", "P0", "certification priority: P0, P1, or all")
	providerFlag := flag.String("provider", "", "coding CLI provider ID")
	packageFlag := flag.String("package", "", "repository-relative Go package containing the P0 tests")
	listProviders := flag.Bool("list-providers", false, "print the registry-derived release matrix as 'provider package' lines")
	cliVersions := flag.Bool("cli-versions", false, "print installed CLI versions as 'provider version' lines (MISSING when undetectable)")
	checkVersions := flag.String("check-cli-versions", "", "fail unless installed CLIs match the certified versions file")
	writeVersions := flag.String("write-cli-versions", "", "record installed CLI versions into the certified versions file")
	providersFlag := flag.String("providers", "", "comma-separated provider IDs to check (default: release matrix)")
	flag.Parse()

	if *cliVersions {
		matrix, err := llmproviders.CodingAgentP0ReleaseMatrix()
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, entry := range matrix {
			version, err := llmproviders.CodingAgentCLIVersion(ctx, entry.Provider)
			if err != nil {
				fmt.Printf("%s MISSING\n", entry.Provider)
				continue
			}
			fmt.Printf("%s %s\n", entry.Provider, version)
		}
		return
	}

	selectedProviders := func(installed map[string]string) []string {
		if trimmed := strings.TrimSpace(*providersFlag); trimmed != "" {
			return SplitProvidersFlag(trimmed)
		}
		providers := make([]string, 0, len(installed))
		for provider := range installed {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		return providers
	}

	if *writeVersions != "" {
		installed, err := ProbeInstalledCLIVersions(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		if err := WriteCertifiedCLIVersions(*writeVersions, installed, selectedProviders(installed)); err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *checkVersions != "" {
		certified, err := LoadCertifiedCLIVersions(*checkVersions)
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		installed, err := ProbeInstalledCLIVersions(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		if mismatches := CheckInstalledCLIVersions(installed, certified, selectedProviders(installed)); len(mismatches) > 0 {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: CLI versions drifted from the certified claim:\n")
			for _, mismatch := range mismatches {
				fmt.Fprintf(os.Stderr, "  %s\n", mismatch)
			}
			fmt.Fprintf(os.Stderr, "re-run P0 with --update-certified-versions to certify the new versions\n")
			os.Exit(1)
		}
		return
	}

	if *listProviders {
		matrix, err := llmproviders.CodingAgentP0ReleaseMatrix()
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-p0-tests: %v\n", err)
			os.Exit(1)
		}
		for _, entry := range matrix {
			fmt.Printf("%s %s\n", entry.Provider, entry.Package)
		}
		return
	}

	provider := llmproviders.Provider(strings.TrimSpace(*providerFlag))
	expectedPackage := filepath.ToSlash(strings.Trim(strings.TrimSpace(*packageFlag), "/"))
	if provider == "" || expectedPackage == "" {
		fmt.Fprintln(os.Stderr, "-provider and -package are required")
		os.Exit(2)
	}
	contract, ok := llmproviders.GetCodingAgentProviderContract(provider, "")
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown coding CLI provider %q\n", provider)
		os.Exit(2)
	}

	priority := strings.ToUpper(strings.TrimSpace(*priorityFlag))
	if priority != "P0" && priority != "P1" && priority != "ALL" {
		fmt.Fprintln(os.Stderr, "-priority must be P0, P1, or all")
		os.Exit(2)
	}
	requiredIDs := llmproviders.RequiredP0CodingAgentCertificationIDs(contract)
	required := make(map[llmproviders.CodingAgentCertificationID]struct{}, len(requiredIDs))
	for _, id := range requiredIDs {
		required[id] = struct{}{}
	}
	found := make(map[llmproviders.CodingAgentCertificationID]struct{}, len(requiredIDs))
	testNames := make(map[string]struct{}, len(requiredIDs))
	for _, certification := range llmproviders.CodingAgentProviderCertifications(provider) {
		_, needed := required[certification.ID]
		if needed {
			found[certification.ID] = struct{}{}
		}
		if needed && !certification.RealE2E {
			fmt.Fprintf(os.Stderr, "%s P0 certification %s is not a real E2E\n", provider, certification.ID)
			os.Exit(1)
		}
		if (priority == "P0" && !needed) || (priority == "P1" && certification.Priority != llmproviders.CodingAgentCertificationPriorityP1) {
			continue
		}
		testPackage := filepath.ToSlash(filepath.Dir(certification.TestFile))
		if testPackage != expectedPackage {
			fmt.Fprintf(os.Stderr, "%s certification test %s is in %s, runner expected %s\n", provider, certification.TestName, testPackage, expectedPackage)
			os.Exit(1)
		}
		found[certification.ID] = struct{}{}
		testNames[certification.TestName] = struct{}{}
	}
	for _, id := range requiredIDs {
		if _, ok := found[id]; !ok {
			fmt.Fprintf(os.Stderr, "%s is missing required P0 test %s\n", provider, id)
			os.Exit(1)
		}
	}

	names := make([]string, 0, len(testNames))
	for name := range testNames {
		names = append(names, regexp.QuoteMeta(name))
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "%s has no registered tests for the selected priority\n", provider)
		os.Exit(1)
	}
	fmt.Printf("^(%s)$\n", strings.Join(names, "|"))
}
