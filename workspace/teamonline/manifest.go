// Package teamonline contains item-scoped online publication data, not Team
// identity or admission. Callers supply already admitted source/destination
// handles; request bodies never grant filesystem or database authority.
package teamonline

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

const Descriptor = "item.json"

// Dataset lists complete tables. Foreign-key parents are added by the engine;
// views and triggers are explicit and must compile using only that closure.
type Dataset struct {
	Tables   []string `json:"tables"`
	Views    []string `json:"views,omitempty"`
	Triggers []string `json:"triggers,omitempty"`
}

type Automation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Table struct {
	Name  string `json:"name"`
	Rows  int64  `json:"rows"`
	Bytes int64  `json:"bytes"`
}

type Dependencies struct {
	Dataset     Dataset      `json:"dataset"`
	Functions   []string     `json:"functions,omitempty"`
	Assets      []string     `json:"assets,omitempty"`
	Automations []Automation `json:"automations,omitempty"`
}

// Item is emitted from the source-owned published revision, not a model's
// inline file bundle. Asset and data parts are covered by the transport's
// existing SHA/size manifest; no credentials or caller grants are included.
type Item struct {
	ID           string              `json:"id"`
	Revision     string              `json:"revision"`
	Snapshot     dashboards.Snapshot `json:"snapshot"`
	Dependencies Dependencies        `json:"dependencies"`
	DatasetParts []string            `json:"dataset_parts,omitempty"`
}

func DecodeItem(reader io.Reader) (Item, error) {
	var item Item
	decoder := json.NewDecoder(io.LimitReader(reader, publication.MaxFileBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		return item, fmt.Errorf("read the item's online dependency manifest: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return item, fmt.Errorf("the online dependency manifest must contain one item")
	}
	if item.ID == "" || len(item.ID) > 64 || len(item.Revision) != 64 {
		return item, fmt.Errorf("choose a published item and its current revision")
	}
	for _, p := range append(append([]string{}, item.Dependencies.Assets...), item.DatasetParts...) {
		if _, err := publication.CleanPath(p); err != nil || publication.CredentialPath(p) {
			return item, fmt.Errorf("an online dependency has an unavailable file path")
		}
	}
	for _, p := range item.DatasetParts {
		if !strings.HasPrefix(p, "data/") {
			return item, fmt.Errorf("dataset parts belong in the item package's data folder")
		}
	}
	for p := range item.Snapshot.Files {
		if _, err := publication.CleanPath(p); err != nil || publication.CredentialPath(p) {
			return item, fmt.Errorf("an online source file has an unavailable path")
		}
	}
	return item, nil
}
