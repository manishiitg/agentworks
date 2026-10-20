package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// hashStepDescription returns a hex SHA256 of the step description, trimmed of
// surrounding whitespace. It is used for observability so builder review tools
// can see whether successful runs happened against a stable step description.
func hashStepDescription(desc string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(desc)))
	return hex.EncodeToString(sum[:])
}

// DetectionHistoryEntry represents a single learning detection result
type DetectionHistoryEntry struct {
	Iteration      int     `json:"iteration"`
	Timestamp      string  `json:"timestamp"`
	HasNewLearning bool    `json:"has_new_learning"`
	Reasoning      string  `json:"reasoning"`
	Confidence     float64 `json:"confidence"`
}

// LearningMetadata represents the learning metadata stored per step
type LearningMetadata struct {
	StepID              string `json:"step_id"`
	StepPath            string `json:"step_path"`
	LearningContentHash string `json:"learning_content_hash,omitempty"` // Deprecated historical field; retained for older metadata. Learning content never selects the execution tier.
	TotalIterations     int    `json:"total_iterations"`
	SuccessfulRuns      int    `json:"successful_runs"`                 // Total count of successful runs (all description versions, observability only)
	LastDescriptionHash string `json:"last_description_hash,omitempty"` // SHA256 of step.GetDescription(); resets DescriptionHashRuns when it changes
	DescriptionHashRuns int    `json:"description_hash_runs,omitempty"` // Successful runs accumulated under LastDescriptionHash.
	FailureLearningRuns int    `json:"failure_learning_runs"`           // Count of failure learning runs (persisted across iterations)
	LastTurnCount       int    `json:"last_turn_count"`                 // Last recorded TurnCount
	LastExecutionLLM    string `json:"last_execution_llm,omitempty"`
	LastLearningLLM     string `json:"last_learning_llm,omitempty"`
	// Detection tracking
	LastLearningDetectedAt  string                  `json:"last_learning_detected_at,omitempty"`
	LastDetectionReasoning  string                  `json:"last_detection_reasoning,omitempty"`
	LastDetectionConfidence float64                 `json:"last_detection_confidence,omitempty"`
	DetectionHistory        []DetectionHistoryEntry `json:"detection_history,omitempty"`
	// Legacy adaptive tier history, retained for backwards-compatible metadata reads.
	// These fields are not used to select or change a model; Architecture owns tier review.
	// Validation failures stay on the selected tier and are surfaced to Pulse as
	// concerns rather than silently changing model allocation.
	PreferredExecutionTier              string `json:"preferred_execution_tier,omitempty"`                 // "high" | "medium"
	LastExecutionTier                   string `json:"last_execution_tier,omitempty"`                      // Last execution tier actually used ("high" | "medium")
	MediumSuccessStreak                 int    `json:"medium_success_streak,omitempty"`                    // Consecutive successful executions on Medium
	HighSuccessStreakSinceMediumFailure int    `json:"high_success_streak_since_medium_failure,omitempty"` // Deprecated compatibility field; cleared on read
	LastMediumFailureAt                 string `json:"last_medium_failure_at,omitempty"`                   // Deprecated compatibility field; cleared on read
	LastTierDecisionReason              string `json:"last_tier_decision_reason,omitempty"`                // Human-readable reason for current preference
	LastTierChangeAt                    string `json:"last_tier_change_at,omitempty"`                      // Timestamp when PreferredExecutionTier last changed
	// Global learning: per-step contribution tracking (only used when StepID == "_global")
	// Maps step ID -> number of times that step has contributed to the global skill
	StepContributions map[string]int `json:"step_contributions,omitempty"`
}

// getLearningsBasePath returns the learnings base path.
func (hcpo *StepBasedWorkflowOrchestrator) getLearningsBasePath() string {
	return "learnings"
}

// GetLearningMetadata reads and returns the learning metadata for a given step
func (hcpo *StepBasedWorkflowOrchestrator) GetLearningMetadata(
	ctx context.Context,
	learningPathIdentifier string,
) (*LearningMetadata, error) {
	// Use relative path - ReadWorkspaceFile auto-prepends workspacePath
	learningsBase := hcpo.getLearningsBasePath()
	metadataPath := filepath.Join(learningsBase, learningPathIdentifier, ".learning_metadata.json")

	content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, metadataPath)
	if err != nil {
		return nil, err // Return error if file doesn't exist or can't be read
	}

	var metadata LearningMetadata
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse learning metadata: %w", err)
	}

	return &metadata, nil
}

// learningArtifactChange classifies a learning turn from the managed artifact
// tree itself. Agent prose is not evidence of a write: a turn can mention files
// changed by a KB/DB operation while leaving learnings byte-identical.
func learningArtifactChange(beforeRef, afterRef string) (bool, string, float64) {
	if beforeRef != afterRef {
		return true, "managed learning artifact tree changed", 1
	}
	return false, "managed learning artifact tree was byte-identical", 1
}

func refreshLearningMetadataIdentity(metadata *LearningMetadata, stepID, stepPath string) {
	metadata.StepID = stepID
	metadata.StepPath = stepPath
}

