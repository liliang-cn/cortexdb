# Data Connector with Privacy/Desensitization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/connector` in CortexDB: connect to live Postgres/MySQL, classify PII (rules + LLM + human-signed plan), desensitize (column masking + free-text redaction + reversible token vault), and feed the result into `pkg/importflow` via a `Source` decorator — with connector tools on importflow's MCP.

**Architecture:** The privacy gate is a decorator over the existing `importflow.Source` interface: `connector.Desensitized(liveSource, desensitizer)` returns a `Source` whose `Schemas()` hides dropped columns and whose `Records()` masks values, so it drops transparently into `importflow.New(db).Run(...)`. Reversible values go to a separate AES-GCM SQLite vault keyed per tenant; the LLM/RAG path never reads the vault.

**Tech Stack:** Go 1.25, `database/sql` + `github.com/jackc/pgx/v5/stdlib` (Postgres) + `github.com/go-sql-driver/mysql` (both pure-Go, no CGO), `crypto/aes`+`crypto/cipher`+`crypto/hmac`, `modernc.org/sqlite` (vault), reuses `pkg/importflow` and `pkg/graphflow.JSONGenerator`.

**Spec:** `docs/superpowers/specs/2026-06-12-data-connector-privacy-design.md`

**Reused existing types** (do not redefine): `importflow.Source` (`Schemas(ctx)([]Schema,error)`, `Records(ctx, func(Record)error)error`, `Close()error`), `importflow.Schema{Table string; Columns []Column; Sample []Record}`, `importflow.Column{Name,Type string}`, `importflow.Record{Table string; Values map[string]string; Nulls map[string]bool; Row int}`, `graphflow.JSONGenerator{GenerateJSON(ctx, sys, user)([]byte,error)}`.

---

### Task 1: Package skeleton + core privacy types

**Files:**
- Create: `pkg/connector/types.go`
- Test: `pkg/connector/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"testing"
	"time"
)

func TestMaskingPlanSign(t *testing.T) {
	p := MaskingPlan{Columns: []ColumnRule{{Table: "users", Column: "phone", PiiKind: PiiPhone, Sensitivity: Confidential, Action: ActionMask}}}
	if p.IsSigned() {
		t.Fatal("new plan must be unsigned")
	}
	p.Sign("alice", time.Unix(1000, 0))
	if !p.IsSigned() || p.SignedBy != "alice" {
		t.Fatalf("sign failed: %+v", p)
	}
}

func TestRuleFor(t *testing.T) {
	p := MaskingPlan{Columns: []ColumnRule{{Table: "users", Column: "phone", Action: ActionMask}}}
	r, ok := p.RuleFor("users", "phone")
	if !ok || r.Action != ActionMask {
		t.Fatalf("RuleFor miss: %+v %v", r, ok)
	}
	if _, ok := p.RuleFor("users", "name"); ok {
		t.Fatal("unexpected rule for unlisted column")
	}
}
```

- [ ] **Step 2: Run, verify fail** — `go test ./pkg/connector` → FAIL (undefined types).

- [ ] **Step 3: Implement `types.go`**

```go
// Package connector turns live data sources into agent-usable knowledge with
// desensitization as a first-class step. It introspects a source's schema,
// classifies PII, applies a human-signed MaskingPlan, and feeds the
// desensitized records into pkg/importflow (RAG + knowledge graph).
package connector

import "time"

// PiiKind labels what kind of sensitive data a column or span holds.
type PiiKind string

const (
	PiiNone       PiiKind = ""
	PiiName       PiiKind = "name"
	PiiPhone      PiiKind = "phone"
	PiiEmail      PiiKind = "email"
	PiiNationalID PiiKind = "national_id"
	PiiBankCard   PiiKind = "bank_card"
	PiiAddress    PiiKind = "address"
	PiiDOB        PiiKind = "dob"
	PiiIP         PiiKind = "ip"
	PiiGeo        PiiKind = "geo"
	PiiCustom     PiiKind = "custom"
)

// Sensitivity is an ordered confidentiality level.
type Sensitivity int

const (
	Public Sensitivity = iota
	Internal
	Confidential
	Restricted
)

// MaskAction is what the desensitizer does to a classified column/span.
type MaskAction string

const (
	ActionDrop         MaskAction = "drop"         // never imported (removed from schema)
	ActionRedact       MaskAction = "redact"       // [REDACTED] (irreversible)
	ActionMask         MaskAction = "mask"         // partial: 138****1234
	ActionHash         MaskAction = "hash"         // deterministic one-way token (irreversible)
	ActionPseudonymize MaskAction = "pseudonymize" // reversible via vault
	ActionGeneralize   MaskAction = "generalize"   // 34 -> 30-40 (irreversible)
	ActionKeep         MaskAction = "keep"         // non-sensitive
)

// Reversible reports whether an action's original is recoverable from the vault.
func (a MaskAction) Reversible() bool { return a == ActionPseudonymize }

// ColumnRule is one column's classification + chosen action.
type ColumnRule struct {
	Table       string      `json:"table"`
	Column      string      `json:"column"`
	PiiKind     PiiKind     `json:"pii_kind"`
	Sensitivity Sensitivity `json:"sensitivity"`
	Action      MaskAction  `json:"action"`
	Reason      string      `json:"reason,omitempty"` // rule id / LLM note
	Source      string      `json:"source,omitempty"` // "rule" | "llm" | "human"
}

// TextScanRule marks a free-text column for in-place PII scanning.
type TextScanRule struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// MaskingPlan is the full, reviewable desensitization decision. Run refuses an
// unsigned plan (schema-first, data-second).
type MaskingPlan struct {
	Columns  []ColumnRule   `json:"columns"`
	TextScan []TextScanRule `json:"text_scan,omitempty"`
	SignedBy string         `json:"signed_by,omitempty"`
	SignedAt time.Time      `json:"signed_at,omitempty"`
}

// Sign marks the plan approved by a named reviewer.
func (p *MaskingPlan) Sign(by string, at time.Time) {
	p.SignedBy = by
	p.SignedAt = at
}

// IsSigned reports whether the plan has been approved.
func (p MaskingPlan) IsSigned() bool { return p.SignedBy != "" }

// RuleFor returns the rule for a table/column, if present.
func (p MaskingPlan) RuleFor(table, column string) (ColumnRule, bool) {
	for _, r := range p.Columns {
		if r.Table == table && r.Column == column {
			return r, true
		}
	}
	return ColumnRule{}, false
}

// TextScanFor reports whether a table/column should be free-text scanned.
func (p MaskingPlan) TextScanFor(table, column string) bool {
	for _, t := range p.TextScan {
		if t.Table == table && t.Column == column {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestMaskingPlan|TestRuleFor' -v`
- [ ] **Step 5: Commit** — `git add pkg/connector && git commit -m "feat(connector): core privacy types (PiiKind/Sensitivity/MaskAction/MaskingPlan)"`

---

### Task 2: Masking primitives (pure functions)

**Files:**
- Create: `pkg/connector/mask.go`
- Test: `pkg/connector/mask_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import "testing"

func TestMaskValue(t *testing.T) {
	cases := []struct{ kind PiiKind; in, want string }{
		{PiiPhone, "13812341234", "138****1234"},
		{PiiEmail, "alice@example.com", "a***@example.com"},
		{PiiName, "张三丰", "张**"},
		{PiiName, "Bob", "B**"},
		{PiiNationalID, "110101199003078888", "110101********8888"},
		{PiiBankCard, "6222021234567890", "6222********7890"},
		{PiiCustom, "secret", "******"},
	}
	for _, c := range cases {
		if got := MaskValue(c.kind, c.in); got != c.want {
			t.Errorf("MaskValue(%s,%q)=%q want %q", c.kind, c.in, got, c.want)
		}
	}
}

func TestGeneralize(t *testing.T) {
	if got := GeneralizeAge("34"); got != "30-40" {
		t.Errorf("age: %q", got)
	}
	if got := GeneralizeAge("7"); got != "0-10" {
		t.Errorf("age: %q", got)
	}
	if got := GeneralizeValue(PiiDOB, "1990-03-07"); got != "1990-03" {
		t.Errorf("dob: %q", got)
	}
}

func TestRedact(t *testing.T) {
	if Redact("anything") != "[REDACTED]" {
		t.Fatal("redact")
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `mask.go`**

```go
package connector

