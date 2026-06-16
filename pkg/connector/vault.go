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

// requireAES256Key rejects any key that is not exactly 32 bytes, so the
// AES-256 guarantee is structural and does not depend on every KeyProvider
// policing its own output (a 16/24-byte key would otherwise silently downgrade
// to AES-128/192).
func requireAES256Key(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("connector: key must be 32 bytes for AES-256, got %d", len(key))
	}
	return nil
}

func sealAESGCM(key, plaintext []byte) ([]byte, error) {
	if err := requireAES256Key(key); err != nil {
		return nil, err
	}
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
	if err := requireAES256Key(key); err != nil {
		return nil, err
	}
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
