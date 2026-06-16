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
