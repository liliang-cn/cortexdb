// Package hindsight: chat.go provides a thin wrapper around the sqvect
// session/message API that optionally auto-triggers fact extraction.
//
// Usage pattern:
//
//	// 1. Register extractor (once at startup)
//	sys.SetFactExtractor(myExtractor)
//
//	// 2. Enable auto-retain
//	sys.SetAutoRetain(&hindsight.AutoRetainConfig{
//	    Enabled:      true,
//	    WindowSize:   6,   // last 6 messages sent to extractor
//	    TriggerEvery: 2,   // fire after every 2 messages (one turn)
//	})
//
//	// 3. Use sys.AddMessage instead of db.Vector().AddMessage
//	sys.AddMessage(ctx, "bank-1", &core.Message{...})
//	// ↑ stores message normally + fires async extraction when threshold is met
package hindsight

import (
	"context"
	"log/slog"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// CreateSession is a convenience wrapper that creates a chat session in the
// underlying store. It is provided so callers that use hindsight as their
// primary entry-point don't need a separate reference to sqvect.DB.
func (s *System) CreateSession(ctx context.Context, session *core.Session) error {
	return s.store.CreateSession(ctx, session)
}

// AddMessage stores a chat message in the session history and — when auto-retain
// is enabled and the trigger threshold is reached — asynchronously extracts and
// persists facts into the memory bank identified by bankID.
//
// Behaviour:
//   - The message is always written synchronously; this call blocks only for the
//     database insert, not for extraction.
//   - Extraction is fired in a goroutine; errors are logged via slog and never
//     surfaced to the caller (they do not affect the message write).
//   - The trigger counter increments for every message whose Role matches
//     AutoRetainConfig.RoleFilter (empty = all roles).
//   - When the counter reaches TriggerEvery, extraction fires with the last
//     WindowSize messages from the same session, then the counter resets.
//
// If auto-retain is disabled or no FactExtractorFn is registered, this method
// is equivalent to db.Vector().AddMessage().
func (s *System) AddMessage(ctx context.Context, bankID string, msg *core.Message) error {
	// 1. Write the message (synchronous, always happens).
	if err := s.store.AddMessage(ctx, msg); err != nil {
		return err
	}

	// 2. Check whether auto-retain should fire.
	s.mu.Lock()
	cfg := s.autoRetainCfg
	extractor := s.factExtractor
	shouldFire := false

	if cfg.Enabled && extractor != nil && msg.SessionID != "" {
		if roleMatches(msg.Role, cfg.RoleFilter) {
			s.autoRetainCounter++
			if s.autoRetainCounter >= cfg.TriggerEvery {
				s.autoRetainCounter = 0
				shouldFire = true
			}
		}
	}
	s.mu.Unlock()

	if !shouldFire {
		return nil
	}

	// 3. Fire async extraction — does not block AddMessage's caller.
	windowSize := cfg.WindowSize
	sessionID := msg.SessionID

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		bgCtx := context.Background()
		// Retrieve the sliding window of recent messages.
		// GetSessionHistory returns messages in descending order (newest first).
		recent, err := s.store.GetSessionHistory(bgCtx, sessionID, windowSize)
		if err != nil {
			slog.Error("hindsight/auto-retain: fetch session history",
				"session_id", sessionID, "bank_id", bankID, "error", err)
			return
		}
		if len(recent) == 0 {
			return
		}
		// Reverse to chronological order before passing to extractor.
		reverseMessages(recent)

		result, err := s.RetainFromText(bgCtx, bankID, recent)
		if err != nil {
			slog.Error("hindsight/auto-retain: RetainFromText",
				"session_id", sessionID, "bank_id", bankID, "error", err)
			return
		}
		if result.Retained > 0 {
			slog.Debug("hindsight/auto-retain: retained facts",
				"session_id", sessionID, "bank_id", bankID,
				"retained", result.Retained, "skipped", result.Skipped)
		}
		if result.Err() != nil {
			slog.Warn("hindsight/auto-retain: partial extraction errors",
				"session_id", sessionID, "bank_id", bankID, "error", result.Err())
		}
	}()

	return nil
}

// roleMatches reports whether role should count toward the trigger.
// filter == nil or empty means every role matches.
func roleMatches(role string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, r := range filter {
		if r == role {
			return true
		}
	}
	return false
}

// reverseMessages reverses a slice of messages in-place.
func reverseMessages(msgs []*core.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
