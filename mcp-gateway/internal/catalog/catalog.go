// Package catalog serves connector templates snapshotted from the AgentWorks
// server list. LoadFile reuses the host product catalog; the embedded snapshot
// keeps the standalone gateway module self-contained.
package catalog

import (
	_ "embed"
	"encoding/json"
	"os"
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
	return parse(raw)
}

// LoadFile reuses the host product MCP catalog without duplicating OAuth metadata.
func LoadFile(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data)
}

func parse(data []byte) (*Catalog, error) {
	var doc map[string]map[string]struct {
		URL   string `json:"url"`
		OAuth any    `json:"oauth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
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
// The name people type ("Github", "google drive") matches the catalog's
// spelling ("GitHub", "GoogleDrive") by its normalized key.
func (c *Catalog) Find(name string) (Provider, bool) {
	if p, ok := c.byName[name]; ok {
		return p, true
	}
	key := Key(name)
	if key == "" {
		return Provider{}, false
	}
	for _, p := range c.Providers {
		if p.Key == key {
			return p, true
		}
	}
	return Provider{}, false
}

// Names lists the catalog's provider names, sorted.
func (c *Catalog) Names() []string {
	names := make([]string, 0, len(c.Providers))
	for _, p := range c.Providers {
		names = append(names, p.Name)
	}
	return names
}
