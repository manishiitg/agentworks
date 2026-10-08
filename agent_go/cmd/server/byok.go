package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

// "Bring your own model key" (PLAT-717): a person adds a key for a model
// service (OpenRouter, NVIDIA NIM, Groq, Google AI Studio or any
// OpenAI-compatible endpoint) as a private Pi account. Turns still run only
// through the Pi CLI; this file only helps set the account up: it checks the
// key, lists the service's models (free, price, tool support) and runs a
// one-request tool-call check on a model. None of these is an agent turn.
//
// The account is an ordinary pi-cli provider connection: UnderlyingProvider
// is the Pi provider id (openrouter, nvidia, groq, google or
// openai-compatible), the key is its credential, and its allowed models are
// the person's picks, so a chat on it never falls back to a model of another
// service (resolveAccountModel runs it on the first pick instead).

// byokCustomProvider is the Pi provider id of a person's own
// OpenAI-compatible endpoint; its base URL is stored on the account.
const byokCustomProvider = "openai-compatible"

type byokService struct {
	ID    string
	Label string
	// ChatURL is the OpenAI-compatible chat completions endpoint.
	ChatURL string
	// ModelsURL lists models; Public means it needs no key.
	ModelsURL string
	Public    bool
	// Recommended are model ids (without the service prefix) known to work
	// for agents (tool calls), in order of preference.
	Recommended []string
}

var byokServices = map[string]byokService{
	"openrouter": {ID: "openrouter", Label: "OpenRouter", ChatURL: "https://openrouter.ai/api/v1/chat/completions", ModelsURL: "https://openrouter.ai/api/v1/models", Public: true,
		Recommended: []string{"nvidia/nemotron-3-super-120b-a12b:free", "nvidia/nemotron-3-ultra-550b-a55b:free", "thinkingmachines/inkling:free", "google/gemma-4-31b-it:free"}},
	"nvidia": {ID: "nvidia", Label: "NVIDIA NIM", ChatURL: "https://integrate.api.nvidia.com/v1/chat/completions", ModelsURL: "https://integrate.api.nvidia.com/v1/models", Public: true,
		Recommended: []string{"z-ai/glm-5.3-flash", "nvidia/nemotron-3.5-lightning-30b-a3b", "nvidia/nemotron-3-super-120b-a12b", "nvidia/nemotron-3-ultra-550b-a55b", "moonshotai/kimi-k3"}},
	"groq": {ID: "groq", Label: "Groq", ChatURL: "https://api.groq.com/openai/v1/chat/completions", ModelsURL: "https://api.groq.com/openai/v1/models",
		Recommended: []string{"openai/gpt-oss-120b", "moonshotai/kimi-k2-instruct-0905", "qwen/qwen3-32b", "llama-3.3-70b-versatile"}},
	"google": {ID: "google", Label: "Google AI Studio", ChatURL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", ModelsURL: "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000",
		Recommended: []string{"gemini-3.8-flash", "gemini-3.5-flash", "gemini-2.5-flash"}},
	byokCustomProvider: {ID: byokCustomProvider, Label: "OpenAI-compatible"},
}

// byokServiceFor is the service of a Pi account, if it is a BYOK one.
func byokServiceFor(record *storedProviderConnection) (byokService, bool) {
	if record == nil || record.Provider != "pi-cli" {
		return byokService{}, false
	}
	service, ok := byokServices[record.UnderlyingProvider]
	return service, ok
}

// byokEndpoint fills in a custom endpoint's URLs from its base URL.
func byokEndpoint(service byokService, baseURL string) (byokService, error) {
	if service.ID != byokCustomProvider {
		return service, nil
	}
	base, err := validateByokBaseURL(baseURL)
	if err != nil {
		return service, err
	}
	service.ChatURL, service.ModelsURL = base+"/chat/completions", base+"/models"
	return service, nil
}