import (
	"strconv"
	"strings"
)

// Redact replaces a value with a fixed irreversible marker.
func Redact(string) string { return "[REDACTED]" }

// MaskValue partially hides a value based on its PII kind, keeping enough for
// humans to recognize but not enough to identify. Irreversible.
func MaskValue(kind PiiKind, v string) string {
	if v == "" {
		return ""
	}
	switch kind {
	case PiiPhone:
		return keepEnds(v, 3, 4)
	case PiiBankCard:
		return keepEnds(v, 4, 4)
	case PiiNationalID:
		return keepEnds(v, 6, 4)
	case PiiEmail:
		at := strings.IndexByte(v, '@')
		if at <= 1 {
			return "***" + v[max(at, 0):]
		}
		return v[:1] + "***" + v[at:]
	case PiiName:
		r := []rune(v)
		if len(r) <= 1 {
			return v
		}
		return string(r[:1]) + strings.Repeat("*", len(r)-1)
	default:
		return strings.Repeat("*", runeLen(v))
	}
}

// keepEnds keeps the first `head` and last `tail` runes, starring the middle.
// When the value is too short to keep both ends, everything is starred.
func keepEnds(v string, head, tail int) string {
	r := []rune(v)
	if len(r) <= head+tail {
		return strings.Repeat("*", len(r))
	}
	return string(r[:head]) + strings.Repeat("*", len(r)-head-tail) + string(r[len(r)-tail:])
}

// GeneralizeValue buckets a value to reduce identifiability. Irreversible.
func GeneralizeValue(kind PiiKind, v string) string {
	switch kind {
	case PiiDOB:
		// keep year-month, drop day: 1990-03-07 -> 1990-03
		if len(v) >= 7 {
			return v[:7]
		}
		return v
	default:
		return GeneralizeAge(v)
	}
}

// GeneralizeAge buckets an integer age into a decade band; non-ints pass through.
func GeneralizeAge(v string) string {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return v
	}
	lo := (n / 10) * 10
	return strconv.Itoa(lo) + "-" + strconv.Itoa(lo+10)
}

func runeLen(s string) int { return len([]rune(s)) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestMaskValue|TestGeneralize|TestRedact' -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): masking primitives (mask/generalize/redact)"`

---

### Task 3: Rule-based classifier

**Files:**
- Create: `pkg/connector/classify.go`
- Test: `pkg/connector/classify_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestRuleClassifier(t *testing.T) {
	c := NewRuleClassifier()
	ctx := context.Background()
	cases := []struct {
		col     string
		samples []string
		want    PiiKind
	}{
		{"phone", []string{"13812341234"}, PiiPhone},
		{"user_email", []string{"a@b.com"}, PiiEmail},
		{"id_card", []string{"110101199003078888"}, PiiNationalID},
		{"full_name", nil, PiiName},
		{"created_at", []string{"2026-01-01"}, PiiNone},
		{"notes", []string{"call me at 13812341234"}, PiiNone}, // free text: rule leaves kind none here
	}
	for _, tc := range cases {
		k, _, _ := c.Classify(ctx, importflow.Column{Name: tc.col}, tc.samples)
		if k != tc.want {
			t.Errorf("col %q -> %q want %q", tc.col, k, tc.want)
		}
	}
}

func TestRuleClassifierValueRegexBeatsName(t *testing.T) {
	c := NewRuleClassifier()
	// column name unhelpful, but values are clearly emails
	k, _, _ := c.Classify(context.Background(), importflow.Column{Name: "contact"}, []string{"a@b.com", "c@d.com"})
	if k != PiiEmail {
		t.Fatalf("value-regex classify failed: %q", k)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `classify.go`**

```go
package connector

import (
	"context"
	"regexp"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// Classifier proposes a PII kind + sensitivity for a column from its name, type,
// and a few SAMPLE values (never the full column).
type Classifier interface {
	Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string)
}

var (
	reEmail    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	rePhoneCN  = regexp.MustCompile(`^1[3-9]\d{9}$`)
	reNatIDCN  = regexp.MustCompile(`^\d{17}[\dXx]$`)
	reBankCard = regexp.MustCompile(`^\d{15,19}$`)
	reIP       = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
)

// nameHints maps substrings in a column name to a PII kind.
var nameHints = []struct {
	sub  string
	kind PiiKind
}{
	{"email", PiiEmail}, {"mail", PiiEmail},
	{"phone", PiiPhone}, {"mobile", PiiPhone}, {"tel", PiiPhone},
	{"id_card", PiiNationalID}, {"idcard", PiiNationalID}, {"national", PiiNationalID}, {"ssn", PiiNationalID},
	{"bank", PiiBankCard}, {"card_no", PiiBankCard}, {"cardno", PiiBankCard},
	{"name", PiiName},
	{"addr", PiiAddress}, {"address", PiiAddress},
	{"birth", PiiDOB}, {"dob", PiiDOB},
	{"ip", PiiIP},
}

// defaultSensitivity assigns a level per kind.
func defaultSensitivity(k PiiKind) Sensitivity {
	switch k {
	case PiiNationalID, PiiBankCard:
		return Restricted
	case PiiPhone, PiiEmail, PiiAddress, PiiDOB:
		return Confidential
	case PiiName, PiiIP, PiiGeo:
		return Internal
	default:
		return Public
	}
}

// RuleClassifier classifies by column-name hints, then by value regex.
type RuleClassifier struct{}

// NewRuleClassifier returns a deterministic, dependency-free classifier.
func NewRuleClassifier() *RuleClassifier { return &RuleClassifier{} }

// Classify implements Classifier. Value-regex evidence overrides a weak name guess.
func (c *RuleClassifier) Classify(_ context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	// 1) value regex (strongest signal — actual data shape)
	if k := classifyByValues(samples); k != PiiNone {
		return k, defaultSensitivity(k), "rule:value-regex"
	}
	// 2) column-name hint
	name := strings.ToLower(col.Name)
	for _, h := range nameHints {
		if strings.Contains(name, h.sub) {
			return h.kind, defaultSensitivity(h.kind), "rule:name:" + h.sub
		}
	}
	return PiiNone, Public, ""
}

// classifyByValues returns a kind only if a strong majority of non-empty samples
// match one pattern (avoids a single coincidental match).
func classifyByValues(samples []string) PiiKind {
	type counter struct {
		kind PiiKind
		re   *regexp.Regexp
	}
	counters := []counter{
		{PiiEmail, reEmail}, {PiiPhone, rePhoneCN}, {PiiNationalID, reNatIDCN}, {PiiIP, reIP}, {PiiBankCard, reBankCard},
	}
	nonEmpty := 0
	hits := map[PiiKind]int{}
	for _, s := range samples {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		nonEmpty++
		for _, c := range counters {
			if c.re.MatchString(s) {
				hits[c.kind]++
				break
			}
		}
	}
	if nonEmpty == 0 {
		return PiiNone
	}
	for _, c := range counters {
		if hits[c.kind]*2 > nonEmpty { // strict majority
			return c.kind
		}
	}
	return PiiNone
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestRuleClassifier -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): rule-based PII classifier (name hints + value regex)"`

---

### Task 4: Token vault (AES-GCM, separate SQLite, key provider)

**Files:**
- Create: `pkg/connector/vault.go`
- Test: `pkg/connector/vault_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"
)

func TestVaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	kp := StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef")) // 32 bytes
	v, err := OpenSQLiteVault(filepath.Join(dir, "t.vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	ctx := context.Background()

	tok1, err := v.Put(ctx, "tenantA", PiiName, "张三", kp)
	if err != nil {
		t.Fatal(err)
	}
	// deterministic: same (tenant,kind,value) -> same token
	tok2, _ := v.Put(ctx, "tenantA", PiiName, "张三", kp)
	if tok1 != tok2 {
		t.Fatalf("tokens not deterministic: %q vs %q", tok1, tok2)
	}
	// token reveals nothing
	if tok1 == "张三" || len(tok1) < 8 {
		t.Fatalf("bad token: %q", tok1)
	}
	// resolve with the right key
	got, err := v.Resolve(ctx, "tenantA", []string{tok1}, kp)
	if err != nil {
		t.Fatal(err)
	}
	if got[tok1] != "张三" {
		t.Fatalf("resolve: %v", got)
	}
}

func TestVaultWrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	good := StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef"))
	bad := StaticKeyProvider([]byte("ffffffffffffffffffffffffffffffff"))
	v, _ := OpenSQLiteVault(filepath.Join(dir, "t.vault.db"))
	defer v.Close()
	ctx := context.Background()
	tok, _ := v.Put(ctx, "t", PiiPhone, "13812341234", good)
	if _, err := v.Resolve(ctx, "t", []string{tok}, bad); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `vault.go`**

```go
package connector

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"

	_ "modernc.org/sqlite"
)

