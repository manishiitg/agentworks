package llmtypes

import "context"

// EmbeddingModel is an interface for models that support embedding generation
// This is separate from the Model interface since not all models support embeddings
type EmbeddingModel interface {
	// GenerateEmbeddings generates embeddings for the given input texts
	// Input can be a single string or a slice of strings
	// Returns an EmbeddingResponse with embeddings and usage information
	GenerateEmbeddings(ctx context.Context, input interface{}, options ...EmbeddingOption) (*EmbeddingResponse, error)
}
