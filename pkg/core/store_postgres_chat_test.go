package core

// What the PostgreSQL chat store has to agree with is chat.go, not a guess at
// what a chat store ought to do. Each test below is a sentence from that file's
// behaviour: the newest N messages come back oldest-first, a missing session is
// ErrNotFound, an exact vector match scores ~1.
//
// These run against a real PostgreSQL because the interesting parts — the FK
// cascade, `<=>`, the timestamp round trip — have no in-process stand-in. They
// live in their own schema: the same database serves other packages' tests, and
// a DROP TABLE aimed at "messages" would take theirs with it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// chatTestDSN puts the connection in its own schema, so nothing here can reach
// another package's tables and nothing there can reach these.
func chatTestDSN(t *testing.T, base, schema string) string {
	t.Helper()
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatalf("parse DSN: %v", err)
		}
		q := u.Query()
		// public stays on the path so the `vector` extension, which lives
		// there, is still resolvable.
		q.Set("search_path", schema+",public")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return base + " search_path=" + schema + ",public"
}

// openChatPG returns a PostgresStore initialised in a throwaway schema.
func openChatPG(t *testing.T, dim int) *PostgresStore {
	t.Helper()

	base := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if base == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — PostgreSQL sessions, messages and chat search are NOT covered by this run")
	}

	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer admin.Close()

	schema := fmt.Sprintf("chat_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// The extension is created by Init if it is missing; it belongs in public,
	// where every schema on this connection can see it.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create extension: %v", err)
	}

	db, err := sql.Open("pgx", chatTestDSN(t, base, schema))
	if err != nil {
		t.Fatalf("open scoped: %v", err)
	}
	cfg := DefaultConfig()
	cfg.VectorDim = dim
	store := NewPostgresStore(db, cfg)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		cleanup, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	return store
}

func TestPostgresSessionRoundTrips(t *testing.T) {
	store := openChatPG(t, 4)
	ctx := context.Background()

	want := &Session{
		ID:       "sess-round-trip",
		UserID:   "ada",
		Metadata: map[string]interface{}{"topic": "arithmetic", "turns": float64(3)},
	}
	if err := store.CreateSession(ctx, want); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != want.ID || got.UserID != want.UserID {
		t.Errorf("session = %+v, want id=%q user=%q", got, want.ID, want.UserID)
	}
	if got.Metadata["topic"] != "arithmetic" || got.Metadata["turns"] != float64(3) {
		t.Errorf("metadata = %+v, want it to survive the round trip", got.Metadata)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v / %v, want the database's clock", got.CreatedAt, got.UpdatedAt)
	}
}

