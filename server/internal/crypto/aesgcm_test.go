package crypto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	plaintext := []byte("my-secret-password-123")
	ct, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Error("ciphertext equals plaintext (encryption failed)")
	}
	if len(ct) < len(plaintext) {
		t.Errorf("ciphertext too short: %d < %d", len(ct), len(plaintext))
	}
	pt, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("Decrypt mismatch: got %q, want %q", pt, plaintext)
	}
}

func TestEncryptStringHelpers(t *testing.T) {
	key := make([]byte, 32)
	ct, err := EncryptString(key, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	s, err := DecryptString(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello world" {
		t.Errorf("got %q", s)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(255 - i)
	}
	ct, _ := Encrypt(key1, []byte("secret"))
	_, err := Decrypt(key2, ct)
	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	ct, _ := Encrypt(key, []byte("secret"))
	ct[len(ct)-1] ^= 0xFF // 改最后 1 字节（tag 区域）
	_, err := Decrypt(key, ct)
	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := Decrypt(key, []byte{1, 2, 3})
	if !errors.Is(err, ErrCipherTooShort) {
		t.Errorf("expected ErrCipherTooShort, got %v", err)
	}
}

func TestEncrypt_WrongKeySize(t *testing.T) {
	_, err := Encrypt([]byte("short"), []byte("data"))
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestEncrypt_RandomNonce(t *testing.T) {
	// 同样的 plaintext + key 每次应该产生不同密文（nonce 随机）
	key := make([]byte, 32)
	ct1, _ := Encrypt(key, []byte("same"))
	ct2, _ := Encrypt(key, []byte("same"))
	if bytes.Equal(ct1, ct2) {
		t.Error("two encrypts of same plaintext produced same ciphertext (nonce should be random)")
	}
}

// === master key tests ===

func TestLoadOrCreateMasterKey_FreshGeneration(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateMasterKey(dir, "")
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key len = %d, want 32", len(key))
	}
	// 文件应已创建
	path := filepath.Join(dir, MasterKeyFilename)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("master key file not created: %v", err)
	}
	// 文件权限应该是 0600
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestLoadOrCreateMasterKey_ReuseExisting(t *testing.T) {
	dir := t.TempDir()
	k1, _ := LoadOrCreateMasterKey(dir, "")
	k2, _ := LoadOrCreateMasterKey(dir, "")
	if !bytes.Equal(k1, k2) {
		t.Error("second call should return same key (read from file)")
	}
}

func TestLoadOrCreateMasterKey_FromEnv(t *testing.T) {
	dir := t.TempDir()
	// 64 字符 hex
	envKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, err := LoadOrCreateMasterKey(dir, envKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Errorf("key len = %d", len(key))
	}
	if key[0] != 0x01 || key[31] != 0xef {
		t.Errorf("key decoded wrong: first=%x last=%x", key[0], key[31])
	}
}

func TestLoadOrCreateMasterKey_EnvBadHex(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadOrCreateMasterKey(dir, "not-hex-but-64-chars-padding-padding-padding-paddin-padd")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestLoadOrCreateMasterKey_EnvWrongLen(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadOrCreateMasterKey(dir, "deadbeef")
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestLoadOrCreateMasterKey_InvalidFileSize(t *testing.T) {
	dir := t.TempDir()
	// 写一个错的文件
	if err := os.WriteFile(filepath.Join(dir, MasterKeyFilename), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateMasterKey(dir, "")
	if !errors.Is(err, ErrMasterKeyInvalid) {
		t.Errorf("expected ErrMasterKeyInvalid, got %v", err)
	}
}