// KeyProvider supplies a per-tenant 32-byte key. Keys are never logged or stored
// in the knowledge DB.
type KeyProvider interface {
	TenantKey(ctx context.Context, tenant string) ([]byte, error)
}

// StaticKeyProvider returns a fixed key for every tenant (tests / single-tenant).
type StaticKeyProvider []byte

func (s StaticKeyProvider) TenantKey(context.Context, string) ([]byte, error) {
	if len(s) != 32 {
		return nil, fmt.Errorf("connector: key must be 32 bytes, got %d", len(s))
	}
	return s, nil
}

// Vault stores reversible original→token mappings outside the knowledge DB.
type Vault interface {
	Put(ctx context.Context, tenant string, kind PiiKind, original string, kp KeyProvider) (token string, err error)
	Resolve(ctx context.Context, tenant string, tokens []string, kp KeyProvider) (map[string]string, error)
	Close() error
}

// SQLiteVault is a Vault backed by its own SQLite file, separate from the
// CortexDB knowledge file. Values are AES-256-GCM ciphertext under the tenant key.
type SQLiteVault struct{ db *sql.DB }

// OpenSQLiteVault opens (creating if needed) a vault file.
func OpenSQLiteVault(path string) (*SQLiteVault, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vault (
		tenant TEXT NOT NULL, token TEXT NOT NULL, ciphertext BLOB NOT NULL,
		PRIMARY KEY (tenant, token))`); err != nil {
		return nil, err
	}
	return &SQLiteVault{db: db}, nil
}

func (v *SQLiteVault) Close() error { return v.db.Close() }

// token = "tok_" + hex(HMAC-SHA256(key, kind|original))[:24] — deterministic per
// (tenant-key, kind, value) so joins survive; reveals nothing without the key.
func deterministicToken(key []byte, kind PiiKind, original string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(string(kind)))
	m.Write([]byte{0})
	m.Write([]byte(original))
	return "tok_" + hex.EncodeToString(m.Sum(nil))[:24]
}

func (v *SQLiteVault) Put(ctx context.Context, tenant string, kind PiiKind, original string, kp KeyProvider) (string, error) {
	key, err := kp.TenantKey(ctx, tenant)
	if err != nil {
		return "", err
	}
	token := deterministicToken(key, kind, original)
	ct, err := sealAESGCM(key, []byte(original))
	if err != nil {
		return "", err
	}
	if _, err := v.db.ExecContext(ctx,
		`INSERT INTO vault (tenant, token, ciphertext) VALUES (?,?,?)
		 ON CONFLICT(tenant, token) DO NOTHING`, tenant, token, ct); err != nil {
		return "", err
	}
	return token, nil
}

func (v *SQLiteVault) Resolve(ctx context.Context, tenant string, tokens []string, kp KeyProvider) (map[string]string, error) {
	key, err := kp.TenantKey(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(tokens))
	for _, tok := range tokens {
		var ct []byte
		err := v.db.QueryRowContext(ctx, `SELECT ciphertext FROM vault WHERE tenant=? AND token=?`, tenant, tok).Scan(&ct)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		pt, err := openAESGCM(key, ct)
		if err != nil {
			return nil, fmt.Errorf("connector: un-mask failed for %s (wrong key?): %w", tok, err)
		}
		out[tok] = string(pt)
	}
	return out, nil
}

func sealAESGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func openAESGCM(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestVault -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): AES-GCM token vault with per-tenant key + deterministic tokens"`

---

### Task 5: Key providers (env + file)

**Files:**
- Create: `pkg/connector/keyprovider.go`
- Test: `pkg/connector/keyprovider_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileKeyProvider(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key")
	// 32 raw bytes
	if err := os.WriteFile(p, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	kp := FileKeyProvider{Path: p}
	k, err := kp.TenantKey(context.Background(), "any")
	if err != nil || len(k) != 32 {
		t.Fatalf("key: %v len=%d", err, len(k))
	}
}

func TestEnvKeyProvider(t *testing.T) {
	// 64 hex chars -> 32 bytes
	t.Setenv("CONNECTOR_KEY_tenantA", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	kp := EnvKeyProvider{Prefix: "CONNECTOR_KEY_"}
	k, err := kp.TenantKey(context.Background(), "tenantA")
	if err != nil || len(k) != 32 {
		t.Fatalf("env key: %v len=%d", err, len(k))
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `keyprovider.go`**

```go
package connector

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// FileKeyProvider reads a single key from a file (same key for all tenants).
// The file is either 32 raw bytes or 64 hex chars.
type FileKeyProvider struct{ Path string }

func (f FileKeyProvider) TenantKey(_ context.Context, _ string) ([]byte, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, err
	}
	return normalizeKey(raw)
}

// EnvKeyProvider reads a per-tenant key from <Prefix><tenant> (32 raw or 64 hex).
type EnvKeyProvider struct{ Prefix string }

func (e EnvKeyProvider) TenantKey(_ context.Context, tenant string) ([]byte, error) {
	v := os.Getenv(e.Prefix + tenant)
	if v == "" {
		return nil, fmt.Errorf("connector: no key in env %s%s", e.Prefix, tenant)
	}
	return normalizeKey([]byte(v))
}

func normalizeKey(raw []byte) ([]byte, error) {
	s := strings.TrimSpace(string(raw))
	if len(s) == 64 { // hex
		k, err := hex.DecodeString(s)
		if err == nil && len(k) == 32 {
			return k, nil
		}
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("connector: key must be 32 raw bytes or 64 hex chars, got %d", len(s))
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run KeyProvider -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): env + file key providers"`

---

### Task 6: Free-text PII scanner (regex layer)

**Files:**
- Create: `pkg/connector/textscan.go`
- Test: `pkg/connector/textscan_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"strings"
	"testing"
)

func TestTextScannerRedactsInPlace(t *testing.T) {
	s := NewTextScanner()
	in := "Call me at 13812341234 or alice@example.com; id 110101199003078888."
	out, hits := s.Scan(in)
	if strings.Contains(out, "13812341234") || strings.Contains(out, "alice@example.com") || strings.Contains(out, "110101199003078888") {
		t.Fatalf("PII leaked: %q", out)
	}
	if hits < 3 {
		t.Fatalf("expected >=3 hits, got %d", hits)
	}
	if !strings.Contains(out, "[REDACTED:phone]") {
		t.Fatalf("missing typed marker: %q", out)
	}
}

func TestTextScannerCleanTextUnchanged(t *testing.T) {
	s := NewTextScanner()
	in := "The customer was happy with the service."
	out, hits := s.Scan(in)
	if out != in || hits != 0 {
		t.Fatalf("clean text changed: %q hits=%d", out, hits)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `textscan.go`**

```go
package connector

import "regexp"

// TextScanner redacts PII embedded in free text, in place. Layer A is regex
// (high precision, deterministic). An optional LLM/NER layer is added later.
type TextScanner struct {
	patterns []textPattern
}

type textPattern struct {
	kind PiiKind
	re   *regexp.Regexp
}

// NewTextScanner returns the default regex-based scanner.
func NewTextScanner() *TextScanner {
	return &TextScanner{patterns: []textPattern{
		{PiiEmail, regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)},
		{PiiNationalID, regexp.MustCompile(`\b\d{17}[\dXx]\b`)},
		{PiiBankCard, regexp.MustCompile(`\b\d{15,19}\b`)},
		{PiiPhone, regexp.MustCompile(`\b1[3-9]\d{9}\b`)},
		{PiiIP, regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)},
	}}
}