// validateByokBaseURL accepts an https URL on a public host (a person's
// endpoint must not reach the server's own network). A deployment that
// serves models on its private network may allow those with
// AGENTWORKS_BYOK_ALLOW_PRIVATE_ENDPOINTS=1.
func validateByokBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("enter the endpoint's base URL, like https://api.example.com/v1")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && byokPrivateEndpointsAllowed()) {
		return "", fmt.Errorf("the base URL must start with https://")
	}
	if !byokPrivateEndpointsAllowed() {
		host := parsed.Hostname()
		if ip := net.ParseIP(host); (ip != nil && byokPrivateIP(ip)) || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") || strings.HasSuffix(strings.ToLower(host), ".internal") {
			return "", fmt.Errorf("the base URL must be a public address")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func byokPrivateEndpointsAllowed() bool {
	return os.Getenv("AGENTWORKS_BYOK_ALLOW_PRIVATE_ENDPOINTS") == "1"
}

func byokPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// byokHTTPClient refuses to connect to private addresses (also after DNS),
// so a custom endpoint cannot probe the server's network.
var byokHTTPClient = &http.Client{
	Timeout: 45 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
			if byokPrivateEndpointsAllowed() {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && byokPrivateIP(ip) {
				return fmt.Errorf("refusing to connect to a private address")
			}
			return nil
		}}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func byokRequest(ctx context.Context, service byokService, method, target, key string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		if service.ID == "google" && method == http.MethodGet {
			req.Header.Set("x-goog-api-key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	if service.ID == "openrouter" {
		req.Header.Set("X-Title", "AgentWorks")
	}
	resp, err := byokHTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, raw, nil
}

// byokCheck is the plain-words result of a key or model check.
type byokCheck struct {
	// State is ok, rejected, rate_limited or error.
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
	Identity string `json:"identity,omitempty"`
}

// byokErrorText is the provider's own error message, without anything that
// looks like a key.
func byokErrorText(raw []byte) string {
	var parsed struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	text := ""
	if json.Unmarshal(raw, &parsed) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var plain string
		switch {
		case json.Unmarshal(parsed.Error, &nested) == nil && nested.Message != "":
			text = nested.Message
		case json.Unmarshal(parsed.Error, &plain) == nil && plain != "":
			text = plain
		default:
			text = firstNonEmptyTrimmed(parsed.Message, parsed.Detail)
		}
	}
	if text == "" {
		text = string(raw)
	}
	return safeProviderText(text)
}

func byokStatusCheck(status int, raw []byte, err error) byokCheck {
	switch {
	case err != nil:
		return byokCheck{State: "error", Detail: "Could not reach the service: " + safeProviderText(err.Error())}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if text := byokErrorText(raw); text != "" {
			return byokCheck{State: "rejected", Detail: "The service rejected the key: " + text}
		}
		return byokCheck{State: "rejected", Detail: "The service rejected the key."}
	case status == http.StatusTooManyRequests:
		return byokCheck{State: "rate_limited", Detail: "The key works, but the service is rate-limiting it right now. Try again in a minute."}
	case status == http.StatusPaymentRequired:
		return byokCheck{State: "error", Detail: "The key works, but the account is out of credit: " + byokErrorText(raw)}
	case status >= 200 && status < 300:
		return byokCheck{State: "ok"}
	default:
		return byokCheck{State: "error", Detail: fmt.Sprintf("The service answered %d: %s", status, byokErrorText(raw))}
	}
}

// checkByokKey makes one cheap request that needs the key.
func checkByokKey(ctx context.Context, service byokService, key string) byokCheck {
	if strings.TrimSpace(key) == "" {
		return byokCheck{State: "rejected", Detail: "Paste the key first."}
	}
	switch service.ID {
	case "openrouter":
		status, raw, err := byokRequest(ctx, service, http.MethodGet, "https://openrouter.ai/api/v1/key", key, nil)
		check := byokStatusCheck(status, raw, err)
		if check.State == "ok" {
			var parsed struct {
				Data struct {
					Usage      float64  `json:"usage"`
					Limit      *float64 `json:"limit"`
					IsFreeTier bool     `json:"is_free_tier"`
				} `json:"data"`
			}
			_ = json.Unmarshal(raw, &parsed)
			parts := []string{"OpenRouter key"}
			if parsed.Data.IsFreeTier {
				parts = append(parts, "free tier")
			}
			if parsed.Data.Usage > 0 {
				parts = append(parts, fmt.Sprintf("$%.2f used", parsed.Data.Usage))
			}
			if parsed.Data.Limit != nil {
				parts = append(parts, fmt.Sprintf("$%.2f limit", *parsed.Data.Limit))
			}
			check.Identity = strings.Join(parts, " · ")
		}
		return check
	case "nvidia":
		// NVIDIA lists models without a key: one 1-token request checks it.
		model := service.Recommended[1]
		status, raw, err := byokRequest(ctx, service, http.MethodPost, service.ChatURL, key, map[string]any{
			"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if status == http.StatusNotFound || status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
			status = http.StatusOK // the key was accepted; the model answered with a request error
		}
		check := byokStatusCheck(status, raw, err)
		if check.State == "ok" {
			check.Identity = "NVIDIA NIM key"
		}
		return check
	default:
		target := service.ModelsURL
		if service.ID == "google" {
			target = "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1"
		}
		status, raw, err := byokRequest(ctx, service, http.MethodGet, target, key, nil)
		if service.ID == byokCustomProvider && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
			return byokCheck{State: "ok", Identity: "OpenAI-compatible key", Detail: "The endpoint has no model list, so the key could not be checked. Add model ids by hand and use Try it."}
		}
		if service.ID == "google" && status == http.StatusBadRequest && strings.Contains(string(raw), "API_KEY_INVALID") {
			status = http.StatusUnauthorized
		}
		check := byokStatusCheck(status, raw, err)
		if check.State == "ok" {
			check.Identity = service.Label + " key"
		}
		return check
	}
}

// byokModel is one model of a service as the setup screens and the chat
// picker show it. ModelID is the Pi id ("<service>/<model>").
type byokModel struct {
	ModelID       string  `json:"model_id"`
	ModelName     string  `json:"model_name"`
	IsFree        bool    `json:"is_free,omitempty"`
	SupportsTools *bool   `json:"supports_tools,omitempty"`
	ContextWindow int     `json:"context_window,omitempty"`
	CostInput     float64 `json:"cost_input,omitempty"`
	CostOutput    float64 `json:"cost_output,omitempty"`
	Recommended   bool    `json:"recommended,omitempty"`
}

var byokCatalogCache = struct {
	sync.Mutex
	at     map[string]time.Time
	models map[string][]byokModel
}{at: map[string]time.Time{}, models: map[string][]byokModel{}}

// nvidiaNonChatMarkers are NVIDIA catalog entries that are not chat models.
var nvidiaNonChatMarkers = []string{"embed", "rerank", "safety", "guard", "reward", "retriever", "parse", "clip", "whisper", "tts", "asr", "ocr", "detector", "yolox", "nemoretriever", "deplot", "paddle", "vila", "kosmos", "fuyu", "neva", "cosmos-predict", "bge", "e5-", "arctic-embed", "nv-embed", "translate"}

// listByokModels lists a service's models. Public catalogs are cached for
// ten minutes and need no key.
func listByokModels(ctx context.Context, service byokService, key string) ([]byokModel, error) {
	cacheKey := service.ID
	if service.Public {
		byokCatalogCache.Lock()
		cached, at := byokCatalogCache.models[cacheKey], byokCatalogCache.at[cacheKey]
		byokCatalogCache.Unlock()
		if len(cached) > 0 && time.Since(at) < 10*time.Minute {
			return cached, nil
		}
		key = ""
	} else if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%s lists its models only with a key", service.Label)
	}
	var models []byokModel
	switch service.ID {
	case "openrouter":
		entries, err := fetchOpenRouterCatalog()
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			tools := entry.SupportsTools
			models = append(models, byokModel{ModelID: entry.ModelID, ModelName: entry.ModelName, IsFree: entry.IsFree, SupportsTools: &tools, ContextWindow: entry.ContextWindow, CostInput: entry.CostInput, CostOutput: entry.CostOutput})
		}
	case "google":
		status, raw, err := byokRequest(ctx, service, http.MethodGet, service.ModelsURL, key, nil)
		if check := byokStatusCheck(status, raw, err); check.State != "ok" {
			return nil, fmt.Errorf("%s", check.Detail)
		}
		var parsed struct {
			Models []struct {
				Name             string   `json:"name"`
				DisplayName      string   `json:"displayName"`
				InputTokenLimit  int      `json:"inputTokenLimit"`
				GenerationMethod []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("unexpected model list from %s", service.Label)
		}
		for _, model := range parsed.Models {
			id := strings.TrimPrefix(model.Name, "models/")
			if !containsFold(model.GenerationMethod, "generateContent") || !strings.HasPrefix(id, "gemini") || strings.Contains(id, "embedding") || strings.Contains(id, "tts") || strings.Contains(id, "image") || strings.Contains(id, "live") {
				continue
			}
			models = append(models, byokModel{ModelID: "google/" + id, ModelName: firstNonEmptyTrimmed(model.DisplayName, id), ContextWindow: model.InputTokenLimit})
		}
	default:
		status, raw, err := byokRequest(ctx, service, http.MethodGet, service.ModelsURL, key, nil)
		if check := byokStatusCheck(status, raw, err); check.State != "ok" {
			return nil, fmt.Errorf("%s", check.Detail)
		}
		var parsed struct {
			Data []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"context_window"`
				Active        *bool  `json:"active"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("unexpected model list from %s", service.Label)
		}
		for _, model := range parsed.Data {
			id := strings.TrimSpace(model.ID)
			if id == "" || (model.Active != nil && !*model.Active) {
				continue
			}
			if service.ID == "nvidia" && byokHasAny(strings.ToLower(id), nvidiaNonChatMarkers) {
				continue
			}
			if service.ID == "groq" && byokHasAny(strings.ToLower(id), []string{"whisper", "tts", "guard", "playai", "distil"}) {
				continue
			}
			// NVIDIA's hosted models are free for development (rate-limited).
			models = append(models, byokModel{ModelID: service.ID + "/" + id, ModelName: id, ContextWindow: model.ContextWindow, IsFree: service.ID == "nvidia"})
		}
	}
	markRecommendedByokModels(service, models)
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Recommended != models[j].Recommended {
			return models[i].Recommended
		}
		if models[i].IsFree != models[j].IsFree {
			return models[i].IsFree
		}
		return false
	})
	if service.Public {
		byokCatalogCache.Lock()
		byokCatalogCache.models[cacheKey], byokCatalogCache.at[cacheKey] = models, time.Now()
		byokCatalogCache.Unlock()
	}
	return models, nil
}

func byokHasAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// markRecommendedByokModels marks the models that suit agents: on OpenRouter
// every free model that supports tool calls, elsewhere the curated list.
func markRecommendedByokModels(service byokService, models []byokModel) {
	for i := range models {
		id := strings.TrimPrefix(models[i].ModelID, service.ID+"/")
		if service.ID == "openrouter" {
			models[i].Recommended = models[i].IsFree && models[i].SupportsTools != nil && *models[i].SupportsTools && id != "openrouter/free"
		} else {
			models[i].Recommended = containsFold(service.Recommended, id)
		}
	}
}

// defaultByokModel is the model a new account starts with: the first
// curated pick the catalog has, else its first recommended, else its first.
func defaultByokModel(service byokService, models []byokModel) string {
	for _, id := range service.Recommended {
		for _, model := range models {
			if model.ModelID == service.ID+"/"+id && (service.ID != "openrouter" || model.Recommended) {
				return model.ModelID
			}
		}
	}
	for _, model := range models {
		if model.Recommended {
			return model.ModelID
		}
	}
	if len(models) > 0 {
		return models[0].ModelID
	}
	return ""
}

// tryByokModel sends one request with one tool and passes when the model
// calls it. The tool is never run.
func tryByokModel(ctx context.Context, service byokService, key, modelID string) byokCheck {
	model := strings.TrimPrefix(strings.TrimSpace(modelID), service.ID+"/")
	if model == "" {
		return byokCheck{State: "error", Detail: "Pick a model first."}
	}
	status, raw, err := byokRequest(ctx, service, http.MethodPost, service.ChatURL, key, map[string]any{
		"model":      model,
		"max_tokens": 400,
		"messages": []map[string]string{
			{"role": "system", "content": "You are testing tool calls. Always answer by calling a tool."},
			{"role": "user", "content": "What is the weather in Paris? Use the get_weather tool."},
		},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{
			"name": "get_weather", "description": "Current weather for a city.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]string{"type": "string"}}, "required": []string{"city"}},
		}}},
		"tool_choice": "auto",
	})
	if status == http.StatusNotFound {
		return byokCheck{State: "error", Detail: "The service does not know this model any more. Pick another."}
	}
	if status == http.StatusTooManyRequests {
		return byokCheck{State: "rate_limited", Detail: "This model is busy or rate-limited right now (common for free models). Try again in a minute or pick another."}
	}
	check := byokStatusCheck(status, raw, err)
	if check.State != "ok" {
		return check
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(raw, &parsed)
	for _, choice := range parsed.Choices {
		for _, call := range choice.Message.ToolCalls {
			if call.Function.Name == "get_weather" {
				return byokCheck{State: "ok", Detail: "The model answered with a tool call. It can work as an agent."}
			}
		}
	}
	return byokCheck{State: "error", Detail: "The model answered without calling the tool. It may not handle agent work well; pick one marked Recommended."}
}

