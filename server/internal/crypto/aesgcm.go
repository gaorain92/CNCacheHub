// Package crypto 提供 CNCacheHub 的本地加密工具（PRD §9.7.3 上游凭据管理）。
//
// 设计：
//   - AES-256-GCM（authenticated encryption，带完整性校验）
//   - 每次加密随机 12 字节 nonce，nonce 附在密文前：[nonce | ciphertext | tag]
//   - 密钥：32 字节 raw，由 MasterKey 管理（持久化在 data_dir/.master_key）
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrCipherTooShort 密文长度不足以包含 nonce。
var ErrCipherTooShort = errors.New("crypto: ciphertext too short")

// ErrDecryptFailed 解密 / 认证失败。
var ErrDecryptFailed = errors.New("crypto: decrypt failed (key mismatch or tampered)")

// Encrypt 用 AES-256-GCM 加密 plaintext。
//
// 返回密文格式：nonce(12) || ciphertext || tag(16)
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
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
	// Seal(dst, nonce, plaintext, additionalData) — dst 包含 nonce + ciphertext + tag
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密 Encrypt 产生的密文。
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrCipherTooShort
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}

// EncryptString helper：明文 → 密文。
func EncryptString(key []byte, s string) ([]byte, error) {
	return Encrypt(key, []byte(s))
}

// DecryptString helper：密文 → 明文。
func DecryptString(key []byte, c []byte) (string, error) {
	p, err := Decrypt(key, c)
	if err != nil {
		return "", err
	}
	return string(p), nil
}