// Scan returns the redacted text and the number of PII spans replaced. Order
// matters: national-id before bank-card before phone to avoid a longer id being
// partially eaten by the phone pattern.
func (s *TextScanner) Scan(text string) (string, int) {
	hits := 0
	for _, p := range s.patterns {
		text = p.re.ReplaceAllStringFunc(text, func(string) string {
			hits++
			return "[REDACTED:" + string(p.kind) + "]"
		})
	}
	return text, hits
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestTextScanner -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): regex free-text PII scanner (in-place redaction)"`

---

### Task 7: Desensitizer + Source decorator

**Files:**
- Create: `pkg/connector/desensitize.go`
- Test: `pkg/connector/desensitize_test.go`

- [ ] **Step 1: Write the failing test** (uses a fake in-memory `importflow.Source`)

```go
package connector

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

type fakeSource struct {
	schema  importflow.Schema
	records []importflow.Record
}

func (f *fakeSource) Schemas(context.Context) ([]importflow.Schema, error) {
	return []importflow.Schema{f.schema}, nil
}
func (f *fakeSource) Records(_ context.Context, fn func(importflow.Record) error) error {
	for _, r := range f.records {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeSource) Close() error { return nil }

func newFake() *fakeSource {
	return &fakeSource{
		schema: importflow.Schema{Table: "users", Columns: []importflow.Column{
			{Name: "id"}, {Name: "name"}, {Name: "phone"}, {Name: "ssn"}, {Name: "notes"},
		}},
		records: []importflow.Record{
			{Table: "users", Values: map[string]string{"id": "1", "name": "张三", "phone": "13812341234", "ssn": "110101199003078888", "notes": "vip, call 13900000000"}},
		},
	}
}

func signedPlan() MaskingPlan {
	p := MaskingPlan{
		Columns: []ColumnRule{
			{Table: "users", Column: "id", Action: ActionKeep},
			{Table: "users", Column: "name", PiiKind: PiiName, Action: ActionPseudonymize},
			{Table: "users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
			{Table: "users", Column: "ssn", PiiKind: PiiNationalID, Action: ActionDrop},
		},
		TextScan: []TextScanRule{{Table: "users", Column: "notes"}},
	}
	p.Sign("tester", time.Unix(1, 0))
	return p
}

func TestDesensitizedSchemaDropsColumns(t *testing.T) {
	d := mustDesensitizer(t, signedPlan())
	src := Desensitized(newFake(), d)
	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range schemas[0].Columns {
		if c.Name == "ssn" {
			t.Fatal("dropped column must not appear in schema")
		}
	}
}

func TestDesensitizedRecordsMasked(t *testing.T) {
	d := mustDesensitizer(t, signedPlan())
	src := Desensitized(newFake(), d)
	var got importflow.Record
	_ = src.Records(context.Background(), func(r importflow.Record) error { got = r; return nil })

	if _, ok := got.Values["ssn"]; ok {
		t.Fatal("dropped column leaked into record")
	}
	if got.Values["phone"] != "138****1234" {
		t.Fatalf("phone not masked: %q", got.Values["phone"])
	}
	if got.Values["name"] == "张三" || got.Values["name"] == "" {
		t.Fatalf("name not pseudonymized: %q", got.Values["name"])
	}
	if got.Values["notes"] == "" || contains(got.Values["notes"], "13900000000") {
		t.Fatalf("free-text PII not redacted: %q", got.Values["notes"])
	}
	if got.Values["id"] != "1" {
		t.Fatalf("kept column altered: %q", got.Values["id"])
	}
}

func TestDesensitizedRefusesUnsignedPlan(t *testing.T) {
	p := signedPlan()
	p.SignedBy = "" // unsign
	_, err := NewDesensitizer(p, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err == nil {
		t.Fatal("expected refusal of unsigned plan")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })() }
func indexOf(s, sub string) int { for i := 0; i+len(sub) <= len(s); i++ { if s[i:i+len(sub)] == sub { return i } }; return -1 }
```

- [ ] **Step 2: Add test helpers** at the bottom of `desensitize_test.go`:

```go
func testKP() KeyProvider { return StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef")) }
func testVault(t *testing.T) Vault {
	v, err := OpenSQLiteVault(t.TempDir() + "/v.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}
func mustDesensitizer(t *testing.T, p MaskingPlan) *Desensitizer {
	d, err := NewDesensitizer(p, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
```

- [ ] **Step 3: Run, verify fail.**

- [ ] **Step 4: Implement `desensitize.go`**

```go
package connector

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// DesensitizerOptions configures a Desensitizer.
type DesensitizerOptions struct {
	Tenant      string
	KeyProvider KeyProvider  // required when the plan has reversible actions
	Vault       Vault        // required when the plan has reversible actions
	TextScanner *TextScanner // defaults to NewTextScanner()
}

// Desensitizer applies a signed MaskingPlan to records.
type Desensitizer struct {
	plan MaskingPlan
	opts DesensitizerOptions
	ts   *TextScanner
}

// NewDesensitizer validates the plan (must be signed) and returns a Desensitizer.
func NewDesensitizer(plan MaskingPlan, opts DesensitizerOptions) (*Desensitizer, error) {
	if !plan.IsSigned() {
		return nil, fmt.Errorf("connector: refusing unsigned MaskingPlan (sign it after review)")
	}
	hasReversible := false
	for _, c := range plan.Columns {
		if c.Action.Reversible() {
			hasReversible = true
		}
	}
	if hasReversible && (opts.Vault == nil || opts.KeyProvider == nil) {
		return nil, fmt.Errorf("connector: plan has reversible actions but Vault/KeyProvider not set")
	}
	ts := opts.TextScanner
	if ts == nil {
		ts = NewTextScanner()
	}
	return &Desensitizer{plan: plan, opts: opts, ts: ts}, nil
}

// keptColumns returns a table's columns minus any with action drop.
func (d *Desensitizer) keptColumns(table string, cols []importflow.Column) []importflow.Column {
	out := make([]importflow.Column, 0, len(cols))
	for _, c := range cols {
		if r, ok := d.plan.RuleFor(table, c.Name); ok && r.Action == ActionDrop {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Apply desensitizes one record per the plan. Dropped columns are removed;
// reversible actions write to the vault and emit a token.
func (d *Desensitizer) Apply(ctx context.Context, r importflow.Record) (importflow.Record, error) {
	out := importflow.Record{Table: r.Table, Row: r.Row, Values: map[string]string{}, Nulls: map[string]bool{}}
	for col, val := range r.Values {
		if r.Nulls[col] {
			out.Nulls[col] = true
			continue
		}
		rule, ok := d.plan.RuleFor(r.Table, col)
		if !ok {
			// Unclassified but present: if it's a text-scan column, redact PII;
			// otherwise default-deny is enforced at plan-build time, so a missing
			// rule here means "keep" only for columns the plan explicitly knows.
			if d.plan.TextScanFor(r.Table, col) {
				masked, _ := d.ts.Scan(val)
				out.Values[col] = masked
			} else {
				out.Values[col] = val
			}
			continue
		}
		switch rule.Action {
		case ActionDrop:
			// omit entirely
		case ActionKeep:
			if d.plan.TextScanFor(r.Table, col) {
				masked, _ := d.ts.Scan(val)
				out.Values[col] = masked
			} else {
				out.Values[col] = val
			}
		case ActionRedact:
			out.Values[col] = Redact(val)
		case ActionMask:
			out.Values[col] = MaskValue(rule.PiiKind, val)
		case ActionGeneralize:
			out.Values[col] = GeneralizeValue(rule.PiiKind, val)
		case ActionHash:
			out.Values[col] = oneWayToken(rule.PiiKind, val)
		case ActionPseudonymize:
			tok, err := d.opts.Vault.Put(ctx, d.opts.Tenant, rule.PiiKind, val, d.opts.KeyProvider)
			if err != nil {
				return out, err
			}
			out.Values[col] = tok
		default:
			out.Values[col] = MaskValue(rule.PiiKind, val)
		}
	}
	return out, nil
}

// oneWayToken is an irreversible deterministic token (no vault entry).
func oneWayToken(kind PiiKind, v string) string {
	// reuse the vault's HMAC shape but with a fixed public salt so it is
	// deterministic yet not reversible (no key, no stored ciphertext).
	return deterministicToken([]byte("connector-oneway-salt-v1"), kind, v)
}

// desensitizedSource wraps a Source so Schemas() hides dropped columns and
// Records() are desensitized.
type desensitizedSource struct {
	inner importflow.Source
	d     *Desensitizer
}

// Desensitized returns a Source that applies d to inner. Drops it straight into
// importflow.New(db).Run(ctx, Desensitized(src, d), plan).
func Desensitized(inner importflow.Source, d *Desensitizer) importflow.Source {
	return &desensitizedSource{inner: inner, d: d}
}

func (s *desensitizedSource) Schemas(ctx context.Context) ([]importflow.Schema, error) {
	in, err := s.inner.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]importflow.Schema, len(in))
	for i, sc := range in {
		nsc := importflow.Schema{Table: sc.Table, Columns: s.d.keptColumns(sc.Table, sc.Columns)}
		for _, r := range sc.Sample {
			dr, err := s.d.Apply(ctx, r)
			if err != nil {
				return nil, err
			}
			nsc.Sample = append(nsc.Sample, dr)
		}
		out[i] = nsc
	}
	return out, nil
}

func (s *desensitizedSource) Records(ctx context.Context, fn func(importflow.Record) error) error {
	return s.inner.Records(ctx, func(r importflow.Record) error {
		dr, err := s.d.Apply(ctx, r)
		if err != nil {
			return err
		}
		return fn(dr)
	})
}

func (s *desensitizedSource) Close() error { return s.inner.Close() }
```