// byokRequestBody names either an account (its stored key and URL) or a
// service, key and base URL being set up.
type byokRequestBody struct {
	ConnectionID string `json:"connection_id"`
	Service      string `json:"service"`
	Credential   string `json:"credential"`
	BaseURL      string `json:"base_url"`
	Model        string `json:"model"`
	// WorkspacePath is where a shared account is being used, for its
	// sharing rules.
	WorkspacePath string `json:"workspace_path"`
}

// resolveByokRequest returns the service and key to use. A key is only ever
// the caller's own: a stored one is used for its owner; someone the account
// is shared with gets the service (public catalogs) but never its key.
func (api *StreamingAPI) resolveByokRequest(ctx context.Context, caller string, body byokRequestBody) (service byokService, key string, record *storedProviderConnection, err error) {
	if id := strings.TrimSpace(body.ConnectionID); id != "" {
		record, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: caller, WorkspacePath: body.WorkspacePath}, "pi-cli", id)
		if err != nil || record == nil {
			return service, "", nil, fmt.Errorf("account unavailable")
		}
		var ok bool
		if service, ok = byokServiceFor(record); !ok {
			return service, "", nil, fmt.Errorf("this account is not a model key account")
		}
		if record.OwnerUserID == caller {
			key = record.Credential
		}
		service, err = byokEndpoint(service, record.BaseURL)
		return service, key, record, err
	}
	var ok bool
	if service, ok = byokServices[strings.TrimSpace(body.Service)]; !ok {
		return service, "", nil, fmt.Errorf("unknown service")
	}
	service, err = byokEndpoint(service, body.BaseURL)
	return service, strings.TrimSpace(body.Credential), nil, err
}

