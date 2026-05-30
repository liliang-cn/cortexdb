// pkg/importflow/source.go
package importflow

import "context"

// Source yields table schemas (for planning) and a stream of records (for import).
// Implementations should make Schemas() cheap enough to call before Records().
type Source interface {
	// Schemas returns table schemas with up to a few sample rows each.
	Schemas(ctx context.Context) ([]Schema, error)
	// Records streams every record; fn returning an error aborts iteration.
	Records(ctx context.Context, fn func(Record) error) error
	// Close releases any underlying resources.
	Close() error
}