- [ ] **Step 5: Run, verify pass** — `go test ./pkg/connector -run TestDesensitized -v`
- [ ] **Step 6: Commit** — `git commit -am "feat(connector): desensitizer + Source decorator (mask/drop/pseudonymize/text-scan)"`

---

### Task 8: BuildMaskingPlan + Unmask (plan assembly with default-deny)

**Files:**
- Create: `pkg/connector/plan.go`
- Test: `pkg/connector/plan_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestBuildMaskingPlanDefaultDeny(t *testing.T) {
	src := &fakeSource{schema: importflow.Schema{Table: "users", Columns: []importflow.Column{
		{Name: "id"}, {Name: "phone"}, {Name: "mystery"},
	}, Sample: []importflow.Record{
		{Table: "users", Values: map[string]string{"id": "1", "phone": "13812341234", "mystery": "xyz"}},
	}}}
	plan, err := BuildMaskingPlan(context.Background(), src, NewRuleClassifier(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byCol := map[string]ColumnRule{}
	for _, r := range plan.Columns {
		byCol[r.Column] = r
	}
	if byCol["phone"].Action != ActionMask {
		t.Errorf("phone action: %q", byCol["phone"].Action)
	}
	if byCol["id"].Action != ActionKeep {
		t.Errorf("id should be keep: %q", byCol["id"].Action)
	}
	// "mystery": unclassified -> default-deny -> redact, never keep
	if byCol["mystery"].Action == ActionKeep {
		t.Errorf("default-deny violated: unclassified column kept")
	}
	if plan.IsSigned() {
		t.Error("freshly built plan must be unsigned")
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `plan.go`**

```go
package connector

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// PlanOptions tunes plan building.
type PlanOptions struct {
	// DefaultAction for unclassified columns. Default-deny: ActionRedact.
	DefaultAction MaskAction
	// ActionFor overrides the action chosen for a given PII kind.
	ActionFor map[PiiKind]MaskAction
	// TextKinds: columns of these types are also marked for free-text scanning.
	ScanTextColumns bool
}

// defaultActionFor maps a PII kind to its default desensitization action.
func defaultActionFor(k PiiKind) MaskAction {
	switch k {
	case PiiNone:
		return "" // decided by caller (keep vs default-deny)
	case PiiNationalID, PiiBankCard:
		return ActionDrop
	case PiiPhone, PiiEmail:
		return ActionMask
	case PiiDOB:
		return ActionGeneralize
	case PiiName:
		return ActionPseudonymize
	default:
		return ActionRedact
	}
}

// BuildMaskingPlan introspects the source schema (NOT bulk data), classifies
// each column, and proposes actions. The returned plan is UNSIGNED — the caller
// must review and Sign() it before Run. Default-deny: a column the classifier is
// unsure about gets DefaultAction (redact), never keep.
func BuildMaskingPlan(ctx context.Context, src importflow.Source, cls Classifier, opts PlanOptions) (MaskingPlan, error) {
	if opts.DefaultAction == "" {
		opts.DefaultAction = ActionRedact // default-deny
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		return MaskingPlan{}, err
	}
	var plan MaskingPlan
	for _, sc := range schemas {
		for _, col := range sc.Columns {
			samples := columnSamples(sc, col.Name)
			kind, sens, reason := cls.Classify(ctx, col, samples)
			action := chooseAction(kind, opts)
			plan.Columns = append(plan.Columns, ColumnRule{
				Table: sc.Table, Column: col.Name, PiiKind: kind, Sensitivity: sens,
				Action: action, Reason: reason, Source: "rule",
			})
			if opts.ScanTextColumns && col.Type == "text" {
				plan.TextScan = append(plan.TextScan, TextScanRule{Table: sc.Table, Column: col.Name})
			}
		}
	}
	return plan, nil
}

// chooseAction picks the action for a classified column, honoring overrides and
// default-deny for unclassified columns.
func chooseAction(kind PiiKind, opts PlanOptions) MaskAction {
	if kind == PiiNone {
		// Unclassified. A clearly-safe name (id/created_at) is keep; everything
		// else is default-deny. Conservative: only an explicit safe-list keeps.
		return opts.DefaultAction
	}
	if a, ok := opts.ActionFor[kind]; ok {
		return a
	}
	return defaultActionFor(kind)
}

func columnSamples(sc importflow.Schema, col string) []string {
	var out []string
	for _, r := range sc.Sample {
		if v, ok := r.Get(col); ok {
			out = append(out, v)
		}
	}
	return out
}

// Unmask reverses pseudonymized tokens back to originals via the vault. The only
// reverse path; audited by the vault implementation. Requires the tenant key.
func Unmask(ctx context.Context, v Vault, tenant string, tokens []string, kp KeyProvider) (map[string]string, error) {
	return v.Resolve(ctx, tenant, tokens, kp)
}
```

Note: the test asserts `id` → keep, but `chooseAction` returns `DefaultAction` (redact) for `PiiNone`. Resolve by adding a safe-name allowlist in `chooseAction`:

```go
// add near top of plan.go
var safeColumnNames = map[string]bool{
	"id": true, "uuid": true, "created_at": true, "updated_at": true,
	"created", "updated": true, "status": true, "type": true, "count": true,
}
```

and change `chooseAction` to take the column name:

```go
func chooseAction(name string, kind PiiKind, opts PlanOptions) MaskAction {
	if kind == PiiNone {
		if safeColumnNames[strings.ToLower(name)] {
			return ActionKeep
		}
		return opts.DefaultAction
	}
	if a, ok := opts.ActionFor[kind]; ok {
		return a
	}
	return defaultActionFor(kind)
}
```

update the call site to `chooseAction(col.Name, kind, opts)` and add `"strings"` to imports. (The `safeColumnNames` map literal above has a typo — write it as individual `"name": true` entries: `id, uuid, created_at, updated_at, created, updated, status, type, count`.)

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestBuildMaskingPlan -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): BuildMaskingPlan (default-deny) + Unmask"`

