package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A hit the session was already given is not given again.
//
// The hook runs before every prompt and the brain answers each one on its own,
// so whatever matches the project matches every prompt of a session about that
// project: on the shared brain ten documents were 42% of all knowledge hits,
// the same guide injected into dozens of consecutive prompts. The agent still
// has the first copy in its context; the repeats cost tokens and bury the one
// new hit among five old ones.
//
// What was injected is kept per session, in a small file under the user's
// cache directory. A hit comes back after recallRepeatAfter prompts: by then
// the first copy may have been compacted out of the conversation.

// recallRepeatAfter is how many prompts pass before a hit may be injected
// again in the same session.
const recallRepeatAfter = 20

// recallSeenKeep is how long a session's record outlives its last prompt.
const recallSeenKeep = 7 * 24 * time.Hour

type recallSeen struct {
	// Prompts counts the prompts this session has recalled for.
	Prompts int `json:"prompts"`
	// At is the prompt count at which each hit was last injected.
	At map[string]int `json:"at"`
}

// recallSeenDir is where the per-session records live; "" when the machine
// has no cache directory, and then nothing is remembered.
var recallSeenDir = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cortexdb", "recall-seen")
}

func recallSeenPath(sessionID string) string {
	dir := recallSeenDir()
	if dir == "" || sessionID == "" {
		return ""
	}
	// The id is a name somebody else chose: hashed, it cannot leave the directory.
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dir, hex.EncodeToString(sum[:12])+".json")
}

// unseenHits drops the hits this session was given within the last
// recallRepeatAfter prompts and records the ones it keeps. Without a session
// id, or when the record cannot be read or written, every hit is kept: a hook
// that cannot remember repeats itself, it does not go quiet.
func unseenHits(sessionID string, hits []recallHit) []recallHit {
	path := recallSeenPath(sessionID)
	if path == "" {
		return hits
	}
	seen := recallSeen{At: map[string]int{}}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &seen) != nil || seen.At == nil {
			seen = recallSeen{At: map[string]int{}}
		}
	}
	seen.Prompts++
	kept := hits[:0:0]
	for _, hit := range hits {
		if hit.KnowledgeID == "" {
			kept = append(kept, hit)
			continue
		}
		if at, ok := seen.At[hit.KnowledgeID]; ok && seen.Prompts-at < recallRepeatAfter {
			continue
		}
		seen.At[hit.KnowledgeID] = seen.Prompts
		kept = append(kept, hit)
	}
	for id, at := range seen.At {
		if seen.Prompts-at >= recallRepeatAfter {
			delete(seen.At, id)
		}
	}
	if raw, err := json.Marshal(seen); err == nil {
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			tmp := path + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	pruneRecallSeen(filepath.Dir(path))
	return kept
}

// pruneRecallSeen removes the records of sessions long over.
func pruneRecallSeen(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-recallSeenKeep)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
