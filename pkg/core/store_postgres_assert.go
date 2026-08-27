package core

// PostgresStore must satisfy Store, or "a second implementation" is a claim
// rather than a fact. A compile-time assertion says so at build time instead
// of at the call site.
var _ Store = (*PostgresStore)(nil)
