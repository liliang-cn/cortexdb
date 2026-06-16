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

// shortKeyProvider returns an undersized key without self-policing, to prove the
// vault's own crypto path rejects it (rather than relying on the provider).
type shortKeyProvider []byte

func (s shortKeyProvider) TenantKey(context.Context, string) ([]byte, error) { return s, nil }

// A non-32-byte key must be rejected by the vault itself, so the AES-256
// guarantee does not silently downgrade to AES-128/192 with a short key.
func TestVaultRejectsShortKey(t *testing.T) {
	dir := t.TempDir()
	short := shortKeyProvider([]byte("0123456789abcdef")) // 16 bytes — would be AES-128
	v, _ := OpenSQLiteVault(filepath.Join(dir, "t.vault.db"))
	defer v.Close()
	if _, err := v.Put(context.Background(), "t", PiiPhone, "13812341234", short); err == nil {
		t.Fatal("expected Put to reject a non-32-byte key")
	}
}
