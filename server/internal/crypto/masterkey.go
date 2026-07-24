package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MasterKeyFilename 是 master key 落盘文件名（放在 data_dir 下）。
//
// 文件权限 0600，root:root；systemd 以 root 跑能读，普通用户读不到。
const MasterKeyFilename = ".master_key"

// ErrMasterKeyInvalid master key 文件大小不对（被外部篡改？）。
var ErrMasterKeyInvalid = errors.New("crypto: master key file is invalid (expected 32 bytes)")

// LoadOrCreateMasterKey 加载 master key；不存在就生成 + 写 (0600)。
//
// 返回 32 字节 raw key。
//
// 优先级：
//  1. env CNCH_MASTER_KEY（hex 编码，64 字符）— 显式指定时优先用
//  2. <dataDir>/.master_key 文件 — 自动持久化
func LoadOrCreateMasterKey(dataDir, envOverride string) ([]byte, error) {
	// 1. env 优先
	if envOverride != "" {
		if len(envOverride) != 64 {
			return nil, fmt.Errorf("crypto: CNCH_MASTER_KEY must be 64 hex chars (32 bytes), got %d", len(envOverride))
		}
		key := make([]byte, 32)
		for i := 0; i < 32; i++ {
			hi, ok1 := hexNibble(envOverride[2*i])
			lo, ok2 := hexNibble(envOverride[2*i+1])
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("crypto: CNCH_MASTER_KEY has invalid hex at position %d", 2*i)
			}
			key[i] = hi<<4 | lo
		}
		return key, nil
	}

	// 2. 读文件
	path := filepath.Join(dataDir, MasterKeyFilename)
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, ErrMasterKeyInvalid
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("crypto: read master key: %w", err)
	}

	// 3. 不存在 → 生成 + 写
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("crypto: mkdir dataDir: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto: rand.Read: %w", err)
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, fmt.Errorf("crypto: write master key: %w", err)
	}
	return key, nil
}

// hexNibble 把 '0'-'9'/'a'-'f'/'A'-'F' 转成 0-15。
func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
