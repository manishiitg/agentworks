package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"

	"github.com/PaesslerAG/jsonpath"
)

const (
	MaxChecksPerFile  = 100              // Limit checks per file
	MaxFilesPerSchema = 20               // Limit files per schema
	MaxJSONFileSize   = 10 * 1024 * 1024 // 10MB max file size
)

// WorkspaceVerificationResult represents the result of pre-validation
type WorkspaceVerificationResult struct {
	OverallPass  bool
	FilesChecked []FileCheckResult
	Summary      ValidationSummary
}

// ValidationSummary provides a summary of validation checks
type ValidationSummary struct {
	TotalChecks    int
	PassedChecks   int
	FailedChecks   int
	SchemaErrors   int // Count of schema errors (e.g., invalid regex patterns)
	Errors         []ValidationError
	SchemaWarnings []ValidationError // Schema errors that don't fail validation
}

// ValidationError represents a single validation error
type ValidationError struct {
	File      string
	Path      string
	CheckType string // "must_exist", "value_type", "min_length", etc.
	Expected  string
	Actual    string
	Message   string
}

// FileCheckResult represents the result of checking a single file
type FileCheckResult struct {
	FileName        string
	Exists          bool
	IsJSON          bool
	ResolvedPath    string
	AlternatePath   string
	AlternateExists bool
	JSONChecks      []JSONCheckResult
}

// JSONCheckResult represents the result of a single JSON validation check
type JSONCheckResult struct {
	Path        string
	Passed      bool
	CheckType   string
	Expected    interface{}
	Actual      interface{}
	ErrorMsg    string
	SchemaError bool // True if this is a schema error (e.g., invalid regex) rather than a validation failure
}

// WorkspaceToolExecutors is a type alias for the workspace tool executors map
type WorkspaceToolExecutors map[string]interface{}

type validationPathScopes struct {
	StepExecutionPath string
	WorkflowRootPath  string
}

// RunPreValidation runs pre-validation on a step's output
// validationSchema can be passed directly (preferred) or extracted from PlanStepInterface
// If validationSchema is nil, pre-validation is skipped and a result indicating skip is returned
func RunPreValidation(
	ctx context.Context,
	validationSchema *ValidationSchema, // Validation schema (preferred - passed from TodoStep)
	workspacePath string,
	baseOrchestrator *orchestrator.BaseOrchestrator,
) (*WorkspaceVerificationResult, error) {
	// If schema is nil, skip pre-validation and return a result indicating skip
	if validationSchema == nil {
		return &WorkspaceVerificationResult{
			OverallPass:  true, // Pass so it doesn't block LLM validation
			FilesChecked: []FileCheckResult{},
			Summary: ValidationSummary{
				TotalChecks:    0,
				PassedChecks:   0,
				FailedChecks:   0,
				SchemaErrors:   0,
				Errors:         []ValidationError{},
				SchemaWarnings: []ValidationError{},
			},
		}, nil
	}

	// If schema is empty (no files and no db rules), skip pre-validation
	if len(validationSchema.Files) == 0 && len(validationSchema.DB) == 0 {
		return &WorkspaceVerificationResult{
			OverallPass:  true, // Pass so it doesn't block LLM validation
			FilesChecked: []FileCheckResult{},
			Summary: ValidationSummary{
				TotalChecks:    0,
				PassedChecks:   0,
				FailedChecks:   0,
				SchemaErrors:   0,
				Errors:         []ValidationError{},
				SchemaWarnings: []ValidationError{},
			},
		}, nil
	}

	// Validate schema limits
	if err := validateSchemaLimits(validationSchema); err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}

	// Perform validation with schema
	return validateWithSchema(ctx, validationSchema, workspacePath, baseOrchestrator)
}

// PreValidationLogEntry represents a pre-validation result persisted to disk
type PreValidationLogEntry struct {
	StepID            string            `json:"step_id"`
	StepPath          string            `json:"step_path"`
	RunFolder         string            `json:"run_folder,omitempty"`
	GroupName         string            `json:"group_name,omitempty"`
	ExecutionMode     string            `json:"execution_mode,omitempty"`
	ValidationPhase   string            `json:"validation_phase,omitempty"`
	ExecutionAttempt  int               `json:"execution_attempt"`
	ValidationAttempt int               `json:"validation_attempt"`
	Timestamp         string            `json:"timestamp"`
	OverallPass       bool              `json:"overall_pass"`
	TotalChecks       int               `json:"total_checks"`
	PassedChecks      int               `json:"passed_checks"`
	FailedChecks      int               `json:"failed_checks"`
	Errors            []ValidationError `json:"errors,omitempty"`
	FilesChecked      []FileCheckResult `json:"files_checked,omitempty"`
	Schema            *ValidationSchema `json:"schema_used,omitempty"`
}

// PreValidationAttempt identifies one concrete validation invocation within a
// run. The caller supplies both counters instead of deriving them from file
// order or timestamps, so a retry can never overwrite its earlier evidence.
type PreValidationAttempt struct {
	ExecutionMode     string
	ValidationPhase   string
	ExecutionAttempt  int
	ValidationAttempt int
}

func SavePreValidationLog(
	ctx context.Context,
	bo *orchestrator.BaseOrchestrator,
	validationWorkspacePath string,
	stepID string,
	stepPath string,
	results *WorkspaceVerificationResult,
	schema *ValidationSchema,
	workspaceRoot string,
	runFolder string,
	groupName string,
	attempts ...PreValidationAttempt,
) {
	if results == nil {
		return
	}
	attempt := PreValidationAttempt{ExecutionMode: "unknown", ValidationPhase: "validation", ExecutionAttempt: 1, ValidationAttempt: 1}
	if len(attempts) > 0 {
		attempt = attempts[0]
	}

	entry := PreValidationLogEntry{
		StepID:            stepID,
		StepPath:          stepPath,
		RunFolder:         runFolder,
		GroupName:         groupName,
		ExecutionMode:     strings.TrimSpace(attempt.ExecutionMode),
		ValidationPhase:   normalizePreValidationAttemptPart(attempt.ValidationPhase, "validation"),
		ExecutionAttempt:  attempt.ExecutionAttempt,
		ValidationAttempt: attempt.ValidationAttempt,
		Timestamp:         time.Now().Format(time.RFC3339),
		OverallPass:       results.OverallPass,
		TotalChecks:       results.Summary.TotalChecks,
		PassedChecks:      results.Summary.PassedChecks,
		FailedChecks:      results.Summary.FailedChecks,
		Errors:            results.Summary.Errors,
		FilesChecked:      results.FilesChecked,
		Schema:            schema,
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return
	}

	logFolder := getArtifactFolderName(stepID, stepPath)
	logPath := fmt.Sprintf("%s/logs/%s/pre_validation.json", validationWorkspacePath, logFolder)
	historyPath := fmt.Sprintf("%s/logs/%s/pre_validation_%s_execution_%03d_attempt_%03d.json",
		validationWorkspacePath, logFolder, entry.ValidationPhase, entry.ExecutionAttempt, entry.ValidationAttempt)
	// Write the immutable attempt record first. A failure to update the compact
	// pointer must never erase the evidence that explains a transient failure.
	_ = bo.WriteWorkspaceFile(ctx, historyPath, string(data))
	_ = bo.WriteWorkspaceFile(ctx, logPath, string(data))

}

