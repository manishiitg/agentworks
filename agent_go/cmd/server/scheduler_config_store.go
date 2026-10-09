package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const schedulerConfigFilePath = "config/scheduler.json"

// SchedulerConfig stores workspace-level scheduler settings in config/scheduler.json.
// Schedule definitions remain in each workflow manifest.
type SchedulerConfig struct {
	// GloballyPaused holds the timed runs of every product: Goals workflows, Relays, Crews and Code.
	GloballyPaused bool       `json:"globally_paused"`
	PausedAt       *time.Time `json:"paused_at,omitempty"`
	PausedBy       string     `json:"paused_by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	// PausedProducts holds the timed runs of the named products only (PLAT-782): "agentworks" (Goals workflows),
	// "relays", "work" (Crews), "code". A product is paused when it is listed here or GloballyPaused is on.
	PausedProducts []string `json:"paused_products,omitempty"`
}

// schedulerPauseProducts are the products whose timed runs a pause can hold.
var schedulerPauseProducts = []string{"agentworks", "relays", "work", "code"}

func normalizePausedProducts(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, product := range in {
		product = strings.ToLower(strings.TrimSpace(product))
		known := false
		for _, allowed := range schedulerPauseProducts {
			known = known || allowed == product
		}
		if known && !seen[product] {
			seen[product] = true
			out = append(out, product)
		}
	}
	return out
}

// ProductPaused reports whether this product's timed runs are held, by the global pause or by its own.
func (c *SchedulerConfig) ProductPaused(product string) bool {
	if c == nil {
		return false
	}
	if c.GloballyPaused {
		return true
	}
	product = strings.ToLower(strings.TrimSpace(product))
	for _, paused := range c.PausedProducts {
		if paused == product {
			return true
		}
	}
	return false
}

func sanitizeSchedulerConfig(cfg *SchedulerConfig) *SchedulerConfig {
	if cfg == nil {
		return &SchedulerConfig{}
	}

	sanitized := &SchedulerConfig{
		GloballyPaused: cfg.GloballyPaused,
		PausedBy:       strings.TrimSpace(cfg.PausedBy),
	}
	if cfg.PausedProducts != nil {
		sanitized.PausedProducts = normalizePausedProducts(cfg.PausedProducts)
	}
	if cfg.GloballyPaused && cfg.PausedAt != nil {
		pausedAt := cfg.PausedAt.UTC()
		sanitized.PausedAt = &pausedAt
	}
	if cfg.UpdatedAt != nil {
		updatedAt := cfg.UpdatedAt.UTC()
		sanitized.UpdatedAt = &updatedAt
	}
	return sanitized
}

func SaveSchedulerConfig(ctx context.Context, cfg *SchedulerConfig) error {
	sanitized := sanitizeSchedulerConfig(cfg)
	now := time.Now().UTC()
	if sanitized.GloballyPaused && sanitized.PausedAt == nil {
		sanitized.PausedAt = &now
	}
	if !sanitized.GloballyPaused {
		sanitized.PausedAt = nil
		sanitized.PausedBy = ""
	}
	sanitized.UpdatedAt = &now

	data, err := json.Marshal(sanitized)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduler config: %w", err)
	}
	if err := writeFileToWorkspace(ctx, schedulerConfigFilePath, string(data)); err != nil {
		return fmt.Errorf("failed to write scheduler config: %w", err)
	}
	return nil
}

func LoadSchedulerConfig(ctx context.Context) (*SchedulerConfig, error) {
	content, exists, err := readFileFromWorkspace(ctx, schedulerConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read scheduler config: %w", err)
	}
	if !exists {
		return &SchedulerConfig{}, nil
	}

	var cfg SchedulerConfig
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal scheduler config: %w", err)
	}
	return sanitizeSchedulerConfig(&cfg), nil
}

func (s *SchedulerService) IsGloballyPaused(ctx context.Context) (bool, *SchedulerConfig, error) {
	cfg, err := LoadSchedulerConfig(ctx)
	if err != nil {
		return false, nil, err
	}
	return cfg.GloballyPaused, cfg, nil
}

// IsProductPaused reports whether a product's timed runs are held, by the global pause or its own (PLAT-782).
func (s *SchedulerService) IsProductPaused(ctx context.Context, product string) (bool, *SchedulerConfig, error) {
	cfg, err := LoadSchedulerConfig(ctx)
	if err != nil {
		return false, nil, err
	}
	return cfg.ProductPaused(product), cfg, nil
}

// workflowPauseProduct is the product a workflow's timed runs belong to for the pause.
func workflowPauseProduct(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "relay") {
		return "relays"
	}
	return "agentworks"
}

func getSchedulerConfigHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		cfg, err := LoadSchedulerConfig(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SchedulerConfigResponse{SchedulerConfig: cfg, RecentPauseEvents: recentSchedulerPauseEvents(r.Context(), 5)})
	}
}

func updateSchedulerConfigHandler(svc *SchedulerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req SchedulerConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		previous, _ := LoadSchedulerConfig(r.Context())
		// An older client sends only globally_paused: it must not wipe the per-product pauses.
		if req.PausedProducts == nil && previous != nil {
			req.PausedProducts = previous.PausedProducts
		}
		if err := SaveSchedulerConfig(r.Context(), &req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cfg, err := LoadSchedulerConfig(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		resp := SchedulerConfigResponse{SchedulerConfig: cfg}
		wasPaused := previous != nil && previous.GloballyPaused
		if wasPaused != cfg.GloballyPaused {
			claims := GetUserFromContext(r.Context())
			ev := SchedulerPauseEvent{At: time.Now().UTC(), Action: "paused", Via: strings.TrimSpace(req.PausedBy), UserAgent: truncateString(r.UserAgent(), 160)}
			if claims != nil {
				ev.UserID, ev.Username = claims.UserID, claims.Username
			}
			if !cfg.GloballyPaused {
				ev.Action = "resumed"
				if previous != nil && previous.PausedAt != nil {
					since := previous.PausedAt.UTC()
					ev.PausedSince = &since
					resp.SkippedWhilePaused = svc.skippedWhilePaused(r.Context(), claims, since, ev.At)
				}
			}
			appendSchedulerPauseEvent(r.Context(), ev)
		}
		// Each product paused or resumed on its own is recorded as its own event.
		before := map[string]bool{}
		if previous != nil {
			for _, product := range previous.PausedProducts {
				before[product] = true
			}
		}
		for _, product := range schedulerPauseProducts {
			now := false
			for _, paused := range cfg.PausedProducts {
				now = now || paused == product
			}
			if now == before[product] {
				continue
			}
			ev := SchedulerPauseEvent{At: time.Now().UTC(), Action: "paused", Product: product, Via: strings.TrimSpace(req.PausedBy), UserAgent: truncateString(r.UserAgent(), 160)}
			if !now {
				ev.Action = "resumed"
			}
			if claims := GetUserFromContext(r.Context()); claims != nil {
				ev.UserID, ev.Username = claims.UserID, claims.Username
			}
			appendSchedulerPauseEvent(r.Context(), ev)
		}
		resp.RecentPauseEvents = recentSchedulerPauseEvents(r.Context(), 5)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