---

### Task 9: LLM classifier layer + chain

**Files:**
- Create: `pkg/connector/classify_llm.go`
- Test: `pkg/connector/classify_llm_test.go`

- [ ] **Step 1: Write the failing test** (fake JSONGenerator)

```go
package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

type fakeJSON struct{ out string }

func (f fakeJSON) GenerateJSON(context.Context, string, string) ([]byte, error) {
	return []byte(f.out), nil
}

func TestLLMClassifier(t *testing.T) {
	llm := &LLMClassifier{Client: fakeJSON{out: `{"pii_kind":"address","sensitivity":2,"reason":"looks like a street address"}`}}
	k, s, reason := llm.Classify(context.Background(), importflow.Column{Name: "loc"}, []string{"123 Main St"})
	if k != PiiAddress || s != Confidential || reason == "" {
		t.Fatalf("llm classify: %q %d %q", k, s, reason)
	}
}

func TestChainClassifierRuleFirst(t *testing.T) {
	// rule resolves phone; LLM must NOT be consulted (would return junk)
	llm := &LLMClassifier{Client: fakeJSON{out: `{"pii_kind":"name"}`}}
	chain := ChainClassifier{NewRuleClassifier(), llm}
	k, _, _ := chain.Classify(context.Background(), importflow.Column{Name: "phone"}, []string{"13812341234"})
	if k != PiiPhone {
		t.Fatalf("chain should keep rule result: %q", k)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `classify_llm.go`**

```go
package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// LLMClassifier asks a model to classify a column from its name + sample values.
// Used only for columns the rule layer is unsure about (cost + trust boundary).
type LLMClassifier struct {
	Client graphflow.JSONGenerator
}

const classifySystemPrompt = `You classify a database column's privacy sensitivity.
Given a column name and a few sample values, return ONLY JSON:
{"pii_kind":"<none|name|phone|email|national_id|bank_card|address|dob|ip|geo|custom>",
 "sensitivity":<0=public|1=internal|2=confidential|3=restricted>,
 "reason":"short"}
Judge by meaning; sample values may be partial. When unsure, prefer a higher
sensitivity (fail safe).`

func (c *LLMClassifier) Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	if c.Client == nil {
		return PiiNone, Public, ""
	}
	user := fmt.Sprintf("column: %s\ntype: %s\nsamples: %s", col.Name, col.Type, strings.Join(samples, " | "))
	raw, err := c.Client.GenerateJSON(ctx, classifySystemPrompt, user)
	if err != nil {
		return PiiNone, Public, "llm:error:" + err.Error()
	}
	var parsed struct {
		PiiKind     string `json:"pii_kind"`
		Sensitivity int    `json:"sensitivity"`
		Reason      string `json:"reason"`
	}
	s := string(raw)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		_ = json.Unmarshal([]byte(s[i:j+1]), &parsed)
	}
	k := PiiKind(parsed.PiiKind)
	if k == "none" {
		k = PiiNone
	}
	sens := Sensitivity(parsed.Sensitivity)
	if k == PiiNone {
		sens = Public
	}
	return k, sens, "llm:" + parsed.Reason
}

// ChainClassifier runs classifiers in order; the first non-none result wins, so
// cheap deterministic rules short-circuit before the LLM is consulted.
type ChainClassifier []Classifier

func (ch ChainClassifier) Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	for _, c := range ch {
		if k, s, r := c.Classify(ctx, col, samples); k != PiiNone {
			return k, s, r
		}
	}
	return PiiNone, Public, ""
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestLLMClassifier|TestChainClassifier' -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): LLM classifier + rule->LLM chain"`

---

### Task 10: Live Postgres source

**Files:**
- Create: `pkg/connector/source_postgres.go`
- Test: `pkg/connector/source_postgres_test.go`

- [ ] **Step 1: Add the driver**

Run: `go get github.com/jackc/pgx/v5/stdlib` then `go mod tidy`.

- [ ] **Step 2: Write the failing test** (skips without a DB; the env var supplies a DSN)

```go
package connector

import (
	"context"
	"os"
	"testing"
)

func TestPostgresSourceIntrospect(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN to run (e.g. postgres://user:pass@localhost:5432/db?sslmode=disable)")
	}
	src, err := NewPostgresSource(dsn, SourceOptions{SampleSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatal("no tables introspected")
	}
	for _, s := range schemas {
		if len(s.Columns) == 0 {
			t.Fatalf("table %s has no columns", s.Table)
		}
	}
}
```

- [ ] **Step 3: Implement `source_postgres.go`**

```go
package connector

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// SourceOptions configures a live SQL source.
type SourceOptions struct {
	Schema     string   // DB schema; Postgres default "public"
	Tables     []string // allow-list; empty = all base tables
	SampleSize int      // sample rows per table in Schemas(); default 5
	RowLimit   int      // max rows streamed per table; 0 = no limit
}

type sqlSource struct {
	db         *sql.DB
	driver     string // "pgx" | "mysql"
	opts       SourceOptions
	listTables func(ctx context.Context, db *sql.DB, schema string) ([]string, error)
	listCols   func(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error)
	quote      func(ident string) string
}

// NewPostgresSource connects to Postgres and returns an importflow.Source.
func NewPostgresSource(dsn string, opts SourceOptions) (importflow.Source, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connector: postgres ping: %w", err)
	}
	if opts.Schema == "" {
		opts.Schema = "public"
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	return &sqlSource{
		db: db, driver: "pgx", opts: opts,
		listTables: pgListTables, listCols: pgListColumns,
		quote: func(s string) string { return `"` + s + `"` },
	}, nil
}

func pgListTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema=$1 AND table_type='BASE TABLE' ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func pgListColumns(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []importflow.Column
	for rows.Next() {
		var name, dt string
		if err := rows.Scan(&name, &dt); err != nil {
			return nil, err
		}
		cols = append(cols, importflow.Column{Name: name, Type: normalizeSQLType(dt)})
	}
	return cols, rows.Err()
}

// normalizeSQLType maps SQL types onto importflow's small type vocabulary.
func normalizeSQLType(dt string) string {
	switch {
	case containsAny(dt, "char", "text", "uuid", "json", "enum"):
		return "text"
	case containsAny(dt, "int", "serial"):
		return "integer"
	case containsAny(dt, "numeric", "decimal", "real", "double", "float"):
		return "number"
	case containsAny(dt, "time", "date"):
		return "timestamp"
	default:
		return ""
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) <= len(s) && indexFold(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexFold(s, sub string) int {
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
```

- [ ] **Step 4: Implement the shared `Schemas`/`Records`/`Close` on `sqlSource`** (same file):