func normalizePreValidationAttemptPart(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = fallback
	}
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return fallback
	}
	return value
}

// validateSchemaLimits checks if the schema exceeds resource limits
func validateSchemaLimits(schema *ValidationSchema) error {
	if len(schema.Files) > MaxFilesPerSchema {
		return fmt.Errorf("schema exceeds max files (%d)", MaxFilesPerSchema)
	}

	for _, file := range schema.Files {
		if len(file.JSONChecks) > MaxChecksPerFile {
			return fmt.Errorf("file %s exceeds max checks (%d)", file.FileName, MaxChecksPerFile)
		}
	}

	return nil
}

// validateWithSchema performs validation using the provided schema
func validateWithSchema(
	ctx context.Context,
	schema *ValidationSchema,
	workspacePath string,
	baseOrchestrator *orchestrator.BaseOrchestrator,
) (*WorkspaceVerificationResult, error) {
	result := &WorkspaceVerificationResult{
		OverallPass:  true,
		FilesChecked: []FileCheckResult{},
		Summary: ValidationSummary{
			TotalChecks:    0,
			PassedChecks:   0,
			FailedChecks:   0,
			SchemaErrors:   0,
			Errors:         []ValidationError{},
			SchemaWarnings: []ValidationError{},
		},
	}

	// Validate each file in the schema
	for _, fileRule := range schema.Files {
		fileResult := validateFile(ctx, fileRule, workspacePath, baseOrchestrator)
		result.FilesChecked = append(result.FilesChecked, fileResult)

		// Update summary
		for _, jsonCheck := range fileResult.JSONChecks {
			result.Summary.TotalChecks++
			if jsonCheck.SchemaError {
				// Schema error (e.g., invalid regex) - don't fail validation, but track it
				result.Summary.SchemaErrors++
				result.Summary.SchemaWarnings = append(result.Summary.SchemaWarnings, ValidationError{
					File:      fileResult.FileName,
					Path:      jsonCheck.Path,
					CheckType: jsonCheck.CheckType,
					Expected:  fmt.Sprintf("%v", jsonCheck.Expected),
					Actual:    fmt.Sprintf("%v", jsonCheck.Actual),
					Message:   jsonCheck.ErrorMsg,
				})
			} else if jsonCheck.Passed {
				result.Summary.PassedChecks++
			} else {
				result.Summary.FailedChecks++
				result.OverallPass = false
				// Add error to summary
				result.Summary.Errors = append(result.Summary.Errors, ValidationError{
					File:      fileResult.FileName,
					Path:      jsonCheck.Path,
					CheckType: jsonCheck.CheckType,
					Expected:  fmt.Sprintf("%v", jsonCheck.Expected),
					Actual:    fmt.Sprintf("%v", jsonCheck.Actual),
					Message:   jsonCheck.ErrorMsg,
				})
			}
		}

		// Check file existence
		if fileRule.MustExist && !fileResult.Exists {
			result.OverallPass = false
			result.Summary.TotalChecks++
			result.Summary.FailedChecks++
			message := fmt.Sprintf("File %s must exist but was not found", fileResult.FileName)
			if hint := buildValidationPathHint(fileResult.ResolvedPath, fileResult.AlternatePath, fileResult.AlternateExists); hint != "" {
				message += " " + hint
			}
			result.Summary.Errors = append(result.Summary.Errors, ValidationError{
				File:      fileResult.FileName,
				Path:      "",
				CheckType: "must_exist",
				Expected:  "file exists",
				Actual:    "file not found",
				Message:   message,
			})
		} else if fileRule.MustExist && fileResult.Exists {
			result.Summary.TotalChecks++
			result.Summary.PassedChecks++
		}
	}

	// Validate each db rule — a read-only query against db/db.sqlite (the source of
	// truth). Reuses FileCheckResult so summary aggregation + corrective feedback
	// work identically to file checks.
	for _, dbRule := range schema.DB {
		dbResult := validateDBRule(ctx, dbRule, baseOrchestrator)
		result.FilesChecked = append(result.FilesChecked, dbResult)
		applyCheckedToSummary(result, dbResult)
	}

	return result, nil
}

