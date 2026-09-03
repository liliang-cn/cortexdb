package authz

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustKeySet(t *testing.T, keys ...Key) *KeySet {
	t.Helper()
	ks, err := NewKeySet(keys)
	if err != nil {
		t.Fatalf("build key set: %v", err)
	}
	return ks
}

func TestTheLegacySingleTokenBecomesOneUnconfinedReadWriteKey(t *testing.T) {
	ks := LegacyToken("s3cret")
	if !ks.Enabled() || ks.Len() != 1 {
		t.Fatalf("expected exactly one key, got %d", ks.Len())
	}
	key, ok := ks.Lookup("s3cret")
	if !ok {
		t.Fatal("the legacy token did not resolve to a key")
	}
	if key.Clearance != ReadWrite {
		t.Fatalf("clearance = %q, want %q", key.Clearance, ReadWrite)
	}
	if !key.Scope.IsZero() {
		t.Fatalf("the legacy token must stay unconfined, got %+v", key.Scope)
	}
	// Every write, every read, no row confinement: the deployments in the
	// wild have to keep behaving exactly as they did.
	for _, method := range ClassifiedMethods() {
		if err := key.AuthorizeMethod(method); err != nil {
			t.Errorf("legacy key refused %s: %v", method, err)
		}
		if err := key.AuthorizeRows(method, nil); err != nil {
			t.Errorf("legacy key refused rows on %s: %v", method, err)
		}
	}
}

func TestAnEmptyLegacyTokenLeavesAuthenticationOff(t *testing.T) {
	if LegacyToken("").Enabled() {
		t.Fatal("an unset token must not enable authentication")
	}
}

