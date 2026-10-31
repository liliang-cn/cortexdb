package pgscan

import (
	"fmt"
	"testing"
)

// The setting is chosen without a database, so CI checks the rule even where
// no PostgreSQL is running; pkg/core and pkg/graph check what it does there.
func ef(n int) string {
	return fmt.Sprintf("SELECT set_config('hnsw.ef_search', GREATEST(COALESCE(NULLIF(current_setting('hnsw.ef_search', true), ''), '40')::int, %d)::text, true)", n)
}

func TestSetting(t *testing.T) {
	for _, tc := range []struct {
		limit    int
		filtered bool
		want     string
	}{
		{1, false, ef(40)},
		{40, false, ef(40)},
		{41, false, ef(41)},
		{1000, false, ef(1000)},
		{1001, false, "SET LOCAL enable_indexscan = off"},
		{0, false, "SET LOCAL enable_indexscan = off"},
		{10, true, "SET LOCAL enable_indexscan = off"},
		{0, true, "SET LOCAL enable_indexscan = off"},
	} {
		if got := Setting(tc.limit, tc.filtered); got != tc.want {
			t.Errorf("Setting(%d, %v) = %q, want %q", tc.limit, tc.filtered, got, tc.want)
		}
	}
}