// validateFile validates a single file against its rules
func validateFile(
	ctx context.Context,
	fileRule FileValidationRule,
	workspacePath string,
	baseOrchestrator *orchestrator.BaseOrchestrator,
) FileCheckResult {
	result := FileCheckResult{
		FileName:   fileRule.FileName,
		Exists:     false,
		IsJSON:     false,
		JSONChecks: []JSONCheckResult{},
	}

	// Resolve schema path against the correct scope.
	// Bare filenames are step-local; workflow paths like knowledgebase/... are workflow-root relative.
	fullPath, err := validateFilePath(workspacePath, fileRule.FileName)
	if err != nil {
		// Path validation failed - add error and return
		result.JSONChecks = append(result.JSONChecks, JSONCheckResult{
			Path:      "",
			Passed:    false,
			CheckType: "path_validation",
			Expected:  "valid file path",
			Actual:    err.Error(),
			ErrorMsg:  fmt.Sprintf("Invalid file path: %v", err),
		})
		return result
	}
	result.ResolvedPath = fullPath

	alternatePath := deriveAlternateValidationPath(workspacePath, fileRule.FileName, fullPath)
	if alternatePath != "" && alternatePath != fullPath {
		result.AlternatePath = alternatePath
		alternateExists, altErr := baseOrchestrator.CheckWorkspaceFileExists(ctx, alternatePath)
		if altErr == nil {
			result.AlternateExists = alternateExists
		}
	}

	// Check if file exists
	exists, err := baseOrchestrator.CheckWorkspaceFileExists(ctx, fullPath)
	if err != nil {
		// Error checking file - add error and return
		result.JSONChecks = append(result.JSONChecks, JSONCheckResult{
			Path:      "",
			Passed:    false,
			CheckType: "file_check",
			Expected:  "file accessible",
			Actual:    err.Error(),
			ErrorMsg:  fmt.Sprintf("Error checking file existence: %v", err),
		})
		return result
	}

	// A step may only write in two places: its execution folder or workflow-root db/.
	// If the schema targets db/ and the file is missing there but present in the step
	// folder, promote the alternate so existence + JSON reads both use it. We scope
	// this to db/ because other workflow-root folders (knowledgebase/, learnings/, …)
	// are written by dedicated agents — a step-local copy there is always a bug.
	readPath := fullPath
	if !exists && result.AlternateExists && isDBSchemaPath(fileRule.FileName) {
		readPath = alternatePath
		result.ResolvedPath = alternatePath
		result.AlternatePath = ""
		result.AlternateExists = false
		exists = true
	}

	result.Exists = exists

	// If file doesn't exist and we have JSON checks, we can't validate them
	if !exists {
		return result
	}

	// Check if file has JSON checks - if not, skip text reading and JSON parsing.
	// Binary artifacts such as mp3/mp4 files can be valid outputs when the schema
	// only requires existence.
	hasJSONChecks := len(fileRule.JSONChecks) > 0
	if !hasJSONChecks {
		return result
	}

	// Read file content
	content, err := baseOrchestrator.ReadWorkspaceFile(ctx, readPath)
	if err != nil {
		result.JSONChecks = append(result.JSONChecks, JSONCheckResult{
			Path:      "",
			Passed:    false,
			CheckType: "file_read",
			Expected:  "file readable",
			Actual:    err.Error(),
			ErrorMsg:  fmt.Sprintf("Error reading file: %v", err),
		})
		return result
	}

	// Check file size
	if len(content) > MaxJSONFileSize {
		result.JSONChecks = append(result.JSONChecks, JSONCheckResult{
			Path:      "",
			Passed:    false,
			CheckType: "file_size",
			Expected:  fmt.Sprintf("file size <= %d bytes", MaxJSONFileSize),
			Actual:    fmt.Sprintf("%d bytes", len(content)),
			ErrorMsg:  fmt.Sprintf("File size exceeds maximum (%d bytes)", MaxJSONFileSize),
		})
		return result
	}

	// If file doesn't have .json extension and has JSON checks, it might be incorrectly configured
	// But we'll still try to parse it - if it fails, we'll only fail if there are JSON checks
	hasJSONExtension := strings.HasSuffix(strings.ToLower(fileRule.FileName), ".json")

	// Try to parse as JSON
	var jsonData interface{}
	if err := json.Unmarshal([]byte(content), &jsonData); err != nil {
		// Not valid JSON
		if hasJSONChecks {
			// File has JSON checks but isn't valid JSON - add error
			result.JSONChecks = append(result.JSONChecks, JSONCheckResult{
				Path:      "",
				Passed:    false,
				CheckType: "json_parse",
				Expected:  "valid JSON",
				Actual:    err.Error(),
				ErrorMsg:  fmt.Sprintf("File is not valid JSON: %v", err),
			})
			// If file doesn't have .json extension, suggest updating the validation schema
			if !hasJSONExtension {
				result.JSONChecks[len(result.JSONChecks)-1].ErrorMsg += fmt.Sprintf(" (File '%s' does not have .json extension - validation schema may need to exclude this file or remove JSON checks)", fileRule.FileName)
			}
			return result
		}
		// No JSON checks - file doesn't need to be JSON, just return with exists check
		return result
	}

	result.IsJSON = true

	// Validate JSON checks
	for _, check := range fileRule.JSONChecks {
		checkResult := validateJSONCheck(ctx, check, jsonData)
		result.JSONChecks = append(result.JSONChecks, checkResult)
	}

	annotateJSONCheckPathHints(&result)

	return result
}

// jsonPathMultiMatchPattern recognizes standard JSONPath syntax that can
// match more than one location: wildcards (*), recursive descent (..),
// filter expressions (?( ), and slice/union index groups ([0:2], [0,1]).
// A plain numeric index ([0]) or a bare field path deliberately does not
// match -- that names exactly one location, regardless of what type its
// value happens to be.
var jsonPathMultiMatchPattern = regexp.MustCompile(`\*|\.\.|\?\(|\[[^\]]*[:,][^\]]*\]`)

// jsonPathHasMultipleMatches reports whether path can name more than one
// JSON location. See the comment above its one call site for why this
// distinction is load-bearing rather than cosmetic.
func jsonPathHasMultipleMatches(path string) bool {
	return jsonPathMultiMatchPattern.MatchString(path)
}