```go
func (s *sqlSource) tables(ctx context.Context) ([]string, error) {
	if len(s.opts.Tables) > 0 {
		return s.opts.Tables, nil
	}
	return s.listTables(ctx, s.db, s.opts.Schema)
}

func (s *sqlSource) Schemas(ctx context.Context) ([]importflow.Schema, error) {
	tables, err := s.tables(ctx)
	if err != nil {
		return nil, err
	}
	var out []importflow.Schema
	for _, t := range tables {
		cols, err := s.listCols(ctx, s.db, s.opts.Schema, t)
		if err != nil {
			return nil, err
		}
		sample, err := s.readRows(ctx, t, cols, s.opts.SampleSize)
		if err != nil {
			return nil, err
		}
		out = append(out, importflow.Schema{Table: t, Columns: cols, Sample: sample})
	}
	return out, nil
}

func (s *sqlSource) Records(ctx context.Context, fn func(importflow.Record) error) error {
	tables, err := s.tables(ctx)
	if err != nil {
		return err
	}
	for _, t := range tables {
		cols, err := s.listCols(ctx, s.db, s.opts.Schema, t)
		if err != nil {
			return err
		}
		recs, err := s.readRows(ctx, t, cols, s.opts.RowLimit)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// readRows selects up to limit rows (0 = all) and converts them to Records.
func (s *sqlSource) readRows(ctx context.Context, table string, cols []importflow.Column, limit int) ([]importflow.Record, error) {
	q := "SELECT * FROM " + s.quote(table)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	colNames, _ := rows.Columns()
	var out []importflow.Record
	idx := 0
	for rows.Next() {
		raw := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := importflow.Record{Table: table, Row: idx, Values: map[string]string{}, Nulls: map[string]bool{}}
		for i, name := range colNames {
			if raw[i] == nil {
				rec.Nulls[name] = true
				continue
			}
			rec.Values[name] = valueToString(raw[i])
		}
		out = append(out, rec)
		idx++
	}
	return out, rows.Err()
}

func valueToString(v any) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

func (s *sqlSource) Close() error { return s.db.Close() }
```

- [ ] **Step 5: Run** — `go build ./pkg/connector && go test ./pkg/connector -run TestPostgresSource -v` (skips without DSN). Optionally start a throwaway PG: `docker run --rm -d -e POSTGRES_PASSWORD=p -p 5433:5432 postgres:16`, seed a table, `CONNECTOR_PG_DSN=postgres://postgres:p@localhost:5433/postgres?sslmode=disable go test ...`.
- [ ] **Step 6: Commit** — `git commit -am "feat(connector): live Postgres source via information_schema"`

---

### Task 11: Live MySQL source

**Files:**
- Create: `pkg/connector/source_mysql.go`
- Test: `pkg/connector/source_mysql_test.go`

- [ ] **Step 1: Add the driver** — `go get github.com/go-sql-driver/mysql` then `go mod tidy`.

- [ ] **Step 2: Write the failing test**

```go
package connector

import (
	"context"
	"os"
	"testing"
)

func TestMySQLSourceIntrospect(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_DSN to run (e.g. user:pass@tcp(localhost:3306)/db)")
	}
	src, err := NewMySQLSource(dsn, SourceOptions{SampleSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatal("no tables introspected")
	}
}
```

- [ ] **Step 3: Implement `source_mysql.go`** (reuses `sqlSource`; MySQL uses backtick quoting and `DATABASE()` as the schema)

```go
package connector

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// NewMySQLSource connects to MySQL/MariaDB and returns an importflow.Source.
func NewMySQLSource(dsn string, opts SourceOptions) (importflow.Source, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connector: mysql ping: %w", err)
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	// MySQL: the active schema is the connection's database; resolve it if unset.
	if opts.Schema == "" {
		if err := db.QueryRow("SELECT DATABASE()").Scan(&opts.Schema); err != nil {
			return nil, fmt.Errorf("connector: mysql current db: %w", err)
		}
	}
	return &sqlSource{
		db: db, driver: "mysql", opts: opts,
		listTables: myListTables, listCols: myListColumns,
		quote: func(s string) string { return "`" + s + "`" },
	}, nil
}

func myListTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema=? AND table_type='BASE TABLE' ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func myListColumns(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []importflow.Column
	for rows.Next() {
		var name, dt string
		if err := rows.Scan(&name, &dt); err != nil {
			return nil, err
		}
		cols = append(cols, importflow.Column{Name: name, Type: normalizeSQLType(dt)})
	}
	return cols, rows.Err()
}
```

- [ ] **Step 4: Run** — `go build ./pkg/connector && go test ./pkg/connector -run TestMySQLSource -v` (skips without DSN; throwaway: `docker run --rm -d -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 3307:3306 mysql:8`, DSN `root:p@tcp(localhost:3307)/test`).
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): live MySQL source via information_schema"`

---

### Task 12: End-to-end into ImportFlow (the decorator in the real pipeline)

**Files:**
- Test: `pkg/connector/e2e_test.go`