func TestAKeyFileReplacesTheLegacyTokenRatherThanJoiningIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	body := `{"keys":[{"id":"hermes","secret":"h-secret","clearance":"read-only"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ks, err := Resolve(path, "legacy-master-token")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := ks.Lookup("legacy-master-token"); ok {
		t.Fatal("the environment token outranked the key file, which is the hole the file exists to close")
	}
	if _, ok := ks.Lookup("h-secret"); !ok {
		t.Fatal("the key file's own key did not resolve")
	}
}

func TestARevokedKeyIsRefusedOnceItLeavesTheKeyFile(t *testing.T) {
	ks := mustKeySet(t,
		Key{ID: "hermes", Secret: "h-secret", Clearance: ReadWrite},
	)
	if _, ok := ks.Lookup("zeus-secret"); ok {
		t.Fatal("a secret that is in no key resolved to a key")
	}
}

func TestAKeyFileWithoutAClearanceIsRefusedRatherThanAssumedReadWrite(t *testing.T) {
	_, err := Parse([]byte(`{"keys":[{"id":"hermes","secret":"h"}]}`))
	if err == nil {
		t.Fatal("a key with no clearance loaded; the silent default would have been full access")
	}
	if !strings.Contains(err.Error(), "clearance") {
		t.Fatalf("error should name the missing clearance, got %v", err)
	}
}

func TestAMisspelledFieldInTheKeyFileIsAnErrorNotAnUnconfinedKey(t *testing.T) {
	// "scopes" instead of "scope" would otherwise load as a key with no
	// confinement at all, which is the worst possible way to mistype.
	_, err := Parse([]byte(`{"keys":[{"id":"h","secret":"s","clearance":"read-only","scopes":{"user_id":"hermes"}}]}`))
	if err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestTwoKeysSharingASecretAreRefusedBecauseTheScopeWouldDependOnFileOrder(t *testing.T) {
	_, err := NewKeySet([]Key{
		{ID: "a", Secret: "same", Clearance: ReadWrite},
		{ID: "b", Secret: "same", Clearance: ReadOnly},
	})
	if err == nil {
		t.Fatal("duplicate secrets were accepted")
	}
}

func TestDuplicateKeyIdsAreRefused(t *testing.T) {
	_, err := NewKeySet([]Key{
		{ID: "a", Secret: "one", Clearance: ReadWrite},
		{ID: "a", Secret: "two", Clearance: ReadWrite},
	})
	if err == nil {
		t.Fatal("duplicate ids were accepted; revoking one would be ambiguous")
	}
}

func TestAnEmptyKeyFileIsAnErrorRatherThanAnOpenServer(t *testing.T) {
	if _, err := Parse([]byte(`{"keys":[]}`)); err == nil {
		t.Fatal("an empty key list loaded, which would have disabled authentication")
	}
}

// TestSecretsAreComparedInConstantTime reads the source rather than timing it.
// A timing measurement in CI is a flaky test that proves nothing on a loaded
// runner; what actually needs protecting is the property that somebody editing
// Lookup does not quietly replace the constant-time comparison with ==, and
// that is visible in the source.
func TestSecretsAreComparedInConstantTime(t *testing.T) {
	src, err := os.ReadFile("authz.go")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "func (ks *KeySet) Lookup("
	start := strings.Index(string(src), marker)
	if start < 0 {
		t.Fatal("Lookup has been renamed; update this test to follow it")
	}
	body := string(src)[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "subtle.ConstantTimeCompare") {
		t.Error("Lookup no longer uses subtle.ConstantTimeCompare")
	}
	if strings.Contains(body, "Secret ==") || strings.Contains(body, "== ks.keys") {
		t.Error("Lookup compares secrets with ==, which leaks their prefix through timing")
	}
	if strings.Contains(body, "break") || strings.Contains(body, "return ks.keys[i]") {
		t.Error("Lookup returns early on a match, which leaks a key's position in the file")
	}
}

func TestAReadOnlyKeyIsRefusedEveryWriteRPCAndAllowedEveryReadOne(t *testing.T) {
	ro := Key{ID: "reader", Secret: "s", Clearance: ReadOnly}
	rw := Key{ID: "writer", Secret: "t", Clearance: ReadWrite}
	for _, method := range ClassifiedMethods() {
		m, _ := LookupMethod(method)
		err := ro.AuthorizeMethod(method)
		switch m.Access {
		case Write:
			if err == nil {
				t.Errorf("read-only key was allowed the write %s", method)
			} else if !errors.Is(err, ErrDenied) {
				t.Errorf("%s: refusal is not an ErrDenied: %v", method, err)
			}
		case Read:
			if err != nil {
				t.Errorf("read-only key was refused the read %s: %v", method, err)
			}
		default:
			t.Errorf("%s is in the table but classified as %s", method, m.Access)
		}
		if err := rw.AuthorizeMethod(method); err != nil {
			t.Errorf("read-write key was refused %s: %v", method, err)
		}
	}
}

func TestAnUnclassifiedMethodIsDeniedEvenToAFullAccessKey(t *testing.T) {
	key := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	const unknown = "/cortexdb.v1.SomeFutureService/DropEverything"
	if err := key.AuthorizeMethod(unknown); err == nil {
		t.Fatal("an unclassified method was allowed; a new write would be reachable by every key")
	}
	confined := Key{ID: "hermes", Secret: "s2", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	if err := confined.AuthorizeRows(unknown, staticLookup(nil)); err == nil {
		t.Fatal("row checks passed for an unclassified method")
	}
}

// staticLookup answers a FieldLookup from a fixed map of top-level values;
// every named field counts as declared.
func staticLookup(top map[string]string) FieldLookup {
	return func(field string) (string, []string, bool) {
		v, ok := top[field]
		return v, nil, ok
	}
}

func TestAnUnconfinedKeySkipsTheRowChecksEntirely(t *testing.T) {
	key := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SearchMemory", nil); err != nil {
		t.Fatalf("an unconfined key was row-checked: %v", err)
	}
}

func TestAConfinedKeyIsDeniedWhenTheRequestCannotBeInspected(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SearchMemory", nil); err == nil {
		t.Fatal("a request that could not be read was allowed through")
	}
}

func TestAConfinedKeyMayStillCallTheRowlessAdminRPCs(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadOnly, Scope: Scope{UserID: "hermes"}}
	for _, method := range []string{
		"/cortexdb.v1.AdminService/Health",
		"/cortexdb.v1.AdminService/Info",
		"/cortexdb.v1.ToolsService/ListTools",
	} {
		if err := key.AuthorizeRows(method, nil); err != nil {
			t.Errorf("%s was refused to a confined key: %v", method, err)
		}
	}
}

func TestAConfinedKeyIsDeniedWhenTheRequestTypeHasNoSuchField(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	err := key.AuthorizeRows("/cortexdb.v1.MemoryService/GetMemory", staticLookup(nil))
	if err == nil {
		t.Fatal("a request with nothing to confine was allowed")
	}
	if !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("the denial should name the missing field, got %v", err)
	}
}

func TestAConfinedKeyIsDeniedWhenTheRequestLeavesTheFieldUnset(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadOnly, Scope: Scope{UserID: "hermes"}}
	err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SearchMemory",
		staticLookup(map[string]string{"user_id": ""}))
	if err == nil {
		t.Fatal("an unset user_id means every user, and a confined key may not ask for that")
	}
}

func TestAConfinedKeyIsDeniedAnotherUsersRows(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SaveMemory",
		staticLookup(map[string]string{"user_id": "zeus"})); err == nil {
		t.Fatal("a confined key wrote into another user's rows")
	}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SaveMemory",
		staticLookup(map[string]string{"user_id": "hermes"})); err != nil {
		t.Fatalf("a confined key was refused its own rows: %v", err)
	}
}

func TestEveryFieldOfAMultiFieldScopeMustMatch(t *testing.T) {
	key := Key{ID: "k", Secret: "s", Clearance: ReadWrite,
		Scope: Scope{UserID: "hermes", MemoryScope: "user", Namespace: "notes"}}
	full := map[string]string{"user_id": "hermes", "scope": "user", "namespace": "notes"}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SaveMemory", staticLookup(full)); err != nil {
		t.Fatalf("a fully matching request was refused: %v", err)
	}
	for field := range full {
		wrong := map[string]string{}
		for k, v := range full {
			wrong[k] = v
		}
		wrong[field] = "elsewhere"
		if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SaveMemory", staticLookup(wrong)); err == nil {
			t.Errorf("a request with the wrong %s was allowed", field)
		}
	}
}

func TestANestedCopyOfAScopeFieldCannotContradictTheKey(t *testing.T) {
	key := Key{ID: "hermes", Secret: "s", Clearance: ReadOnly, Scope: Scope{UserID: "hermes"}}
	lookup := func(field string) (string, []string, bool) {
		if field != "user_id" {
			return "", nil, false
		}
		// The shape of a SearchMemoryRequest that says hermes at the top and
		// zeus inside its retrieval plan's filters.
		return "hermes", []string{"zeus"}, true
	}
	if err := key.AuthorizeRows("/cortexdb.v1.MemoryService/SearchMemory", lookup); err == nil {
		t.Fatal("a nested filter smuggled another user past the scope check")
	}
}