// updateLearningMetadataWithTurnCount updates the learning metadata with TurnCount-based complexity tracking.
// This is the implementation of the "TurnCount Tracker" described in learnings_architecture.md.
func (hcpo *StepBasedWorkflowOrchestrator) updateLearningMetadataWithTurnCount(
	ctx context.Context,
	stepIndex int,
	stepPath string,
	learningPathIdentifier string,
	hasNewLearning bool,
	reasoning string,
	confidence float64,
	turnCount int,
	step PlanStepInterface,
	validationPassed bool,
	executionLLM string,
	learningLLM string,
) error {
	// Use relative path - ReadWorkspaceFile/WriteWorkspaceFile auto-prepend workspacePath
	learningsBase := hcpo.getLearningsBasePath()
	metadataPath := filepath.Join(learningsBase, learningPathIdentifier, ".learning_metadata.json")

	// Read existing metadata or create new
	var metadata LearningMetadata
	content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, metadataPath)
	if err != nil {
		// Metadata doesn't exist - create new
		metadata = LearningMetadata{
			StepID:          learningPathIdentifier,
			StepPath:        stepPath,
			TotalIterations: 0,
		}
	} else {
		// Parse existing metadata
		if err := json.Unmarshal([]byte(content), &metadata); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to parse learning metadata: %v (creating new)", err))
			metadata = LearningMetadata{
				StepID:          learningPathIdentifier,
				StepPath:        stepPath,
				TotalIterations: 0,
			}
		}
	}

	// Initialize slices if nil
	if metadata.DetectionHistory == nil {
		metadata.DetectionHistory = []DetectionHistoryEntry{}
	}

	// Update common fields
	// Step IDs are stable, but step_path is an execution position and changes
	// whenever the plan is reordered. Refresh both on every write rather than
	// preserving creation-time identity forever.
	refreshLearningMetadataIdentity(&metadata, learningPathIdentifier, stepPath)
	metadata.TotalIterations++
	metadata.LastTurnCount = turnCount
	metadata.LastExecutionLLM = executionLLM
	metadata.LastLearningLLM = learningLLM
	if hasNewLearning {
		metadata.LastLearningDetectedAt = time.Now().Format(time.RFC3339)
	}
	metadata.LastDetectionReasoning = reasoning
	metadata.LastDetectionConfidence = confidence

	// Increment successful run counter on successful validation
	if validationPassed && turnCount > 0 {
		metadata.SuccessfulRuns++
		// Sync successful run count to step_config.json so review/harden prompts can see run evidence.
		if step != nil {
			hcpo.syncSuccessfulRunsToStepConfig(ctx, step.GetID(), metadata.SuccessfulRuns)
		}

		// Description-hash-scoped successful-run counter. Runtime does not lock
		// learnings based on this; it is evidence for builder/user review.
		if step != nil {
			currentHash := hashStepDescription(step.GetDescription())
			if metadata.LastDescriptionHash != currentHash {
				if metadata.LastDescriptionHash != "" {
					hcpo.GetLogger().Info(fmt.Sprintf("🔁 Step description changed for %s (hash %s → %s) — resetting description_hash_runs", learningPathIdentifier, metadata.LastDescriptionHash[:8], currentHash[:8]))
				}
				metadata.LastDescriptionHash = currentHash
				metadata.DescriptionHashRuns = 0
			}
			metadata.DescriptionHashRuns++
		}
	}

	// Add detection result to history
	detectionEntry := DetectionHistoryEntry{
		Iteration:      metadata.TotalIterations,
		Timestamp:      time.Now().Format(time.RFC3339),
		HasNewLearning: hasNewLearning,
		Reasoning:      reasoning,
		Confidence:     confidence,
	}
	metadata.DetectionHistory = append(metadata.DetectionHistory, detectionEntry)

	// Limit history to last 50 entries
	if len(metadata.DetectionHistory) > 50 {
		metadata.DetectionHistory = metadata.DetectionHistory[len(metadata.DetectionHistory)-50:]
	}

	// Write updated metadata
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal learning metadata: %w", err)
	}

	if err := hcpo.BaseOrchestrator.WriteWorkspaceFile(ctx, metadataPath, string(metadataJSON)); err != nil {
		return fmt.Errorf("failed to write learning metadata: %w", err)
	}

	return nil
}

// syncSuccessfulRunsToStepConfig updates the successful_runs count in step_config.json
// so it's visible in step_config.json for the workflow builder.
func (hcpo *StepBasedWorkflowOrchestrator) syncSuccessfulRunsToStepConfig(ctx context.Context, stepID string, count int) {
	configs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read step configs for syncing successful_runs: %v", err))
		return
	}

	found := false
	for i := range configs {
		if configs[i].ID == stepID {
			if configs[i].AgentConfigs == nil {
				configs[i].AgentConfigs = &AgentConfigs{}
			}
			configs[i].AgentConfigs.SuccessfulRuns = &count
			found = true
			break
		}
	}

	if !found {
		newConfig := StepConfig{
			ID:           stepID,
			AgentConfigs: &AgentConfigs{SuccessfulRuns: &count},
		}
		configs = append(configs, newConfig)
	}

	if err := hcpo.WriteStepConfigs(ctx, configs); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to sync successful_runs to step_config.json: %v", err))
	}
}

// Note: savePreviousLearningsToMetadata has been removed
// Previous learnings are now read directly from files before the learning phase runs
// This avoids storing large content in metadata files