- [ ] **Step 1: Write the test** — fake source → build plan → sign → desensitize → `importflow.New(db).Run` with a hand-written MappingPlan, then assert RAG search returns the masked content (no PII).

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestEndToEndDesensitizedImport(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	src := newFake() // users: id,name,phone,ssn,notes (from desensitize_test.go)

	plan, err := BuildMaskingPlan(ctx, src, NewRuleClassifier(), PlanOptions{ScanTextColumns: true})
	if err != nil {
		t.Fatal(err)
	}
	plan.Sign("reviewer", time.Unix(1, 0))
	d, err := NewDesensitizer(plan, DesensitizerOptions{
		Tenant: "t", KeyProvider: testKP(),
		Vault: func() Vault { v, _ := OpenSQLiteVault(filepath.Join(dir, "v.db")); return v }(),
	})
	if err != nil {
		t.Fatal(err)
	}
	safe := Desensitized(src, d)

	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone} {notes}"}},
	}}
	if _, err := importflow.New(db).Run(ctx, safe, mapping); err != nil {
		t.Fatal(err)
	}

	res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "vip customer", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Results {
		full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		body := full.Knowledge.Content
		for _, leak := range []string{"13812341234", "13900000000", "110101199003078888", "张三"} {
			if indexOf(body, leak) >= 0 {
				t.Fatalf("PII leaked into RAG content: %q in %q", leak, body)
			}
		}
	}
}
```

- [ ] **Step 2: Run, verify pass** — `go test ./pkg/connector -run TestEndToEnd -v` (lexical mode; no embedder needed).
- [ ] **Step 3: Commit** — `git commit -am "test(connector): end-to-end desensitized import into ImportFlow RAG (no PII leak)"`

---

### Task 13: Connector tools on importflow's MCP surface

**Files:**
- Create: `pkg/connector/toolbox.go`
- Test: `pkg/connector/toolbox_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestToolboxIntrospectAndUnmask(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	v, _ := OpenSQLiteVault(filepath.Join(dir, "v.db"))
	defer v.Close()
	tb := NewToolbox(db, ToolboxOptions{Vault: v, KeyProvider: testKP(), Tenant: "t"})

	// definitions present
	names := map[string]bool{}
	for _, d := range tb.Definitions() {
		names[d.Name] = true
	}
	for _, want := range []string{"connector_introspect", "connector_plan", "connector_run", "connector_unmask"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}

	// unmask round-trips a token created via the vault
	tok, _ := v.Put(context.Background(), "t", PiiName, "张三", testKP())
	out, err := tb.Call(context.Background(), "connector_unmask", json.RawMessage(`{"tokens":["`+tok+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if indexOf(string(b), "张三") < 0 {
		t.Fatalf("unmask did not return original: %s", b)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `toolbox.go`** — connector tools that ride the same `cortexdb.ToolDefinition` shape importflow uses, so they register onto importflow's MCP/toolbox. For DSN-based sources, `connector_introspect`/`plan`/`run` take a `{driver, dsn, schema, sample_size}` input.

```go
package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// ToolboxOptions wires the connector tools to a vault + classifier.
type ToolboxOptions struct {
	Vault       Vault
	KeyProvider KeyProvider
	Tenant      string
	Classifier  Classifier // defaults to NewRuleClassifier()
}

// Toolbox exposes the connector as agent-callable tools, designed to be merged
// into importflow's MCP/toolbox surface.
type Toolbox struct {
	db   *cortexdb.DB
	opts ToolboxOptions
	cls  Classifier
}

// NewToolbox builds a connector toolbox.
func NewToolbox(db *cortexdb.DB, opts ToolboxOptions) *Toolbox {
	cls := opts.Classifier
	if cls == nil {
		cls = NewRuleClassifier()
	}
	return &Toolbox{db: db, opts: opts, cls: cls}
}

func openSource(driver, dsn string, so SourceOptions) (importflow.Source, error) {
	switch driver {
	case "postgres", "pgx":
		return NewPostgresSource(dsn, so)
	case "mysql":
		return NewMySQLSource(dsn, so)
	default:
		return nil, fmt.Errorf("connector: unknown driver %q", driver)
	}
}

// Definitions returns the connector tool definitions.
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	src := map[string]any{
		"driver":      map[string]any{"type": "string", "description": "postgres | mysql"},
		"dsn":         map[string]any{"type": "string", "description": "connection string"},
		"schema":      map[string]any{"type": "string", "description": "DB schema (optional)"},
		"sample_size": map[string]any{"type": "integer", "description": "sample rows per table"},
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	return []cortexdb.ToolDefinition{
		{Name: "connector_introspect", Description: "Introspect a live DB's schema (tables, columns, sample rows). Data is NOT imported.", InputSchema: obj(src, "driver", "dsn")},
		{Name: "connector_plan", Description: "Introspect + classify PII and return an UNSIGNED MaskingPlan for review.", InputSchema: obj(src, "driver", "dsn")},
		{Name: "connector_run", Description: "Run a desensitized import into RAG+KG using a signed MaskingPlan and a MappingPlan.", InputSchema: obj(map[string]any{
			"driver": src["driver"], "dsn": src["dsn"], "schema": src["schema"],
			"masking_plan": map[string]any{"type": "object", "description": "signed MaskingPlan JSON"},
			"mapping_plan": map[string]any{"type": "object", "description": "importflow MappingPlan JSON"},
		}, "driver", "dsn", "masking_plan", "mapping_plan")},
		{Name: "connector_unmask", Description: "Reverse pseudonymized tokens to originals via the tenant vault (audited).", InputSchema: obj(map[string]any{
			"tokens": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, "tokens")},
	}
}

// Call dispatches a connector tool by name.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "connector_introspect":
		var in struct{ Driver, DSN, Schema string; SampleSize int `json:"sample_size"` }
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema, SampleSize: in.SampleSize})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		return src.Schemas(ctx)

	case "connector_plan":
		var in struct{ Driver, DSN, Schema string; SampleSize int `json:"sample_size"` }
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema, SampleSize: in.SampleSize})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		return BuildMaskingPlan(ctx, src, t.cls, PlanOptions{ScanTextColumns: true})

	case "connector_run":
		var in struct {
			Driver, DSN, Schema string
			MaskingPlan         MaskingPlan            `json:"masking_plan"`
			MappingPlan         importflow.MappingPlan `json:"mapping_plan"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		d, err := NewDesensitizer(in.MaskingPlan, DesensitizerOptions{Tenant: t.opts.Tenant, KeyProvider: t.opts.KeyProvider, Vault: t.opts.Vault})
		if err != nil {
			return nil, err
		}
		return importflow.New(t.db).Run(ctx, Desensitized(src, d), in.MappingPlan)

	case "connector_unmask":
		var in struct{ Tokens []string `json:"tokens"` }
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		if t.opts.Vault == nil || t.opts.KeyProvider == nil {
			return nil, fmt.Errorf("connector: unmask needs Vault + KeyProvider")
		}
		return Unmask(ctx, t.opts.Vault, t.opts.Tenant, in.Tokens, t.opts.KeyProvider)

	default:
		return nil, fmt.Errorf("connector: unknown tool %q", name)
	}
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestToolbox -v`
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): agent toolbox (introspect/plan/run/unmask) for importflow MCP"`

---

### Task 14: Docs + example

**Files:**
- Create: `examples/09_connector/main.go`
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1: Example** — `examples/09_connector/main.go`: open a temp CortexDB, build a `CSVSource` (reuse importflow's, no live DB needed to run), `BuildMaskingPlan` → print it → `Sign` → `Desensitized` → `importflow.Run` → `SearchKnowledge`, printing that results contain no PII. Compile-only in CI (matches the examples convention).

```go
// Demo: desensitize a CSV through the connector privacy gate, then import to RAG.
// go run ./examples/09_connector
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func main() {
	dbPath := "example_connector.db"
	vaultPath := "example_connector.vault.db"
	defer os.Remove(dbPath)
	defer os.Remove(vaultPath)

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	csv := "id,name,phone,notes\n1,张三,13812341234,VIP; reach at 13900000000\n"
	src, err := importflow.NewCSVSource(strings.NewReader(csv), importflow.CSVOptions{Table: "customers"})
	if err != nil {
		log.Fatal(err)
	}

	plan, err := connector.BuildMaskingPlan(ctx, src, connector.NewRuleClassifier(), connector.PlanOptions{ScanTextColumns: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("proposed MaskingPlan (review before signing):")
	for _, r := range plan.Columns {
		fmt.Printf("  %s.%s  kind=%s  action=%s  (%s)\n", r.Table, r.Column, r.PiiKind, r.Action, r.Reason)
	}
	plan.Sign("you", time.Now())

	vault, err := connector.OpenSQLiteVault(vaultPath)
	if err != nil {
		log.Fatal(err)
	}
	defer vault.Close()
	d, err := connector.NewDesensitizer(plan, connector.DesensitizerOptions{
		Tenant: "demo", KeyProvider: connector.StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef")), Vault: vault,
	})
	if err != nil {
		log.Fatal(err)
	}

	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"customers": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone} {notes}"}},
	}}
	rep, err := importflow.New(db).Run(ctx, connector.Desensitized(src, d), mapping)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("imported: %d rows, %d chunks (PII desensitized before indexing)\n", rep.RowsRead, rep.ChunksIndexed)
}
```

- [ ] **Step 2: Verify it compiles** — `cd examples/09_connector && go build -o /dev/null .`
- [ ] **Step 3: README/README_CN/SKILL** — add a "Data connector (privacy/desensitization)" subsection under the ImportFlow material: what it does, the schema-first/signed-plan invariant, the four tools, vault/un-mask, and `examples/09_connector`. Mirror across all three per the repo's sync convention.
- [ ] **Step 4: Commit** — `git commit -am "docs(connector): example 09 + README/SKILL data-connector section"`

---

### Task 15: Full verification

- [ ] **Step 1:** `go build ./...` → OK
- [ ] **Step 2:** `go test ./pkg/connector -race` → all pass (live-DB tests skip without DSNs)
- [ ] **Step 3:** `go test ./... -race` → no regressions
- [ ] **Step 4:** examples compile: `for d in examples/*/; do (cd "$d" && go build -o /dev/null .) || echo "FAIL $d"; done`
- [ ] **Step 5: Commit** any tidy-ups; ensure `go.mod`/`go.sum` committed (pgx + mysql drivers).

---

## Self-review notes

- **Spec coverage:** live sources PG+MySQL (T10,T11) · 3-layer classifier rules+LLM+human-sign (T3,T9, sign gate in T1/T7) · column masking incl. drop/mask/hash/generalize/pseudonymize (T2,T7) · free-text PII (T6, wired in T7) · token vault + key custody + Unmask (T4,T5,T8) · desensitizer-as-Source decorator (T7) · default-deny (T8) · irreversible-into-LLM (vault never read on import/retrieval path; T7/T12 assert no PII in RAG) · connector tools on importflow MCP (T13) · audit (vault Resolve is the single reverse path; extend with a log table if T13 review wants it). 
- **Known judgment calls:** `safeColumnNames` allowlist keeps obvious non-PII (id/created_at) so default-deny doesn't redact everything (T8). Free-text NER LLM layer is deferred (regex layer ships in v1; LLM column-classify ships in T9) — the spec's "honest residual risk" is satisfied by surfacing it in docs, and a v2 note. Audit-log destination is an open decision (spec §Open decisions) — vault `Resolve` is the choke point to add it.
- **Verify-at-impl markers:** the `safeColumnNames` map literal in T8 must be written as individual `"x": true` pairs (the inline version has a deliberate typo callout). Confirm pgx v5 `stdlib` driver name is `"pgx"` and mysql is `"mysql"` at build time.
