package cortexdb

import (
	"context"
	"time"
)

func (db *DB) recallLogOn() bool { return false }

func (db *DB) logRecall(context.Context, KnowledgeMemoryRecallRequest, *KnowledgeMemoryRecallResponse, *RecallExplain, time.Duration) {
}
