package memoryflow

import (
	"context"
)

// PassthroughRecallStrategy is the default recall strategy. It delegates to
// the next recall function unchanged.
type PassthroughRecallStrategy struct{}

// Recall delegates unchanged.
func (PassthroughRecallStrategy) Recall(ctx context.Context, req RecallRequest, next RecallFunc) (*RecallResponse, error) {
	return next(ctx, req)
}