// validateJSONCheck validates a single JSON check
func validateJSONCheck(
	ctx context.Context,
	check JSONValidationCheck,
	jsonData interface{},
) JSONCheckResult {
	result := JSONCheckResult{
		Path:      check.Path,
		Passed:    false,
		CheckType: "",
		Expected:  nil,
		Actual:    nil,
		ErrorMsg:  "",
	}
	// An array wildcard expresses a per-item contract. Expand it before the
	// JSONPath library flattens results: otherwise an empty parent and a
	// populated parent whose items lack a required field become indistinguishable.
	if index := strings.Index(check.Path, "[*]"); index >= 0 && !jsonPathHasMultipleMatches(check.Path[:index]) {
		parent, parentErr := jsonpath.Get(check.Path[:index], jsonData)
		if items, ok := parent.([]interface{}); parentErr == nil && ok {
			result.Passed = true
			result.CheckType = "per_item"
			for i, item := range items {
				itemCheck := check
				itemCheck.Path = "$" + check.Path[index+3:]
				got := validateJSONCheck(ctx, itemCheck, item)
				got.Path = check.Path
				if !got.Passed {
					got.ErrorMsg = fmt.Sprintf("match %d of %d: %s", i, len(items), got.ErrorMsg)
					return got
				}
				result = got
			}
			return result
		}
	}

	// Evaluate JSONPath
	values, err := jsonpath.Get(check.Path, jsonData)
	if err != nil {
		// Path doesn't exist or is invalid
		if check.MustExist {
			result.CheckType = "must_exist"
			result.Expected = "path exists"
			result.Actual = "path not found"
			result.ErrorMsg = fmt.Sprintf("Path %s must exist but was not found: %v", check.Path, err)
			return result
		}
		// Path doesn't need to exist, so this check passes
		result.Passed = true
		return result
	}

	// Path exists - if MustExist is true, this check passes
	if check.MustExist {
		result.Passed = true
		result.CheckType = "must_exist"
	}

	// If path exists but MustExist is false, we still need to validate other checks
	// Handle JSONPath return value correctly:
	// - If path points to an array field directly (like $.missing_months), jsonpath.Get returns the array itself
	// - If path uses wildcards/filters, jsonpath.Get returns a slice of matching results
	// - If path points to a scalar, jsonpath.Get returns the scalar value
	//
	// []interface{} is genuinely ambiguous between those first two cases --
	// PaesslerAG/jsonpath.Get returns that same Go type whether $.notes'
	// own value is the array [] / ["a"], or $.notes[*] matched zero/one/many
	// separate locations. check.Path itself is the only place that
	// ambiguity can be resolved: a definite path (no wildcard/recursive-
	// descent/filter/slice/union syntax) always names exactly one location,
	// so whatever jsonpath.Get returns for it IS that location's value,
	// whatever its type -- never a collection of match results to unwrap.
	// Treating a definite path's own array value as a multi-match
	// collection and silently substituting its first element let a $.notes
	// field actually holding a JSON array pass a value_type=string check
	// (PUL-61C84987): a non-empty string-containing array's first element
	// is itself a string, so validateValueType saw a string and passed.
	var value interface{}
	var multiMatchValues []interface{}
	if valuesSlice, ok := values.([]interface{}); ok && jsonPathHasMultipleMatches(check.Path) {
		multiMatchValues = valuesSlice
		if len(valuesSlice) > 0 {
			value = valuesSlice[0]
		} else {
			// A wildcard with no matches has no values to type/range/pattern check.
			// Existence is a separate contract; require a nonempty parent array with
			// min_length when the workflow must produce at least one item.
			result.CheckType = "must_exist"
			result.Expected = "at least one matching value"
			result.Actual = 0
			result.Passed = !check.MustExist
			if check.MustExist {
				result.ErrorMsg = fmt.Sprintf("Path %s must exist but matched no values", check.Path)
			} else {
				result.CheckType = "no_matches"
			}
			return result
		}
	} else {
		value = values
	}

	// A genuine multi-match path (e.g. $.items[*].name) checks every matched
	// value against a per-value predicate, not only the first -- otherwise a
	// valid first item followed by invalid ones would silently pass, since
	// only value[0] was ever inspected. This applies uniformly to every
	// per-value check below (type, length, numeric range, pattern), value_type
	// included: an earlier version special-cased value_type="array" to check
	// whether the *multi-match result slice itself* was a Go []interface{},
	// which is trivially always true for a genuine multi-match regardless of
	// what any individual matched value actually was -- e.g. a real wildcard
	// match on $.items[*].tags with one item's tags holding a plain string
	// would still report value_type=array as passed. value_type=array on a
	// multi-match path means "every matched value must itself be an array",
	// the same per-match rule as every other predicate here, not "the
	// collection of matches is a slice."
	everyMultiMatch := len(multiMatchValues) > 1
	checkEveryMatch := func(checkFn func(interface{}) JSONCheckResult) JSONCheckResult {
		if !everyMultiMatch {
			return checkFn(value)
		}
		for index, matched := range multiMatchValues {
			matchResult := checkFn(matched)
			if !matchResult.Passed {
				matchResult.ErrorMsg = fmt.Sprintf("match %d of %d: %s", index, len(multiMatchValues), matchResult.ErrorMsg)
				return matchResult
			}
		}
		return checkFn(multiMatchValues[0])
	}

	// Validate value type.
	if check.ValueType != "" {
		typeResult := checkEveryMatch(func(v interface{}) JSONCheckResult {
			return validateValueType(check.Path, v, check.ValueType)
		})
		if !typeResult.Passed {
			return typeResult
		}
		result.CheckType = "value_type"
		result.Passed = true
	}

	// Validate min/max length for strings and arrays.
	if check.MinLength != nil || check.MaxLength != nil {
		lengthResult := checkEveryMatch(func(v interface{}) JSONCheckResult {
			return validateLength(check.Path, v, check.MinLength, check.MaxLength)
		})
		if !lengthResult.Passed {
			return lengthResult
		}
		if result.CheckType == "" {
			result.CheckType = "length"
		}
		result.Passed = true
	}

	// Validate min/max value for numbers.
	if check.MinValue != nil || check.MaxValue != nil {
		valueResult := checkEveryMatch(func(v interface{}) JSONCheckResult {
			return validateValueRange(check.Path, v, check.MinValue, check.MaxValue)
		})
		if !valueResult.Passed {
			return valueResult
		}
		if result.CheckType == "" {
			result.CheckType = "value_range"
		}
		result.Passed = true
	}

	// Validate pattern (regex) for strings.
	if check.Pattern != "" {
		patternResult := checkEveryMatch(func(v interface{}) JSONCheckResult {
			return validatePattern(check.Path, v, check.Pattern)
		})
		if !patternResult.Passed {
			return patternResult
		}
		if result.CheckType == "" {
			result.CheckType = "pattern"
		}
		result.Passed = true
	}

	// Validate consistency check
	if check.ConsistencyCheck != nil {
		// Skip consistency check if compare_with_path is empty (malformed schema)
		// This allows other checks (like value_type) to still pass
		comparePath := strings.TrimSpace(check.ConsistencyCheck.CompareWithPath)
		if comparePath == "" {
			// Malformed consistency check - skip it but don't fail
			// The path exists and other checks can still validate it
		} else {
			consistencyResult := validateConsistency(ctx, check, jsonData)
			if !consistencyResult.Passed {
				return consistencyResult
			}
			if result.CheckType == "" {
				result.CheckType = "consistency"
			}
			result.Passed = true
		}
	}

	// If no specific checks were performed but path exists and MustExist is true, it passes
	if result.CheckType == "" && check.MustExist {
		result.Passed = true
		result.CheckType = "must_exist"
	}

	return result
}

