package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"sync"
)

// What one call leaves behind for whoever logs it.
//
// A recall in the cortexdb-live mod once failed with "JSON Parse error:
// Unexpected identifier "Error"" while every line on the server and in the
// host's MCP log for that minute said the call succeeded. Nothing joined the
// client's failure to a server record. A Call is that join: an id the client
// sends (or the first server to see the call mints), carried through MCP and
// gRPC to the trace record and handed back in the response, plus the facts
// the request log can only learn after the handler has returned — who spoke,
// what a recall found and how much it sent back.

// RequestIDHeader is the gRPC metadata key and HTTP header that carry a
// call's id, both ways: a client may send one, and the server returns the
// id it used.
const RequestIDHeader = "x-request-id"

// MaxRequestIDBytes bounds an id a caller supplies. The id is written to the
// log before the caller is authenticated, so it is bounded in length and
// limited to a plain alphabet (see ValidRequestID).
const MaxRequestIDBytes = 64

// NewRequestID returns a fresh id: 16 random bytes, hex.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// ValidRequestID reports whether id may be adopted as a call's id: 1 to
// MaxRequestIDBytes of letters, digits, '.', '_', ':' and '-'. Anything else
// is replaced, not cleaned, so a log line never carries text a caller chose
// beyond that alphabet.
func ValidRequestID(id string) bool {
	if id == "" || len(id) > MaxRequestIDBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

// RecallNote is what a recall reports about itself to the request log:
// enough for the metrics (how many hits, whether it was degraded, what each
// floor dropped) and, as Explain, the full account of why it answered what
// it did. Explain is whatever the recall built; the log only marshals it.
type RecallNote struct {
	Tool         string
	Hits         int
	Degraded     []string
	FloorDropped map[string]int
	Explain      any
}

// Call is the record of one call in progress. The outermost layer that sees
// the call creates it (WithCall); layers below fill it in; the outermost
// reads it back when the call returns. A value travelling down a context
// never comes back up, which is why this is a holder and not a value.
type Call struct {
	id string

	mu     sync.Mutex
	key    string
	client ClientInfo
	recall *RecallNote
}

// ClientInfo is what a client says about where a call comes from. The MCP
// bridge fills it from its own machine; it is the caller's claim, not an
// identity — the key is the identity.
type ClientInfo struct {
	Host    string `json:"host,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

type callKey struct{}

// WithCall gives ctx a call record with id, or a fresh id when id is not
// ValidRequestID. An existing record in ctx is kept, so a call that already
// has an id (the MCP bridge made one, then forwarded it in process) keeps it.
func WithCall(ctx context.Context, id string) (context.Context, *Call) {
	if c := CallFrom(ctx); c != nil {
		return ctx, c
	}
	if !ValidRequestID(id) {
		id = NewRequestID()
	}
	c := &Call{id: id}
	return context.WithValue(ctx, callKey{}, c), c
}

// CallFrom returns the call record in ctx, or nil.
func CallFrom(ctx context.Context) *Call {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(callKey{}).(*Call)
	return c
}

// RequestID is the id of the call in ctx, or "".
func RequestID(ctx context.Context) string {
	if c := CallFrom(ctx); c != nil {
		return c.id
	}
	return ""
}

// ID is the call's request id.
func (c *Call) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// SetKey records the id of the key the call authenticated as.
func (c *Call) SetKey(keyID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.key = keyID
	c.mu.Unlock()
}

// Key is the id of the key the call authenticated as, or "".
func (c *Call) Key() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.key
}

// SetClient records what the client said about itself.
func (c *Call) SetClient(info ClientInfo) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.client = info
	c.mu.Unlock()
}

// Client is what the client said about itself.
func (c *Call) Client() ClientInfo {
	if c == nil {
		return ClientInfo{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// NoteRecall records what a recall found. Free when ctx has no call.
func NoteRecall(ctx context.Context, note RecallNote) {
	c := CallFrom(ctx)
	if c == nil {
		return
	}
	c.mu.Lock()
	c.recall = &note
	c.mu.Unlock()
}

// Recall is what the call's recall reported, or nil when it ran none.
func (c *Call) Recall() *RecallNote {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recall
}

// Client metadata keys: what an MCP bridge says about the machine and
// project a call comes from. Bounded like the request id when read.
const (
	ClientHostHeader    = "x-cortexdb-host"
	ClientProjectHeader = "x-cortexdb-project"
	ClientAgentHeader   = "x-cortexdb-agent"
	maxClientInfoBytes  = 128
)

// EncodeClientValue makes v safe to send as gRPC metadata or an HTTP
// header, which carry printable ASCII only: a project named in Chinese is
// escaped, not refused. DecodeClientValue reverses it.
func EncodeClientValue(v string) string { return url.PathEscape(CleanClientValue(v)) }

// DecodeClientValue reads a value EncodeClientValue wrote, bounded.
func DecodeClientValue(v string) string {
	if d, err := url.PathUnescape(v); err == nil {
		v = d
	}
	return CleanClientValue(v)
}

// CleanClientValue cuts v to a bounded single line, or "" when it holds
// nothing printable.
func CleanClientValue(v string) string {
	out := make([]rune, 0, len(v))
	n := 0
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			continue
		}
		n += len(string(r))
		if n > maxClientInfoBytes {
			break
		}
		out = append(out, r)
	}
	return string(out)
}
