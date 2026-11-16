//go:build !windows

package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// keyDir 是存放本地主密钥的目录（非 Windows 平台没有 DPAPI）。
var keyDir string

var (
	masterOnce sync.Once
	masterKey  []byte
	masterErr  error
)

func loadMasterKey() ([]byte, error) {
	masterOnce.Do(func() {
		path := filepath.Join(keyDir, ".master.key")
		if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
			masterKey = b
			return
		}
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			masterErr = err
			return
		}
		if err := os.WriteFile(path, k, 0o600); err != nil {
			masterErr = err
			return
		}
		masterKey = k
	})
	return masterKey, masterErr
}

func protect(data []byte) ([]byte, error) {
	key, err := loadMasterKey()
	if err != nil {
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
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func unprotect(data []byte) ([]byte, error) {
	key, err := loadMasterKey()
	if err != nil {
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
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("密文过短")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}
