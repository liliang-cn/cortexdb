package hindsight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockExtractState holds extractor call tracking and a buffered signal channel.
type mockExtractState struct {
	mu       sync.Mutex
	calls    int
	received [][]*core.Message // copy of each call's message slice
	done     chan struct{}
	err      error // if non-nil, returned by extractor
}

// newMockState creates state that can signal up to cap fires.
func newMockState(cap int) *mockExtractState {
	return &mockExtractState{done: make(chan struct{}, cap)}
}

// extractor is registered via sys.SetFactExtractor.
// It always returns a single dummy fact (so Retain is exercised).
func (m *mockExtractState) extractor(ctx context.Context, bankID string, msgs []*core.Message) ([]ExtractedFact, error) {
	m.mu.Lock()
	m.calls++
	callN := m.calls // capture under lock before unlock
	cp := make([]*core.Message, len(msgs))
	copy(cp, msgs)
	m.received = append(m.received, cp)
	m.mu.Unlock()
	select {
	case m.done <- struct{}{}:
	default:
	}
	if m.err != nil {
		return nil, m.err
	}
	return []ExtractedFact{
		{ID: fmt.Sprintf("fact-%d", callN), Content: "test", Vector: make([]float32, 64)},
	}, nil
}

// wait blocks until at least one fire signal is received or the test times out.
func (m *mockExtractState) wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(timeout):
		t.Error("timed out waiting for extractor to be called")
	}
}

// callCount returns the number of extractor calls so far.
func (m *mockExtractState) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// receivedAt returns the messages received in call index i (0-based).
func (m *mockExtractState) receivedAt(i int) []*core.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= len(m.received) {
		return nil
	}
	return m.received[i]
}

// newChatTestSystem creates a temporary System + bank + memories collection.
// Cleanup is registered on t.
func newChatTestSystem(t *testing.T, dim int) (*System, string) {
	t.Helper()
	dbPath := fmt.Sprintf("test_chat_%d.db", time.Now().UnixNano())
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	sys, err := New(&Config{DBPath: dbPath, VectorDim: dim})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = sys.Close() })

	ctx := context.Background()
	bankID := "test-bank"
	sys.CreateBank(ctx, NewBank(bankID, "Test Bank"))

	// Ensure the memories collection exists.
	// New() already creates it; this call is a no-op when the collection exists.
	_, _ = sys.store.CreateCollection(ctx, "memories", dim)
	return sys, bankID
}

// newTestSession creates a chat session and returns its ID.
func newTestSession(t *testing.T, sys *System, userID string) string {
	t.Helper()
	ctx := context.Background()
	sessionID := uuid.New().String()
	if err := sys.CreateSession(ctx, &core.Session{ID: sessionID, UserID: userID}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sessionID
}

// newMsg builds a Message with a fresh UUID.
func newMsg(sessionID, role, content string) *core.Message {
	return &core.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      role,
		Content:   content,
	}
}

// ---------------------------------------------------------------------------
// TestCreateSession
// ---------------------------------------------------------------------------