// validateValueType validates that a value matches the expected type
func validateValueType(path string, value interface{}, expectedType string) JSONCheckResult {
	result := JSONCheckResult{
		Path:      path,
		CheckType: "value_type",
		Passed:    false,
		Expected:  expectedType,
		Actual:    fmt.Sprintf("%T", value),
	}

	switch expectedType {
	case "string":
		if _, ok := value.(string); ok {
			result.Passed = true
		} else {
			result.ErrorMsg = fmt.Sprintf("Expected string, got %T", value)
		}
	case "number":
		switch value.(type) {
		case float64, int, int64, float32:
			result.Passed = true
		default:
			result.ErrorMsg = fmt.Sprintf("Expected number, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); ok {
			result.Passed = true
		} else {
			result.ErrorMsg = fmt.Sprintf("Expected boolean, got %T", value)
		}
	case "array":
		if _, ok := value.([]interface{}); ok {
			result.Passed = true
		} else {
			result.ErrorMsg = fmt.Sprintf("Expected array, got %T", value)
		}
	case "object":
		if _, ok := value.(map[string]interface{}); ok {
			result.Passed = true
		} else {
			result.ErrorMsg = fmt.Sprintf("Expected object, got %T", value)
		}
	default:
		result.ErrorMsg = fmt.Sprintf("Unknown value type: %s", expectedType)
	}

	return result
}

// validateLength validates min/max length for strings and arrays
func validateLength(path string, value interface{}, minLength *int, maxLength *int) JSONCheckResult {
	result := JSONCheckResult{
		Path:      path,
		CheckType: "length",
		Passed:    false,
	}

	var length int
	var valueType string

	switch v := value.(type) {
	case string:
		length = len(v)
		valueType = "string"
	case []interface{}:
		length = len(v)
		valueType = "array"
	default:
		result.ErrorMsg = "Length validation only applies to strings and arrays"
		result.Expected = "string or array"
		result.Actual = fmt.Sprintf("%T", value)
		return result
	}

	if minLength != nil && length < *minLength {
		result.ErrorMsg = fmt.Sprintf("%s length %d is less than minimum %d", valueType, length, *minLength)
		result.Expected = fmt.Sprintf("length >= %d", *minLength)
		result.Actual = fmt.Sprintf("length = %d", length)
		return result
	}

	if maxLength != nil && length > *maxLength {
		result.ErrorMsg = fmt.Sprintf("%s length %d exceeds maximum %d", valueType, length, *maxLength)
		result.Expected = fmt.Sprintf("length <= %d", *maxLength)
		result.Actual = fmt.Sprintf("length = %d", length)
		return result
	}

	result.Passed = true
	return result
}

// validateValueRange validates min/max value for numbers
func validateValueRange(path string, value interface{}, minValue *float64, maxValue *float64) JSONCheckResult {
	result := JSONCheckResult{
		Path:      path,
		CheckType: "value_range",
		Passed:    false,
	}

	var numValue float64
	switch v := value.(type) {
	case float64:
		numValue = v
	case int:
		numValue = float64(v)
	case int64:
		numValue = float64(v)
	case float32:
		numValue = float64(v)
	default:
		result.ErrorMsg = "Value range validation only applies to numbers"
		result.Expected = "number"
		result.Actual = fmt.Sprintf("%T", value)
		return result
	}

	if minValue != nil && numValue < *minValue {
		result.ErrorMsg = fmt.Sprintf("Value %f is less than minimum %f", numValue, *minValue)
		result.Expected = fmt.Sprintf("value >= %f", *minValue)
		result.Actual = fmt.Sprintf("value = %f", numValue)
		return result
	}

	if maxValue != nil && numValue > *maxValue {
		result.ErrorMsg = fmt.Sprintf("Value %f exceeds maximum %f", numValue, *maxValue)
		result.Expected = fmt.Sprintf("value <= %f", *maxValue)
		result.Actual = fmt.Sprintf("value = %f", numValue)
		return result
	}

	result.Passed = true
	return result
}

// validatePattern validates a string against a regex pattern
func validatePattern(path string, value interface{}, pattern string) JSONCheckResult {
	result := JSONCheckResult{
		Path:        path,
		CheckType:   "pattern",
		Passed:      false,
		Expected:    pattern,
		SchemaError: false,
	}

	strValue, ok := value.(string)
	if !ok {
		result.ErrorMsg = "Pattern validation only applies to strings"
		result.Actual = fmt.Sprintf("%T", value)
		return result
	}

	// Try to compile and match with the pattern as-is
	regex, err := regexp.Compile(pattern)
	if err != nil {
		// Invalid regex pattern from schema - treat as schema error, not validation failure
		// This allows validation to continue even if LLM generated a malformed regex
		result.Passed = true      // Pass so it doesn't fail validation
		result.SchemaError = true // Mark as schema error
		result.ErrorMsg = fmt.Sprintf("Invalid regex pattern in schema (skipped): %v", err)
		result.Actual = pattern
		return result
	}

	// If pattern matches, we're done
	if regex.MatchString(strValue) {
		result.Passed = true
		return result
	}

	// Pattern didn't match - check if it's double-escaped
	// Common regex escape sequences that might be double-escaped in JSON:
	// \\d, \\s, \\w, \\D, \\S, \\W, \\n, \\t, \\r, \\f, \\v
	fixedPattern := fixDoubleEscapedPattern(pattern)
	if fixedPattern != pattern {
		// Try with the fixed pattern
		fixedRegex, err := regexp.Compile(fixedPattern)
		if err == nil && fixedRegex.MatchString(strValue) {
			// Fixed pattern works! Use it
			result.Passed = true
			result.Expected = fixedPattern // Update expected to show the fixed pattern
			return result
		}
	}

	// Pattern doesn't match even after fixing
	result.ErrorMsg = fmt.Sprintf("String '%s' does not match pattern '%s'", strValue, pattern)
	result.Actual = strValue
	return result
}

// fixDoubleEscapedPattern fixes common double-escaped regex sequences
// This handles cases where JSON contains "\\\\d" which unmarshals to "\\d" (literal backslash + d)
// but should be "\d" (digit class)
func fixDoubleEscapedPattern(pattern string) string {
	// List of common regex escape sequences that might be double-escaped
	// Format: double-escaped -> single-escaped
	replacements := map[string]string{
		"\\\\d":  "\\d",  // digit class
		"\\\\s":  "\\s",  // whitespace
		"\\\\w":  "\\w",  // word character
		"\\\\D":  "\\D",  // non-digit
		"\\\\S":  "\\S",  // non-whitespace
		"\\\\W":  "\\W",  // non-word
		"\\\\n":  "\\n",  // newline
		"\\\\t":  "\\t",  // tab
		"\\\\r":  "\\r",  // carriage return
		"\\\\f":  "\\f",  // form feed
		"\\\\v":  "\\v",  // vertical tab
		"\\\\b":  "\\b",  // word boundary
		"\\\\B":  "\\B",  // non-word boundary
		"\\\\A":  "\\A",  // start of string
		"\\\\Z":  "\\Z",  // end of string
		"\\\\z":  "\\z",  // end of string
		"\\\\G":  "\\G",  // start of match
		"\\\\.":  "\\.",  // escaped dot
		"\\\\(":  "\\(",  // open paren
		"\\\\)":  "\\)",  // close paren
		"\\\\[":  "\\[",  // open bracket
		"\\\\]":  "\\]",  // close bracket
		"\\\\{":  "\\{",  // open brace
		"\\\\}":  "\\}",  // close brace
		"\\\\*":  "\\*",  // star
		"\\\\+":  "\\+",  // plus
		"\\\\?":  "\\?",  // question mark
		"\\\\|":  "\\|",  // pipe
		"\\\\^":  "\\^",  // caret
		"\\\\$":  "\\$",  // dollar sign
		"\\\\\"": "\\\"", // double quote
		"\\\\'":  "\\'",  // single quote
		"\\\\/":  "/",    // forward slash
	}

	fixed := pattern
	for doubleEscaped, singleEscaped := range replacements {
		// Only replace if the double-escaped sequence exists
		if strings.Contains(fixed, doubleEscaped) {
			fixed = strings.ReplaceAll(fixed, doubleEscaped, singleEscaped)
		}
	}

	return fixed
}

// validateConsistency validates consistency checks between fields
func validateConsistency(
	ctx context.Context,
	check JSONValidationCheck,
	jsonData interface{},
) JSONCheckResult {
	result := JSONCheckResult{
		Path:      check.Path,
		CheckType: "consistency",
		Passed:    false,
	}

	// Get the value at the current path
	currentValues, err := jsonpath.Get(check.Path, jsonData)
	if err != nil {
		result.ErrorMsg = fmt.Sprintf("Failed to get value at path %s: %v", check.Path, err)
		return result
	}

	var currentValue interface{}
	if valuesSlice, ok := currentValues.([]interface{}); ok {
		// For consistency checks, if we need the full array (like array_length), keep it
		// Otherwise, take the first element
		if check.ConsistencyCheck.Type == "array_length" {
			// For array_length, if Path points to an array, use its length
			// Otherwise, currentValue should be a number, so take first element
			currentValue = valuesSlice
		} else if len(valuesSlice) > 0 {
			currentValue = valuesSlice[0]
		} else {
			currentValue = valuesSlice
		}
	} else {
		currentValue = currentValues
	}

	// Validate comparison path before using it
	comparePath := strings.TrimSpace(check.ConsistencyCheck.CompareWithPath)
	if comparePath == "" {
		result.ErrorMsg = fmt.Sprintf("Consistency check requires a valid 'compare_with_path', but it is empty or whitespace. Check type: %s", check.ConsistencyCheck.Type)
		result.Expected = "non-empty JSONPath string"
		result.Actual = "empty or whitespace"
		return result
	}

	// Get the value at the comparison path
	compareValues, err := jsonpath.Get(comparePath, jsonData)
	if err != nil {
		result.ErrorMsg = fmt.Sprintf("Failed to get value at comparison path '%s': %v", comparePath, err)
		result.Expected = fmt.Sprintf("valid path that exists in JSON: %s", comparePath)
		result.Actual = fmt.Sprintf("path error: %v", err)
		return result
	}

	var compareValue interface{}
	if valuesSlice, ok := compareValues.([]interface{}); ok {
		// For array_length check, compareValue should be the full array
		// For other checks, take the first element
		if check.ConsistencyCheck.Type == "array_length" {
			compareValue = valuesSlice
		} else if len(valuesSlice) > 0 {
			compareValue = valuesSlice[0]
		} else {
			compareValue = valuesSlice
		}
	} else {
		compareValue = compareValues
	}

	// Perform consistency check based on type
	switch check.ConsistencyCheck.Type {
	case "equals":
		if fmt.Sprintf("%v", currentValue) != fmt.Sprintf("%v", compareValue) {
			result.ErrorMsg = fmt.Sprintf("Values do not match: %v != %v", currentValue, compareValue)
			result.Expected = fmt.Sprintf("equals %v", compareValue)
			result.Actual = fmt.Sprintf("%v", currentValue)
			return result
		}
	case "array_length":
		// Handle bidirectional array length check
		// Case 1: Path points to count (number), ComparePath points to array (standard)
		// Case 2: Path points to array, ComparePath points to count (number) (swapped but valid intent)

		var arrayLen int
		var countVal float64
		var matched bool

		// Check Case 1: Path=Number, Compare=Array
		if compareArray, ok := compareValue.([]interface{}); ok {
			if num, ok := getNumericValue(currentValue); ok {
				arrayLen = len(compareArray)
				countVal = num
				matched = true
			}
		}

		// Check Case 2: Path=Array, Compare=Number
		if !matched {
			if currentArray, ok := currentValue.([]interface{}); ok {
				if num, ok := getNumericValue(compareValue); ok {
					arrayLen = len(currentArray)
					countVal = num
					matched = true
				}
			}
		}

		if !matched {
			// Determine specific error message
			_, currentIsArray := currentValue.([]interface{})
			_, compareIsArray := compareValue.([]interface{})
			_, currentIsNum := getNumericValue(currentValue)
			_, compareIsNum := getNumericValue(compareValue)

			if currentIsArray && compareIsArray {
				result.ErrorMsg = fmt.Sprintf("Ambiguous array length check: Both %s and %s point to arrays. One must be a number (count).", check.Path, comparePath)
			} else if currentIsNum && compareIsNum {
				result.ErrorMsg = fmt.Sprintf("Ambiguous array length check: Both %s and %s point to numbers. One must be an array.", check.Path, comparePath)
			} else {
				result.ErrorMsg = fmt.Sprintf("Invalid array length check: Requires one array and one number. Got %T at %s and %T at %s", currentValue, check.Path, compareValue, comparePath)
			}
			result.Expected = "one array and one number"
			result.Actual = fmt.Sprintf("%T and %T", currentValue, compareValue)
			return result
		}

		if int(countVal) != arrayLen {
			result.ErrorMsg = fmt.Sprintf("Count %v does not match array length %d", countVal, arrayLen)
			result.Expected = fmt.Sprintf("count equals array length (%d)", arrayLen)
			result.Actual = fmt.Sprintf("%v", countVal)
			return result
		}
	case "greater_than":
		currentNum, ok1 := getNumericValue(currentValue)
		compareNum, ok2 := getNumericValue(compareValue)
		if !ok1 || !ok2 {
			result.ErrorMsg = "Greater than check requires numeric values"
			return result
		}
		if currentNum <= compareNum {
			result.ErrorMsg = fmt.Sprintf("Value %v is not greater than %v", currentNum, compareNum)
			result.Expected = fmt.Sprintf("> %v", compareNum)
			result.Actual = fmt.Sprintf("%v", currentNum)
			return result
		}
	case "less_than":
		currentNum, ok1 := getNumericValue(currentValue)
		compareNum, ok2 := getNumericValue(compareValue)
		if !ok1 || !ok2 {
			result.ErrorMsg = "Less than check requires numeric values"
			return result
		}
		if currentNum >= compareNum {
			result.ErrorMsg = fmt.Sprintf("Value %v is not less than %v", currentNum, compareNum)
			result.Expected = fmt.Sprintf("< %v", compareNum)
			result.Actual = fmt.Sprintf("%v", currentNum)
			return result
		}
	case "in_array":
		// Current value should be in the comparison array
		var compareArray []interface{}
		if arr, ok := compareValue.([]interface{}); ok {
			compareArray = arr
		} else {
			result.ErrorMsg = fmt.Sprintf("In array check requires array at %s, got %T", comparePath, compareValue)
			return result
		}

		found := false
		for _, item := range compareArray {
			if fmt.Sprintf("%v", item) == fmt.Sprintf("%v", currentValue) {
				found = true
				break
			}
		}

		if !found {
			result.ErrorMsg = fmt.Sprintf("Value %v not found in array", currentValue)
			result.Expected = "value in array"
			result.Actual = fmt.Sprintf("%v", currentValue)
			return result
		}
	default:
		result.ErrorMsg = fmt.Sprintf("Unknown consistency check type: %s", check.ConsistencyCheck.Type)
		return result
	}

	result.Passed = true
	return result
}

// getNumericValue extracts a numeric value from an interface{}
func getNumericValue(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	default:
		return 0, false
	}
}

