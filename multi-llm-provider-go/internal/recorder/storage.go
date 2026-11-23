package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SaveResponse saves a recorded response to a JSON file
func SaveResponse(config RecordingConfig, responseData interface{}, chunkCount int, request RequestInfo) (string, error) {
	if !config.Enabled {
		return "", nil
	}

	// Compute request hash for matching
	requestHash, err := ComputeRequestHash(request)
	if err != nil {
		return "", fmt.Errorf("failed to compute request hash: %w", err)
	}

	// Determine file path
	baseDir := config.BaseDir
	if baseDir == "" {
		baseDir = "testdata"
	}

	// Create provider directory
	providerDir := filepath.Join(baseDir, config.Provider)
	if err := os.MkdirAll(providerDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create provider directory: %w", err)
	}

	// Generate filename with request hash for easier identification
	filename := generateFilenameWithHash(config.TestName, config.ModelID, requestHash)
	filePath := filepath.Join(providerDir, filename)

	// Create recorded response
	recorded := RecordedResponse{
		Provider:     config.Provider,
		ModelID:      config.ModelID,
		TestName:     config.TestName,
		RecordedAt:   time.Now(),
		RequestHash:  requestHash,
		Request:      request,
		ResponseData: responseData,
		ChunkCount:   chunkCount,
	}

	// Marshal to JSON
	jsonData, err := json.MarshalIndent(recorded, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal recorded response: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return "", fmt.Errorf("failed to write recorded response: %w", err)
	}

	return filePath, nil
}

// LoadResponse loads a recorded response from a JSON file
func LoadResponse(config RecordingConfig) (*RecordedResponse, error) {
	// Determine file path
	baseDir := config.BaseDir
	if baseDir == "" {
		baseDir = "testdata"
	}

	providerDir := filepath.Join(baseDir, config.Provider)

	// Find the most recent file matching the pattern
	// Pattern: {testname}_{model}_*.json
	pattern := fmt.Sprintf("%s_%s_*.json", config.TestName, sanitizeForFilename(config.ModelID))
	matches, err := filepath.Glob(filepath.Join(providerDir, pattern))
	if err != nil || len(matches) == 0 {
		// Fallback: try exact filename match
		filename := generateFilename(config.TestName, config.ModelID)
		filePath := filepath.Join(providerDir, filename)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to find recorded response: %w", err)
		}
		var recorded RecordedResponse
		if err := json.Unmarshal(data, &recorded); err != nil {
			return nil, fmt.Errorf("failed to unmarshal recorded response: %w", err)
		}
		return &recorded, nil
	}

	// Get the most recent file
	var mostRecent string
	var mostRecentTime time.Time
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if info.ModTime().After(mostRecentTime) {
			mostRecent = match
			mostRecentTime = info.ModTime()
		}
	}

	if mostRecent == "" {
		return nil, fmt.Errorf("no matching recorded response found")
	}

	// Read file
	data, err := os.ReadFile(mostRecent)
	if err != nil {
		return nil, fmt.Errorf("failed to read recorded response: %w", err)
	}

	// Unmarshal - ResponseData will be unmarshaled as interface{} (JSON object/array)
	var recorded RecordedResponse
	if err := json.Unmarshal(data, &recorded); err != nil {
		return nil, fmt.Errorf("failed to unmarshal recorded response: %w", err)
	}

	return &recorded, nil
}

// LoadResponseByRequest loads a recorded response that matches the given request
func LoadResponseByRequest(config RecordingConfig, request RequestInfo) (*RecordedResponse, error) {
	// Compute hash of current request
	currentHash, err := ComputeRequestHash(request)
	if err != nil {
		return nil, fmt.Errorf("failed to compute request hash: %w", err)
	}

	// Determine file path
	baseDir := config.BaseDir
	if baseDir == "" {
		baseDir = "testdata"
	}

	providerDir := filepath.Join(baseDir, config.Provider)

	// Find all files matching the pattern
	pattern := fmt.Sprintf("%s_%s_*.json", config.TestName, sanitizeForFilename(config.ModelID))
	matches, err := filepath.Glob(filepath.Join(providerDir, pattern))
	if err != nil {
		return nil, fmt.Errorf("failed to search for recorded responses: %w", err)
	}

	// Try to find exact hash match first
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}

		var recorded RecordedResponse
		if err := json.Unmarshal(data, &recorded); err != nil {
			continue
		}

		// Check if hash matches
		if recorded.RequestHash == currentHash {
			return &recorded, nil
		}
	}

	// If no exact match, return most recent (for backwards compatibility)
	if len(matches) > 0 {
		var mostRecent string
		var mostRecentTime time.Time
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			if info.ModTime().After(mostRecentTime) {
				mostRecent = match
				mostRecentTime = info.ModTime()
			}
		}

		if mostRecent != "" {
			data, err := os.ReadFile(mostRecent)
			if err == nil {
				var recorded RecordedResponse
				if err := json.Unmarshal(data, &recorded); err == nil {
					return &recorded, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no recorded response found matching request hash: %s", currentHash)
}

// generateFilename creates a filename from test name and model ID
func generateFilename(testName, modelID string) string {
	// Sanitize model ID for filename (replace special chars)
	sanitizedModel := sanitizeForFilename(modelID)

	// Generate filename: testname_model_001.json
	// Use timestamp for uniqueness
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s_%s.json", testName, sanitizedModel, timestamp)
}

// generateFilenameWithHash creates a filename with request hash
func generateFilenameWithHash(testName, modelID, requestHash string) string {
	// Sanitize model ID for filename (replace special chars)
	sanitizedModel := sanitizeForFilename(modelID)

	// Use first 8 chars of hash for filename (keeps it readable)
	hashShort := requestHash[:8]
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s_%s_%s.json", testName, sanitizedModel, hashShort, timestamp)
}

// sanitizeForFilename removes/replaces characters unsafe for filenames
func sanitizeForFilename(s string) string {
	result := []rune{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			result = append(result, r)
		} else if r == '.' || r == '/' {
			result = append(result, '_')
		}
	}
	return string(result)
}
