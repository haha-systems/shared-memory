package embeddings

import "context"

// Provider is reserved for v2 semantic ranking support.
type Provider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