// validateFilePath resolves a validation schema file path to either the step execution folder
// or the workflow root and rejects paths outside the current workflow workspace.
func validateFilePath(workspacePath string, fileName string) (string, error) {
	scopes := deriveValidationPathScopes(workspacePath)
	cleanPath, err := sanitizeValidationFileName(fileName)
	if err != nil {
		return "", err
	}

	// Absolute /app/workspace-docs paths are allowed only when they stay inside the current workflow.
	if filepath.IsAbs(cleanPath) {
		return resolveAbsoluteValidationPath(cleanPath, scopes)
	}

	// Allow callers that already pass a full workflow-relative or step-relative path.
	if cleanPath == scopes.WorkflowRootPath || strings.HasPrefix(cleanPath, scopes.WorkflowRootPath+"/") {
		return cleanPath, nil
	}
	if cleanPath == scopes.StepExecutionPath || strings.HasPrefix(cleanPath, scopes.StepExecutionPath+"/") {
		return cleanPath, nil
	}
	if strings.HasPrefix(cleanPath, "Workflow/") {
		return "", fmt.Errorf("path outside workflow workspace")
	}

	if isWorkflowRootValidationPath(cleanPath) {
		return filepath.Join(scopes.WorkflowRootPath, cleanPath), nil
	}

	return filepath.Join(scopes.StepExecutionPath, cleanPath), nil
}

