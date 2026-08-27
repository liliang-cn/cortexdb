package core

// PostgresStore must be able to back a brain, not merely hold vectors.
// The compiler enumerates what that takes; this is the line that makes it do so.
var _ BrainStore = (*PostgresStore)(nil)
