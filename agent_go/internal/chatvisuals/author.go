package chatvisuals

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
)

// Generated from the same catalog used by the browser and the formatter.
//
//go:embed author-schema.json
var authorSchema []byte

func AuthorSchema() (map[string]any, error) {
	var schema map[string]any
	err := json.Unmarshal(authorSchema, &schema)
	return schema, err
}

// Author formats data the caller supplied. The caller chooses whether to
// include the returned block in its own reply.
func Author(ctx context.Context, fields map[string]any) (string, error) {
	data, err := json.Marshal(fields)
	if err != nil {
		return "", errors.New("visual input could not be read")
	}
	if len(data) > 200000 {
		return "", errors.New("visual input is too long. Use fewer items or shorter text")
	}
	out, err := runFormatter(ctx, struct {
		Visual json.RawMessage `json:"visual"`
	}{data})
	if err != nil {
		return "", err
	}
	var result struct {
		Markdown string `json:"markdown"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(out, &result) != nil || result.Error == "visual_format_failed" {
		return "", errors.New("visual authoring failed")
	}
	if result.Error != "" {
		return "", errors.New(result.Error)
	}
	if result.Markdown == "" {
		return "", errors.New("visual authoring returned an empty block")
	}
	return result.Markdown, nil
}
