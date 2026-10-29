package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestProviderRuntimeDerivesFromSDKContracts(t *testing.T) {
	for _, contract := range llm.CodingAgentProviderContracts() {
		if contract.Transport != llm.CodingAgentTransportTmux {
			continue
		}
		if got := providerRuntime(string(contract.Provider)); got != contract.RuntimeBinary {
			t.Errorf("providerRuntime(%s) = %q, want SDK RuntimeBinary %q",
				contract.Provider, got, contract.RuntimeBinary)
		}
		if providerInstallCommand(string(contract.Provider)) != contract.InstallCommand {
			t.Errorf("providerInstallCommand(%s) diverges from the SDK contract", contract.Provider)
		}
	}
	if got := providerRuntime("openai"); got != "" {
		t.Errorf("providerRuntime(openai) = %q, want empty for API providers", got)
	}
}

func TestCliUpdateStatusMapsFloor(t *testing.T) {
	if got := cliUpdateStatus("2.1.278", "2.1.278"); got != "supported" {
		t.Errorf("equal versions = %q, want supported", got)
	}
	if got := cliUpdateStatus("2.1.300", "2.1.278"); got != "supported" {
		t.Errorf("newer version = %q, want supported", got)
	}
	if got := cliUpdateStatus("2.1.0", "2.1.278"); got != "unsupported" {
		t.Errorf("older version = %q, want unsupported", got)
	}
	if got := cliUpdateStatus("", "2.1.278"); got != "unknown" {
		t.Errorf("empty version = %q, want unknown", got)
	}
}

func TestCertifiedCLIVersionsMeetFloors(t *testing.T) {
	raw, err := os.ReadFile("../../../scripts/p0-certified-cli-versions.json")
	if err != nil {
		t.Fatalf("read certified versions: %v", err)
	}
	var claimed struct {
		Versions map[string]string `json:"versions"`
	}
	if err := json.Unmarshal(raw, &claimed); err != nil {
		t.Fatalf("parse certified versions: %v", err)
	}
	for _, contract := range llm.CodingAgentProviderContracts() {
		if contract.Transport != llm.CodingAgentTransportTmux {
			continue
		}
		certified, ok := claimed.Versions[string(contract.Provider)]
		if !ok || strings.TrimSpace(certified) == "" {
			t.Errorf("%s has no certified version recorded", contract.Provider)
			continue
		}
		if llm.CompareCodingAgentCLIVersions(certified, contract.MinCLIVersion) < 0 {
			t.Errorf("%s certified %s is below the SDK floor %s",
				contract.Provider, certified, contract.MinCLIVersion)
		}
	}
	for provider := range claimed.Versions {
		if _, ok := llm.GetCodingAgentProviderContract(llm.Provider(provider), ""); !ok {
			t.Errorf("certified versions names unknown provider %s", provider)
		}
	}
}

func TestParsePiCLIModelList(t *testing.T) {
	output := `provider       model                          context  max-out  thinking  images
google         gemini-3.8-flash               1.0M     65.5K    yes       yes
google         gemini-3.5-flash               1.0M     65.5K    yes       yes
google         gemma-4-26b-a4b-it             262.1K   32.8K    yes       yes
google-vertex  gemini-3.5-flash               1.0M     65.5K    yes       yes
zai            glm-5.3                        1.0M     65.5K    yes       yes
minimax        MiniMax-M3                     512K     131K     yes       yes
kimi-coding    k3                             1.0M     131K     yes       yes
`

	models := parsePiCLIModelList(output)
	if len(models) != 7 {
		t.Fatalf("models len = %d, want 7: %#v", len(models), models)
	}
	if models[0].ModelID != "google/gemini-3.8-flash" {
		t.Fatalf("first model id = %q", models[0].ModelID)
	}
	if !models[0].IsDefault {
		t.Fatal("google/gemini-3.8-flash should be marked default")
	}
	if models[0].ContextWindow != 1_000_000 {
		t.Fatalf("context = %d, want 1000000", models[0].ContextWindow)
	}
	if models[1].IsDefault {
		t.Fatal("google/gemini-3.5-flash should not be marked default")
	}
	if models[2].ContextWindow != 262_100 {
		t.Fatalf("context = %d, want 262100", models[2].ContextWindow)
	}
	if models[3].Group != "Google Vertex" {
		t.Fatalf("group = %q, want Google Vertex", models[3].Group)
	}
	if models[4].ModelID != "zai/glm-5.3" || models[4].Group != "Z.AI" {
		t.Fatalf("zai model = %#v, want zai/glm-5.3 in Z.AI group", models[4])
	}
	if models[5].ModelID != "minimax/MiniMax-M3" || models[5].Group != "MiniMax" {
		t.Fatalf("minimax model = %#v, want minimax/MiniMax-M3 in MiniMax group", models[5])
	}
	if models[6].ModelID != "kimi-coding/k3" || models[6].Group != "Kimi" {
		t.Fatalf("kimi model = %#v, want kimi-coding/k3 in Kimi group", models[6])
	}
}