func deriveValidationPathScopes(stepExecutionPath string) validationPathScopes {
	cleanStepPath := filepath.Clean(strings.TrimSpace(stepExecutionPath))
	workflowRootPath := cleanStepPath

	if idx := strings.Index(cleanStepPath, "/runs/"); idx > 0 {
		workflowRootPath = cleanStepPath[:idx]
	} else if idx := strings.Index(cleanStepPath, "/execution/"); idx > 0 {
		workflowRootPath = cleanStepPath[:idx]
	}

	return validationPathScopes{
		StepExecutionPath: cleanStepPath,
		WorkflowRootPath:  workflowRootPath,
	}
}

func sanitizeValidationFileName(fileName string) (string, error) {
	trimmed := strings.TrimSpace(fileName)
	if trimmed == "" {
		return "", fmt.Errorf("empty file path")
	}

	if !filepath.IsAbs(trimmed) && strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}

	return filepath.Clean(trimmed), nil
}

func resolveAbsoluteValidationPath(fileName string, scopes validationPathScopes) (string, error) {
	docsRoot := filepath.Clean(GetPromptDocsRoot())
	workflowRootAbs := filepath.Join(docsRoot, scopes.WorkflowRootPath)
	cleanAbsPath := filepath.Clean(fileName)

	if !strings.HasPrefix(cleanAbsPath, docsRoot+"/") && cleanAbsPath != docsRoot {
		return "", fmt.Errorf("absolute path outside workspace docs root")
	}
	if !strings.HasPrefix(cleanAbsPath, workflowRootAbs+"/") && cleanAbsPath != workflowRootAbs {
		return "", fmt.Errorf("absolute path outside workflow workspace")
	}

	return strings.TrimPrefix(cleanAbsPath, docsRoot+"/"), nil
}

// isDBSchemaPath reports whether a validation-schema file name targets the
// workflow-root db/ folder — either as a bare relative path (`db/foo.json`) or
// after absolute-path normalization. Used to scope the step-local fallback in
// validateFile: db/ is the only workflow-root folder a step is allowed to
// populate directly, so it is the only prefix where accepting a step-local
// copy is safe.
func isDBSchemaPath(fileName string) bool {
	cleanPath, err := sanitizeValidationFileName(fileName)
	if err != nil {
		return false
	}
	if filepath.IsAbs(cleanPath) {
		// Absolute paths carry the workflow root prefix; strip to the segment
		// after the workflow-root folder before checking.
		parts := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
		for i, p := range parts {
			if p == DBFolderName && i+1 < len(parts) {
				return true
			}
		}
		return false
	}
	return cleanPath == DBFolderName || strings.HasPrefix(cleanPath, DBFolderName+"/")
}