func TestCreateSession(t *testing.T) {
	sys, _ := newChatTestSystem(t, 64)
	ctx := context.Background()

	sessionID := uuid.New().String()
	if err := sys.CreateSession(ctx, &core.Session{ID: sessionID, UserID: "u1"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := sys.store.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != sessionID {
		t.Errorf("session ID: want %s, got %s", sessionID, got.ID)
	}
	if got.UserID != "u1" {
		t.Errorf("user ID: want u1, got %s", got.UserID)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_AutoRetainDisabled
// Extractor must never be called when auto-retain is off (the default).
// ---------------------------------------------------------------------------

func TestAddMessage_AutoRetainDisabled(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	mock := newMockState(1)
	sys.SetFactExtractor(mock.extractor)
	// auto-retain is disabled by default — no sys.SetAutoRetain call

	for i := 0; i < 6; i++ {
		if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}
	// Allow any spurious goroutine a chance to run.
	time.Sleep(50 * time.Millisecond)

	if n := mock.callCount(); n != 0 {
		t.Errorf("extractor must not be called when disabled; got %d calls", n)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_AutoRetainTrigger
// TriggerEvery=2: extractor fires once after the 2nd message and then resets.
// ---------------------------------------------------------------------------

func TestAddMessage_AutoRetainTrigger(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	mock := newMockState(2)
	sys.SetFactExtractor(mock.extractor)
	sys.SetAutoRetain(&AutoRetainConfig{Enabled: true, WindowSize: 6, TriggerEvery: 2})

	// First message: counter = 1 → no fire
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "hello")); err != nil {
		t.Fatalf("AddMessage 1: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := mock.callCount(); n != 0 {
		t.Errorf("extractor called too early: want 0 after 1 message, got %d", n)
	}

	// Second message: counter = 2 >= TriggerEvery → fire, counter resets to 0
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "assistant", "world")); err != nil {
		t.Fatalf("AddMessage 2: %v", err)
	}
	mock.wait(t, 2*time.Second)
	// Give the goroutine headroom to finish its DB writes before adding more.
	time.Sleep(200 * time.Millisecond)
	if n := mock.callCount(); n != 1 {
		t.Errorf("want 1 extractor call after 2 messages, got %d", n)
	}

	// Third message: counter = 1 again → no fire
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "again")); err != nil {
		t.Fatalf("AddMessage 3: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := mock.callCount(); n != 1 {
		t.Errorf("extractor should not fire after single message; got %d calls", n)
	}

	// Fourth message: counter hits 2 again → second fire
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "assistant", "ok")); err != nil {
		t.Fatalf("AddMessage 4: %v", err)
	}
	mock.wait(t, 2*time.Second)
	time.Sleep(200 * time.Millisecond)
	if n := mock.callCount(); n != 2 {
		t.Errorf("want 2 extractor calls after 4 messages, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_RoleFilter
// Only "user" messages increment the counter; assistant messages are ignored.
// ---------------------------------------------------------------------------

func TestAddMessage_RoleFilter(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	mock := newMockState(1)
	sys.SetFactExtractor(mock.extractor)
	sys.SetAutoRetain(&AutoRetainConfig{
		Enabled:      true,
		WindowSize:   6,
		TriggerEvery: 2,
		RoleFilter:   []string{"user"},
	})

	// Two assistant messages — must NOT increment counter
	for i := 0; i < 2; i++ {
		if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "assistant", fmt.Sprintf("a%d", i))); err != nil {
			t.Fatalf("AddMessage assistant %d: %v", i, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := mock.callCount(); n != 0 {
		t.Errorf("extractor must not fire for non-matching role; got %d calls", n)
	}

	// First user message: counter = 1 → no fire
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "q1")); err != nil {
		t.Fatalf("AddMessage user 1: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := mock.callCount(); n != 0 {
		t.Errorf("after 1 user message: want 0 calls, got %d", n)
	}

	// Second user message: counter = 2 → fire
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "q2")); err != nil {
		t.Fatalf("AddMessage user 2: %v", err)
	}
	mock.wait(t, 2*time.Second)
	if n := mock.callCount(); n != 1 {
		t.Errorf("want 1 call after 2 user messages, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_WindowSize
// Extractor must receive exactly WindowSize messages even when history is larger.
// ---------------------------------------------------------------------------

func TestAddMessage_WindowSize(t *testing.T) {
	const (
		dim    = 64
		window = 3
	)
	sys, bankID := newChatTestSystem(t, dim)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	// Populate 10 messages directly via the underlying store (no counter bump).
	for i := 0; i < 10; i++ {
		if err := sys.store.AddMessage(ctx, newMsg(sessionID, "user", fmt.Sprintf("old %d", i))); err != nil {
			t.Fatalf("store.AddMessage %d: %v", i, err)
		}
	}

	mock := newMockState(1)
	sys.SetFactExtractor(mock.extractor)
	sys.SetAutoRetain(&AutoRetainConfig{Enabled: true, WindowSize: window, TriggerEvery: 2})

	// Add 2 messages via sys.AddMessage → triggers extraction with window=3.
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "new 1")); err != nil {
		t.Fatalf("AddMessage 1: %v", err)
	}
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "new 2")); err != nil {
		t.Fatalf("AddMessage 2: %v", err)
	}
	mock.wait(t, 2*time.Second)

	msgs := mock.receivedAt(0)
	if msgs == nil {
		t.Fatal("extractor was not called")
	}
	if len(msgs) != window {
		t.Errorf("extractor received %d messages; want %d (window size)", len(msgs), window)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_ExtractorError
// An error from the extractor must not surface through AddMessage.
// ---------------------------------------------------------------------------

func TestAddMessage_ExtractorError(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	mock := newMockState(1)
	mock.err = errors.New("mock extractor failure")
	sys.SetFactExtractor(mock.extractor)
	sys.SetAutoRetain(&AutoRetainConfig{Enabled: true, WindowSize: 4, TriggerEvery: 2})

	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "m1")); err != nil {
		t.Fatalf("AddMessage 1: %v", err)
	}
	if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", "m2")); err != nil {
		t.Errorf("AddMessage must not propagate extractor error; got: %v", err)
	}
	mock.wait(t, 2*time.Second)

	if n := mock.callCount(); n != 1 {
		t.Errorf("want 1 extractor call, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// TestAddMessage_NoExtractor
// When no extractor is registered, AddMessage silently skips auto-retain.
// ---------------------------------------------------------------------------

func TestAddMessage_NoExtractor(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	// Enable auto-retain but register no extractor.
	sys.SetAutoRetain(&AutoRetainConfig{Enabled: true, WindowSize: 4, TriggerEvery: 2})

	for i := 0; i < 4; i++ {
		if err := sys.AddMessage(ctx, bankID, newMsg(sessionID, "user", fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("AddMessage %d should succeed even without extractor: %v", i, err)
		}
	}
	// No panic, no error, no goroutine should be launched.
	time.Sleep(50 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// TestAddMessage_MessageStoredAlways
// Messages must be persisted even when auto-retain is off.
// ---------------------------------------------------------------------------

func TestAddMessage_MessageStoredAlways(t *testing.T) {
	sys, bankID := newChatTestSystem(t, 64)
	sessionID := newTestSession(t, sys, "u1")
	ctx := context.Background()

	msg := newMsg(sessionID, "user", "hello world")
	if err := sys.AddMessage(ctx, bankID, msg); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	history, err := sys.store.GetSessionHistory(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("GetSessionHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 message in history, got %d", len(history))
	}
	if history[0].Content != "hello world" {
		t.Errorf("content: want %q, got %q", "hello world", history[0].Content)
	}
}
