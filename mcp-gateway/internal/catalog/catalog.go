// Package catalog serves connector templates snapshotted from the AgentWorks
// server list. The snapshot keeps the gateway module self-contained; M1
// re-sources it from a single canonical file.
package catalog

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed catalog.json
var raw []byte

// Provider is one connectable upstream template.
type Provider struct {
	Name  string
	Key   string // normalized public-name prefix, e.g. "notion"
	URL   string
	OAuth bool
}

// Catalog is the sorted provider list.
type Catalog struct {
	Providers []Provider
	byName    map[string]Provider
}

// Load parses the embedded snapshot.
func Load() (*Catalog, error) {
	var doc map[string]map[string]struct {
		URL   string `json:"url"`
		OAuth any    `json:"oauth"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	servers := doc["mcpServers"]
	c := &Catalog{byName: map[string]Provider{}}
	for name, s := range servers {
		if s.URL == "" {
			continue
		}
		p := Provider{Name: name, Key: Key(name), URL: s.URL, OAuth: s.OAuth != nil}
		c.Providers = append(c.Providers, p)
		c.byName[name] = p
	}
	sort.Slice(c.Providers, func(i, j int) bool { return c.Providers[i].Name < c.Providers[j].Name })
	return c, nil
}

// Key normalizes a provider name to a public-name prefix.
func Key(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Find returns a provider template by catalog name.
func (c *Catalog) Find(name string) (Provider, bool) {
	p, ok := c.byName[name]
	return p, ok
}