// POST /api/byok/{action}: test-key, models or try-model.
func (api *StreamingAPI) handleByok(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	if !providerEnabled("pi-cli") {
		http.Error(w, "Pi is not enabled on this server", http.StatusBadRequest)
		return
	}
	var body byokRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	service, key, record, err := api.resolveByokRequest(r.Context(), caller, body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	action := mux.Vars(r)["action"]
	if action != "models" && key == "" {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "only the account's owner can use its key here"})
		return
	}
	switch action {
	case "test-key":
		check := checkByokKey(ctx, service, key)
		log.Printf("[BYOK] %s tested a %s key: %s", caller, service.ID, check.State)
		if record != nil {
			rememberProviderAccountStatus(record.ID, byokAccountStatus(check))
		}
		_ = json.NewEncoder(w).Encode(check)
	case "try-model":
		check := tryByokModel(ctx, service, key, body.Model)
		log.Printf("[BYOK] %s tried %s on %s: %s", caller, body.Model, service.ID, check.State)
		_ = json.NewEncoder(w).Encode(check)
	case "models":
		models, err := listByokModels(ctx, service, key)
		if err != nil {
			if record != nil && key == "" {
				// Someone the account is shared with: its picks, without details.
				models = nil
				for _, id := range record.AllowedModels {
					models = append(models, byokModel{ModelID: id, ModelName: strings.TrimPrefix(id, service.ID+"/")})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"models": models, "default_model": firstAllowed(record.AllowedModels)})
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models, "default_model": defaultByokModel(service, models)})
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
	}
}

func firstAllowed(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// byokAccountStatus maps a key check onto the account status line.
func byokAccountStatus(check byokCheck) providerAccountStatus {
	status := providerAccountStatus{Verified: true, CheckedAt: time.Now().UTC(), Identity: check.Identity, Detail: check.Detail}
	switch check.State {
	case "ok":
		status.State = "signed_in"
	case "rejected":
		status.State = "key_rejected"
	case "rate_limited":
		status.State, status.Detail = "signed_in", "rate-limited right now"
	default:
		status.State = "unknown"
	}
	return status
}