// PLAT (OpenRouter free models): confirmed live that fetchPiCLIModels(true)
// was silently dropping IsFree=true for most OpenRouter models -- pi's own
// --list-models output can legitimately include the same OpenRouter model
// IDs with no pricing info at all, and merging it ahead of the live
// OpenRouter fetch let that pricing-blind duplicate win the
// mergePiModelEntries dedup (which keeps the first occurrence per ModelID).
// Drives fetchPiCLIModels(true) itself (not just mergePiModelEntries in
// isolation) via the two overridable fetch points, with a canned "pi's own
// listing" result that duplicates a live free model's ID without pricing --
// exactly the live-observed failure mode.
// A deployment that supplies its own Pi models offers only those: Pi's built-in catalog has no key there, and a
// project that saved one of its models failed every turn with "No API key found".
func TestFetchPiCLIModelsOffersOnlyTheDeploymentModelsWhenItSuppliesItsOwn(t *testing.T) {
	dir := t.TempDir()
	models := `{"providers":{"gateway":{"baseUrl":"https://gateway.example.com/v1","api":"openai-completions","apiKey":"$GATEWAY_API_KEY","models":[{"id":"model-a","name":"Model A"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(picli.EnvPiAgentTemplateDir, dir)
	resp := fetchPiCLIModels(false)
	if len(resp.Models) != 1 || resp.Models[0].ModelID != "gateway/model-a" || !resp.Models[0].IsDefault {
		t.Fatalf("models = %#v, want only the deployment's model as the default", resp.Models)
	}
	if resp.SupportsCustom {
		t.Fatal("a deployment's own models are the only ones with a key: custom model IDs must not be offered")
	}
}

func TestFetchPiCLIModelsFullCatalogKeepsLiveOpenRouterPricingOverACLIDuplicate(t *testing.T) {
	origORURL := openRouterModelsAPIURL
	origListFn := listPiCLIModelsFn
	defer func() {
		openRouterModelsAPIURL = origORURL
		listPiCLIModelsFn = origListFn
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"dots-studio/dots-3-note-preview","name":"Dots 3 Note Preview","context_length":32768,"pricing":{"prompt":"0","completion":"0"}}
		]}`))
	}))
	defer server.Close()
	openRouterModelsAPIURL = server.URL

	// pi's own --list-models output never carries pricing/IsFree.
	listPiCLIModelsFn = func() ([]dynamicModelEntry, error) {
		return []dynamicModelEntry{
			{ModelID: "openrouter/dots-studio/dots-3-note-preview", ModelName: "Dots 3 Note Preview", Group: "OpenRouter"},
		}, nil
	}

	resp := fetchPiCLIModels(true)
	var found *dynamicModelEntry
	for i := range resp.Models {
		if resp.Models[i].ModelID == "openrouter/dots-studio/dots-3-note-preview" {
			found = &resp.Models[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("full catalog missing the duplicated free model entirely: %#v", resp.Models)
	}
	if !found.IsFree {
		t.Fatalf("merged entry = %#v, want IsFree true (must not be shadowed by pi's pricing-blind duplicate)", *found)
	}
}

func TestParsePiCLIModelListNoMatches(t *testing.T) {
	if got := parsePiCLIModelList(`No models matching "sonnet"`); len(got) != 0 {
		t.Fatalf("models len = %d, want 0: %#v", len(got), got)
	}
}

func TestFetchAvailablePiCLIModelsReturnsOnlyLiveConnectedInventory(t *testing.T) {
	original := listPiCLIModelsFn
	listPiCLIModelsFn = func() ([]dynamicModelEntry, error) {
		return []dynamicModelEntry{
			{ModelID: "google/gemini-3.8-flash", ModelName: "Gemini 3.8 Flash", Group: "Gemini"},
			{ModelID: "openrouter/openrouter/free", ModelName: "OpenRouter Free", Group: "OpenRouter"},
		}, nil
	}
	invalidatePiProviderCaches()
	t.Cleanup(func() {
		listPiCLIModelsFn = original
		invalidatePiProviderCaches()
	})

	resp := fetchAvailablePiCLIModels()
	if resp.Source != "cli_available" || resp.Error != "" {
		t.Fatalf("available response = %#v", resp)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("available models = %#v, want exactly the two live models", resp.Models)
	}
	if len(resp.Groups) != 2 || resp.Groups[0] != "Gemini" || resp.Groups[1] != "OpenRouter" {
		t.Fatalf("available groups = %#v", resp.Groups)
	}
}

func TestMergePiModelEntriesKeepsCuratedModelsFirst(t *testing.T) {
	curated := piFallbackModels()
	listed := []dynamicModelEntry{
		{ModelID: "google/gemini-3.8-flash", Group: "Google"},
		{ModelID: "anthropic/claude-sonnet-4-6", Group: "Anthropic"},
	}
	merged := mergePiModelEntries(curated, listed)
	if len(merged) != len(curated)+1 {
		t.Fatalf("merged len = %d, want %d", len(merged), len(curated)+1)
	}
	if merged[0].ModelID != "google/gemini-3.8-flash" || !merged[0].IsDefault {
		t.Fatalf("first merged model = %#v, want curated default first", merged[0])
	}
	if merged[len(merged)-1].ModelID != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("last merged model = %#v, want listed non-curated model", merged[len(merged)-1])
	}
}

