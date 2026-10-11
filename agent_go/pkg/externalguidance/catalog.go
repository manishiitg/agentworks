// Package externalguidance reads opt-in exposure metadata from guidance sources.
package externalguidance

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

type Topic struct {
	Name                string   `yaml:"name"`
	Description         string   `yaml:"description"`
	External            bool     `yaml:"external"`
	ExternalDescription string   `yaml:"external_description"`
	ExternalNote        string   `yaml:"external_note"`
	Scopes              []string `yaml:"external_scopes"`
	Content             string   `yaml:"-"`
}

// Parse strips metadata before rendering or serving the body. Unmarked files
// remain private; malformed metadata is an error, never implicit admission.
func Parse(raw []byte) (Topic, string, error) {
	text := string(raw)
	var topic Topic
	if !strings.HasPrefix(text, "---\n") {
		return topic, text, nil
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return topic, "", fmt.Errorf("unterminated guidance metadata")
	}
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &topic); err != nil {
		return topic, "", fmt.Errorf("guidance metadata: %w", err)
	}
	body := text[4+end+5:]
	if topic.ExternalDescription != "" {
		topic.Description = topic.ExternalDescription
	}
	if topic.External && (strings.TrimSpace(topic.Name) == "" || strings.TrimSpace(topic.Description) == "" || strings.TrimSpace(body) == "") {
		return topic, "", fmt.Errorf("external guidance needs a name, description and body")
	}
	topic.Content = body
	return topic, body, nil
}

func Load(fsys fs.FS, pattern string) ([]Topic, error) {
	paths, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	var topics []Topic
	for _, path := range paths {
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, err
		}
		topic, _, err := Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if topic.External {
			topics = append(topics, topic)
		}
	}
	return topics, nil
}
