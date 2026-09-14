package core

// VectorAggregate on PostgreSQL.
//
// Everything interesting about this feature is in vector_aggregate.go — the
// three reductions, the grouping, the ordering guarantees. This file is the
// other half of the sentence: fetch the rows on this backend, hand them to the
// same code. It is short on purpose. A second centroid loop living here would
// be a divergence waiting to happen, and unlike a spelling difference in SQL
// it would never announce itself.

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// VectorAggregate reduces the matching vectors to one vector (or one member)
// per group. Same contract as the SQLite implementation, including which
// absences are errors and which are empty results.
func (s *PostgresStore) VectorAggregate(ctx context.Context, req VectorAggregateRequest) (*VectorAggregateResponse, error) {
	if err := validateVectorAggregateRequest(req); err != nil {
		return nil, wrapError("vector_aggregate", err)
	}

	// `vector::text` rather than the bare column: pgvector renders a vector as
	// [1,2,3] in text, which is the one format both directions of this driver
	// agree on without a pgvector-specific Go type. pgParseVector reads it
	// back, exactly as the other reads in this backend do.
	query, args := buildVectorAggregateQuery(sqldialect.For(sqldialect.Postgres), "vector::text", req)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError("vector_aggregate", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var fetched []vectorAggregateRow
	for rows.Next() {
		var (
			id         string
			vectorText string
			metadata   []byte
			content    string
		)
		if err := rows.Scan(&id, &vectorText, &metadata, &content); err != nil {
			return nil, wrapError("vector_aggregate", fmt.Errorf("failed to scan row: %w", err))
		}
		// Fatal, not skipped, and for the same reason as on SQLite: a mean
		// computed over a silently smaller set is wrong rather than merely
		// incomplete.
		vec, err := pgParseVector(vectorText)
		if err != nil {
			return nil, wrapError("vector_aggregate", fmt.Errorf("failed to decode vector of %q: %w", id, err))
		}
		fetched = append(fetched, vectorAggregateRow{
			id:      id,
			vector:  vec,
			meta:    decodeAggregateMetadata(metadata),
			content: content,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError("vector_aggregate", fmt.Errorf("error iterating rows: %w", err))
	}

	resp, err := runVectorAggregate(req, fetched, s.GetSimilarityFunc())
	if err != nil {
		return nil, wrapError("vector_aggregate", err)
	}
	return resp, nil
}
