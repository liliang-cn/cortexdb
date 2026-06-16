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