func isWorkflowRootValidationPath(fileName string) bool {
	topLevel := fileName
	if idx := strings.Index(fileName, "/"); idx >= 0 {
		topLevel = fileName[:idx]
	}

	switch topLevel {
	case "knowledgebase", "planning", "variables", "learnings", "memory", "output", "evaluation", "runs", "logs", "db", "soul":
		return true
	default:
		return false
	}
}

func deriveAlternateValidationPath(stepExecutionPath string, fileName string, resolvedPath string) string {
	scopes := deriveValidationPathScopes(stepExecutionPath)
	cleanPath, err := sanitizeValidationFileName(fileName)
	if err != nil {
		return ""
	}

	if filepath.IsAbs(cleanPath) {
		cleanPath, err = resolveAbsoluteValidationPath(cleanPath, scopes)
		if err != nil {
			return ""
		}
	}

	if strings.HasPrefix(resolvedPath, scopes.StepExecutionPath+"/") {
		rel := strings.TrimPrefix(resolvedPath, scopes.StepExecutionPath+"/")
		if rel != "" {
			return filepath.Join(scopes.WorkflowRootPath, rel)
		}
	}

	if strings.HasPrefix(resolvedPath, scopes.WorkflowRootPath+"/") {
		rel := strings.TrimPrefix(resolvedPath, scopes.WorkflowRootPath+"/")
		if rel != "" {
			return filepath.Join(scopes.StepExecutionPath, rel)
		}
	}

	if isWorkflowRootValidationPath(cleanPath) {
		return filepath.Join(scopes.StepExecutionPath, cleanPath)
	}

	return filepath.Join(scopes.WorkflowRootPath, cleanPath)
}

func annotateJSONCheckPathHints(result *FileCheckResult) {
	hint := buildValidationPathHint(result.ResolvedPath, result.AlternatePath, result.AlternateExists)
	if hint == "" {
		return
	}

	for i := range result.JSONChecks {
		check := &result.JSONChecks[i]
		if check.Passed || check.SchemaError || check.ErrorMsg == "" {
			continue
		}
		check.ErrorMsg += " " + hint
	}
}

func buildValidationPathHint(resolvedPath string, alternatePath string, alternateExists bool) string {
	if resolvedPath == "" {
		return ""
	}
	if alternatePath == "" {
		return fmt.Sprintf("(Validation checked %s.)", resolvedPath)
	}
	if alternateExists {
		return fmt.Sprintf("(Validation checked %s. Another copy also exists at %s. This often means execution wrote one copy while validation read the other.)", resolvedPath, alternatePath)
	}
	return fmt.Sprintf("(Validation checked %s. No alternate copy was found at %s.)", resolvedPath, alternatePath)
}

// formatWorkspaceResults formats the pre-validation results for the validation agent
func formatWorkspaceResults(results *WorkspaceVerificationResult) string {
	// Check if pre-validation was skipped (no checks performed)
	if results.Summary.TotalChecks == 0 && len(results.FilesChecked) == 0 {
		return `
⏭️ PRE-VALIDATION SKIPPED

No validation schema was provided for this step. Pre-validation was skipped.

Your task: Verify the execution history proves this output is authentic.
Focus on:
1. Did the agent actually read/process the claimed data sources?
2. Do tool calls in execution history match the output values?
3. Is the timeline consistent?
4. Are there any suspicious patterns (e.g., round numbers, fake data)?
`
	}

	if results.OverallPass {
		var output strings.Builder
		output.WriteString(fmt.Sprintf(`
✅ PRE-VALIDATION PASSED

Files Checked: %d
Checks Passed: %d/%d
`, len(results.FilesChecked), results.Summary.PassedChecks, results.Summary.TotalChecks))

		// Report schema warnings if any
		if results.Summary.SchemaErrors > 0 {
			output.WriteString(fmt.Sprintf("⚠️ Schema Warnings: %d (invalid patterns skipped, validation continued)\n", results.Summary.SchemaErrors))
			for _, warning := range results.Summary.SchemaWarnings {
				output.WriteString(fmt.Sprintf("   ⚠️ %s [%s]: %s\n", warning.File, warning.Path, warning.Message))
			}
		}

		output.WriteString(`
All structural checks passed.

Your task: Verify the execution history proves this output is authentic.
Focus on:
1. Did the agent actually read/process the claimed data sources?
2. Do tool calls in execution history match the output values?
3. Is the timeline consistent?
4. Are there any suspicious patterns (e.g., round numbers, fake data)?
`)
		return output.String()
	} else {
		var errDetails strings.Builder
		errDetails.WriteString("❌ PRE-VALIDATION FAILED\n\n")
		errDetails.WriteString(fmt.Sprintf("Checks: %d passed, %d failed\n",
			results.Summary.PassedChecks, results.Summary.FailedChecks))

		// Report schema warnings if any
		if results.Summary.SchemaErrors > 0 {
			errDetails.WriteString(fmt.Sprintf("⚠️ Schema Warnings: %d (invalid patterns skipped)\n", results.Summary.SchemaErrors))
		}
		errDetails.WriteString("\n")

		for _, err := range results.Summary.Errors {
			errDetails.WriteString(fmt.Sprintf("❌ %s [%s]: %s\n",
				err.File, err.Path, err.Message))
			if err.Expected != "" {
				errDetails.WriteString(fmt.Sprintf("   Expected: %s, Actual: %s\n",
					err.Expected, err.Actual))
			}
		}

		// Show schema warnings separately
		if len(results.Summary.SchemaWarnings) > 0 {
			errDetails.WriteString("\n⚠️ Schema Warnings (non-blocking):\n")
			for _, warning := range results.Summary.SchemaWarnings {
				errDetails.WriteString(fmt.Sprintf("   ⚠️ %s [%s]: %s\n",
					warning.File, warning.Path, warning.Message))
			}
		}

		errDetails.WriteString("\nThe execution agent did not produce valid output structure.\n")
		errDetails.WriteString("Validation FAILED - structural issues must be fixed.\n")

		return errDetails.String()
	}
}