// A session with no metadata is the common case, and json.Marshal(nil) writes
// the four bytes `null` — which must not come back as a decode error.
func TestPostgresSessionWithoutMetadata(t *testing.T) {
	store := openChatPG(t, 4)
	ctx := context.Background()

	if err := store.CreateSession(ctx, &Session{ID: "sess-bare", UserID: "grace"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := store.GetSession(ctx, "sess-bare")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Metadata) != 0 {
		t.Errorf("metadata = %+v, want empty", got.Metadata)
	}
}

// The one case where an empty result would be a lie.
func TestPostgresMissingSessionIsNotFound(t *testing.T) {
	store := openChatPG(t, 4)

	got, err := store.GetSession(context.Background(), "no-such-session")
	if err == nil {
		t.Fatalf("GetSession returned %+v and no error for a session that does not exist", got)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want it to wrap ErrNotFound so callers can test either backend the same way", err)
	}
	if got != nil {
		t.Errorf("session = %+v, want nil alongside the error", got)
	}
}

func TestPostgresSessionHistoryIsChronological(t *testing.T) {
	store := openChatPG(t, 4)
	ctx := context.Background()

	if err := store.CreateSession(ctx, &Session{ID: "sess-history", UserID: "ada"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	said := []string{"first", "second", "third", "fourth", "fifth"}
	for i, content := range said {
		msg := &Message{
			ID:        fmt.Sprintf("msg-%d", i),
			SessionID: "sess-history",
			Role:      "user",
			Content:   content,
			Metadata:  map[string]interface{}{"turn": float64(i)},
		}
		if err := store.AddMessage(ctx, msg); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
		// The timestamp is the caller's clock at microsecond resolution in the
		// column; a gap keeps the ordering about the code rather than about
		// how fast the loop ran.
		time.Sleep(2 * time.Millisecond)
	}

	got, err := store.GetSessionHistory(ctx, "sess-history", 10)
	if err != nil {
		t.Fatalf("GetSessionHistory: %v", err)
	}
	if len(got) != len(said) {
		t.Fatalf("got %d messages, want %d", len(got), len(said))
	}
	for i, want := range said {
		if got[i].Content != want {
			t.Fatalf("history = %v, want oldest first %v", contents(got), said)
		}
	}
	if got[0].Metadata["turn"] != float64(0) {
		t.Errorf("metadata = %+v, want it to survive the round trip", got[0].Metadata)
	}
	if got[0].Role != "user" || got[0].SessionID != "sess-history" {
		t.Errorf("message = %+v, want role and session id back", got[0])
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("created_at is zero, want the time AddMessage recorded")
	}
}

// The limit takes the newest N — and then still reads oldest-first, which is
// the part that is easy to get backwards.
func TestPostgresSessionHistoryLimitTakesTheNewest(t *testing.T) {
	store := openChatPG(t, 4)
	ctx := context.Background()

	if err := store.CreateSession(ctx, &Session{ID: "sess-limit", UserID: "ada"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i, content := range []string{"first", "second", "third", "fourth", "fifth"} {
		if err := store.AddMessage(ctx, &Message{
			ID:        fmt.Sprintf("lim-%d", i),
			SessionID: "sess-limit",
			Role:      "user",
			Content:   content,
		}); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	got, err := store.GetSessionHistory(ctx, "sess-limit", 2)
	if err != nil {
		t.Fatalf("GetSessionHistory: %v", err)
	}
	want := []string{"fourth", "fifth"}
	if len(got) != 2 || got[0].Content != want[0] || got[1].Content != want[1] {
		t.Errorf("history(limit 2) = %v, want %v", contents(got), want)
	}

	// A session nobody has written to is empty, not an error — the same
	// answer SQLite gives, and the reason GetSession exists to ask the other
	// question.
	empty, err := store.GetSessionHistory(ctx, "sess-that-never-existed", 10)
	if err != nil {
		t.Fatalf("GetSessionHistory on an unknown session: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("history = %v, want nothing", contents(empty))
	}
}

func TestPostgresChatSearchRanksBySimilarity(t *testing.T) {
	store := openChatPG(t, 4)
	ctx := context.Background()

	if err := store.CreateSession(ctx, &Session{ID: "sess-search", UserID: "ada"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Four points on a line, as in the parity suite: "nearest to [1,0,0,0]"
	// has one right answer.
	seed := []struct {
		id      string
		content string
		vec     []float32
	}{
		{"near", "nearest", []float32{1, 0, 0, 0}},
		{"close", "close by", []float32{0.9, 0.1, 0, 0}},
		{"middle", "halfway", []float32{0.5, 0.5, 0, 0}},
		{"far", "orthogonal", []float32{0, 1, 0, 0}},
	}
	for _, s := range seed {
		if err := store.AddMessage(ctx, &Message{
			ID: s.id, SessionID: "sess-search", Role: "assistant",
			Content: s.content, Vector: s.vec,
		}); err != nil {
			t.Fatalf("AddMessage %s: %v", s.id, err)
		}
	}
	// A message with no vector must not be findable by vector.
	if err := store.AddMessage(ctx, &Message{
		ID: "vectorless", SessionID: "sess-search", Role: "user", Content: "no embedding",
	}); err != nil {
		t.Fatalf("AddMessage vectorless: %v", err)
	}
	// Nor may a message from another session leak in.
	if err := store.CreateSession(ctx, &Session{ID: "sess-other", UserID: "grace"}); err != nil {
		t.Fatalf("CreateSession other: %v", err)
	}
	if err := store.AddMessage(ctx, &Message{
		ID: "other", SessionID: "sess-other", Role: "user",
		Content: "another session", Vector: []float32{1, 0, 0, 0},
	}); err != nil {
		t.Fatalf("AddMessage other: %v", err)
	}

	scored, err := store.SearchChatHistoryScored(ctx, []float32{1, 0, 0, 0}, "sess-search", 10)
	if err != nil {
		t.Fatalf("SearchChatHistoryScored: %v", err)
	}
	want := []string{"near", "close", "middle", "far"}
	if len(scored) != len(want) {
		t.Fatalf("got %d results, want %d (the vectorless message and the other session must be excluded)", len(scored), len(want))
	}
	for i, w := range want {
		if scored[i].Message.ID != w {
			ids := make([]string, len(scored))
			for j := range scored {
				ids[j] = scored[j].Message.ID
			}
			t.Fatalf("ranking = %v, want %v", ids, want)
		}
	}
	// The score has to mean what it means on SQLite, or a threshold tuned on
	// one backend is wrong on the other.
	if scored[0].Score < 0.99 {
		t.Errorf("an exact match scored %.4f — cosine similarity should be ~1", scored[0].Score)
	}
	if scored[len(scored)-1].Score > 0.01 {
		t.Errorf("an orthogonal vector scored %.4f — cosine similarity should be ~0", scored[len(scored)-1].Score)
	}
	for i := 1; i < len(scored); i++ {
		if scored[i].Score > scored[i-1].Score {
			t.Errorf("scores do not descend: %.4f then %.4f", scored[i-1].Score, scored[i].Score)
		}
	}
	// The vector must survive the round trip: callers re-rank on it.
	if len(scored[0].Message.Vector) != 4 || scored[0].Message.Vector[0] != 1 {
		t.Errorf("vector = %v, want [1 0 0 0] back", scored[0].Message.Vector)
	}

	// The limit applies after the ranking, not before it.
	top, err := store.SearchChatHistory(ctx, []float32{1, 0, 0, 0}, "sess-search", 2)
	if err != nil {
		t.Fatalf("SearchChatHistory: %v", err)
	}
	if len(top) != 2 || top[0].ID != "near" || top[1].ID != "close" {
		t.Errorf("top 2 = %v, want [near close]", ids(top))
	}

	// A limit of zero is nothing, not everything.
	none, err := store.SearchChatHistoryScored(ctx, []float32{1, 0, 0, 0}, "sess-search", 0)
	if err != nil {
		t.Fatalf("SearchChatHistoryScored(limit 0): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("limit 0 returned %d results", len(none))
	}
}

// The foreign key SQLite declares and enforces (foreign_keys=ON) has to be
// enforced here too, or a message can be written into a session that does not
// exist on one backend and not the other.
func TestPostgresMessageNeedsItsSession(t *testing.T) {
	store := openChatPG(t, 4)

	err := store.AddMessage(context.Background(), &Message{
		ID: "orphan", SessionID: "sess-not-created", Role: "user", Content: "hello?",
	})
	if err == nil {
		t.Error("AddMessage into a session that does not exist succeeded, want the foreign key to refuse it")
	}
}

func contents(msgs []*Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func ids(msgs []*Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}
