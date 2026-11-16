package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"

	"golang.org/x/crypto/scrypt"
)

// Backup 是导出的完整备份：结构化数据（含明文敏感字段）+ 记忆/技能等文件。
type Backup struct {
	Version   int               `json:"version"`
	CreatedAt int64             `json:"createdAt"`
	Data      *Data             `json:"data"`
	Files     map[string]string `json:"files"`
}

var backupMagic = []byte("SSHPILOT-BACKUP-1\n")

func deriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
}

// EncryptBackup 用口令（scrypt + AES-256-GCM）加密备份。
func EncryptBackup(b *Backup, password string) ([]byte, error) {
	if len(password) < 6 {
		return nil, errors.New("备份密码至少 6 位")
	}
	plain, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := deriveKey(password, salt)
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
	out := append([]byte{}, backupMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, backupMagic), nil
}

func DecryptBackup(raw []byte, password string) (*Backup, error) {
	if !bytes.HasPrefix(raw, backupMagic) {
		return nil, errors.New("不是有效的 QianYe SSH Pilot 备份文件")
	}
	raw = raw[len(backupMagic):]
	if len(raw) < 16+12 {
		return nil, errors.New("备份文件已损坏")
	}
	salt, rest := raw[:16], raw[16:]
	key, err := deriveKey(password, salt)
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
	nonce, ct := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, backupMagic)
	if err != nil {
		return nil, errors.New("备份密码错误或文件已损坏")
	}
	var b Backup
	if err := json.Unmarshal(plain, &b); err != nil {
		return nil, err
	}
	if b.Data == nil {
		return nil, errors.New("备份中没有数据")
	}
	return &b, nil
}
