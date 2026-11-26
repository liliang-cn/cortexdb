package cli

// DeepSeek Harness (dsh) sessions.
//
// dsh keeps every session as an event log under $DSH_HOME (default ~/.dsh):
//
//	sessions/<project>/session-<uuid>/session.v<N>.jsonl.zstd
//
// One JSON event per line, Zstandard-compressed by default, and a new file per
// format generation (v4 today; an older generation can sit beside it). The
// first line is the session header; the conversation is the user/message and
// assistant/message events. A user/message is not always the person: dsh puts
// its runtime-context snapshot there, and plugins attach their own context
// (CortexDB's recall among them) — only source.kind "user" is someone typing.
//
// Capture and import read the log the way they read a Claude Code transcript
// or a Codex rollout, and write the same record, under auto:dsh-<id>:.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// dshLogName matches a session log: session.jsonl for generation zero,
// session.vN.jsonl after it, either one optionally .zstd.
var dshLogName = regexp.MustCompile(`^session(?:\.v(\d+))?\.jsonl(\.zstd)?$`)

func isDshSessionLog(path string) bool { return dshLogName.MatchString(filepath.Base(path)) }

// dshHome is where dsh keeps its data: $DSH_HOME, else ~/.dsh, as dsh itself
// resolves it.
func dshHome(home string) string {
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	return filepath.Join(home, ".dsh")
}

// dshSessionID is the bare id of a "session-<uuid>" session.
func dshSessionID(id string) string { return strings.TrimPrefix(id, "session-") }

// dshIDPrefix is capturedIDPrefix for a dsh session. Its own namespace keeps a
// dsh session from ever overwriting a Claude Code one that happens to share
// the first eight characters.
func dshIDPrefix(sessionID string) string {
	id := dshSessionID(sessionID)
	return "auto:dsh-" + id[:min(8, len(id))] + ":"
}

// listDshSessions finds dsh's session logs, the newest generation of each.
func listDshSessions(dshRoot string) []transcript {
	newest := map[string]struct {
		path    string
		version int
		info    fs.FileInfo
	}{}
	root := filepath.Join(dshRoot, "sessions")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// sessions/<project>/<session>/ — nothing deeper is a session log.
			if rel, rerr := filepath.Rel(root, p); rerr == nil && strings.Count(rel, string(filepath.Separator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		m := dshLogName.FindStringSubmatch(d.Name())
		if m == nil {
			return nil
		}
		version, _ := strconv.Atoi(m[1])
		dir := filepath.Dir(p)
		if cur, ok := newest[dir]; ok && cur.version >= version {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		newest[dir] = struct {
			path    string
			version int
			info    fs.FileInfo
		}{p, version, info}
		return nil
	})
	var out []transcript
	for dir, log := range newest {
		out = append(out, transcript{Kind: "dsh", ID: dshSessionID(filepath.Base(dir)), Path: log.path, Modified: log.info.ModTime()})
	}
	return out
}

// digestDshSession reduces a dsh session log to the conversation, the way
// digestTranscript does for Claude Code, and returns the session's id from its
// header.
//
// A session still open has a last compressed frame not yet written out. What
// decodes before it is the conversation so far, and is returned as such: the
// capture of a live session is a capture of what has been said.
func digestDshSession(path string) (digest string, userTurns int, sessionID string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, "", err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".zstd") {
		dec, derr := zstd.NewReader(f)
		if derr != nil {
			return "", 0, "", derr
		}
		defer dec.Close()
		r = dec
	}

	var b strings.Builder
	lines := 0
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
	for scanner.Scan() {
		lines++
		var event struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Data struct {
				Content json.RawMessage `json:"content"`
				Source  struct {
					Kind string `json:"kind"`
				} `json:"source"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			} `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Type {
		case "session":
			sessionID = event.ID
		case "user/message":
			if event.Data.Source.Kind != "user" {
				continue
			}
			text := stripTranscriptNoise(textFromContent(event.Data.Content))
			if text == "" {
				continue
			}
			userTurns++
			fmt.Fprintf(&b, "USER: %s\n", clip(text, 2000))
		case "assistant/message":
			text := strings.TrimSpace(scrubForCapture(textFromContent(event.Data.Message.Content)))
			if text == "" {
				continue
			}
			fmt.Fprintf(&b, "ASSISTANT: %s\n", clip(text, 1200))
		}
	}
	if serr := scanner.Err(); serr != nil && lines == 0 {
		return "", 0, "", serr
	}
	if sessionID == "" {
		sessionID = filepath.Base(filepath.Dir(path))
	}
	return budgetDigest(b.String()), userTurns, dshSessionID(sessionID), nil
}