func TestFetchOpenRouterCatalogParsesPricingIntoIsFree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"stealth/ox-alpha","name":"Ox Alpha","context_length":1048576,"pricing":{"prompt":"0","completion":"0"}},
			{"id":"meta/muse-spark","name":"Muse Spark","context_length":1048576,"pricing":{"prompt":"0.0000001","completion":"0.0000002"}}
		]}`))
	}))
	defer server.Close()

	original := openRouterModelsAPIURL
	openRouterModelsAPIURL = server.URL
	defer func() { openRouterModelsAPIURL = original }()

	models, err := fetchOpenRouterCatalog()
	if err != nil {
		t.Fatalf("fetchOpenRouterCatalog() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2: %#v", len(models), models)
	}
	if models[0].ModelID != "openrouter/stealth/ox-alpha" || !models[0].IsFree {
		t.Fatalf("free model = %#v, want openrouter/stealth/ox-alpha with IsFree true", models[0])
	}
	if models[0].Group != "OpenRouter" {
		t.Fatalf("free model group = %q, want OpenRouter", models[0].Group)
	}
	if models[1].ModelID != "openrouter/meta/muse-spark" || models[1].IsFree {
		t.Fatalf("paid model = %#v, want openrouter/meta/muse-spark with IsFree false", models[1])
	}
	if models[1].CostInput <= 0 || models[1].CostOutput <= 0 {
		t.Fatalf("paid model costs = input:%v output:%v, want both > 0 (per-1M-token conversion)", models[1].CostInput, models[1].CostOutput)
	}
}

func TestFetchOpenRouterCatalogReturnsErrorOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	original := openRouterModelsAPIURL
	openRouterModelsAPIURL = server.URL
	defer func() { openRouterModelsAPIURL = original }()

	if _, err := fetchOpenRouterCatalog(); err == nil {
		t.Fatal("fetchOpenRouterCatalog() error = nil, want an error for a non-200 response")
	}
}

func TestDynamicModelGroupsPreservesFirstSeenOrder(t *testing.T) {
	groups := dynamicModelGroups([]dynamicModelEntry{
		{Group: "Google"},
		{Group: "OpenAI"},
		{Group: "Google"},
		{},
	})
	want := []string{"Google", "OpenAI", "Other"}
	if len(groups) != len(want) {
		t.Fatalf("groups = %#v, want %#v", groups, want)
	}
	for i := range want {
		if groups[i] != want[i] {
			t.Fatalf("groups = %#v, want %#v", groups, want)
		}
	}
}

func TestFallbackProviderModelIDsOffersLocalCLIModels(t *testing.T) {
	ids := fallbackProviderModelIDs("claude-code")
	if len(ids) == 0 {
		t.Fatal("claude-code fallback models are empty")
	}
	found := false
	for _, id := range ids {
		if id == "claude-sonnet-5-5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("claude-code fallback models = %v, want claude-sonnet-5-5 among them", ids)
	}
	if got := fallbackProviderModelIDs("no-such-provider"); len(got) != 0 {
		t.Fatalf("unknown provider fallback = %v, want empty", got)
	}
}
