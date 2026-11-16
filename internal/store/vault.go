package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- 启动密码 ----
//
// 设置启动密码后，程序生成一把随机的数据密钥，所有敏感字段（服务器密码、私钥、API Key 等）先用它加密
// （AES-256-GCM），再和以前一样交给系统加密后写入 store.json。数据密钥本身用启动密码派生出的密钥
// （scrypt）加密后放在 lock.json 里，启动密码不保存在任何地方。没有输入正确的启动密码时，这些字段
// 在内存里保持密文，程序不会用它们去连接服务器。
//
// lock.json 单独成一个文件：不认识启动密码的旧版本程序打开同一份数据时，只会原样保留那些密文，
// 不会把解锁所需的信息弄丢。

const (
	// vaultPrefix 标记一个已经用数据密钥加密的字段。开头的控制字符保证它不会和用户填写的密码撞上。
	vaultPrefix = "\x01vault1:"

	lockFileName    = "lock.json"
	minLockPassword = 4
	maxLockPassword = 128
	// 连续输错这么多次后开始限速：30 秒起，每多错一次翻倍，最长 16 分钟
	lockFreeFails = 5
)

var (
	ErrWrongPassword = errors.New("启动密码不正确")
	ErrLocked        = errors.New("程序已锁定，请先输入启动密码")
	ErrLockFileGone  = errors.New("找不到解锁所需的 lock.json，已保存的密码和密钥无法解密")
)

// RetryError 表示输错次数过多，要等到 At（毫秒时间戳）之后才能再试。
type RetryError struct{ At int64 }

func (e *RetryError) Error() string {
	wait := time.Until(time.UnixMilli(e.At)).Round(time.Second)
	if wait < time.Second {
		wait = time.Second
	}
	return fmt.Sprintf("输错次数过多，请在 %s 后再试", wait)
}

type lockFile struct {
	Version int `json:"version"`
	// Salt 与 Wrapped：用启动密码（scrypt）加密后的数据密钥。Check 用来确认一把数据密钥是不是这份数据的。
	Salt    string `json:"salt"`
	Wrapped string `json:"wrapped"`
	Check   string `json:"check"`
	SetAt   int64  `json:"setAt"`
	// AutoLock 是空闲多少分钟后自动锁定，0 表示不自动锁定。
	AutoLock int   `json:"autoLock"`
	Fails    int   `json:"fails,omitempty"`
	RetryAt  int64 `json:"retryAt,omitempty"`
}

// LockInfo 是启动密码当前的状态。
type LockInfo struct {
	Enabled bool `json:"enabled"`
	// Locked 表示敏感字段还是密文（启动后还没有输入过正确的密码）。
	Locked bool `json:"locked"`
	// Missing 表示数据里有加密的字段，但 lock.json 不见了。
	Missing  bool  `json:"missing"`
	AutoLock int   `json:"autoLock"`
	RetryAt  int64 `json:"retryAt"`
	SetAt    int64 `json:"setAt"`
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealGCM(key, plain, aad []byte) ([]byte, error) {
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, aad), nil
}

func openGCM(key, data, aad []byte) ([]byte, error) {
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("密文过短")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], aad)
}

var (
	aadSecret = []byte("sshpilot/secret")
	aadKey    = []byte("sshpilot/vault-key")
)

// Sealed 判断一个敏感字段是否还是用数据密钥加密的密文。
func Sealed(v string) bool { return strings.HasPrefix(v, vaultPrefix) }

func vaultSeal(key []byte, plain string) (string, error) {
	if Sealed(plain) {
		return plain, nil
	}
	b, err := sealGCM(key, []byte(plain), aadSecret)
	if err != nil {
		return "", err
	}
	return vaultPrefix + base64.StdEncoding.EncodeToString(b), nil
}

func vaultOpen(key []byte, v string) (string, error) {
	if !Sealed(v) {
		return v, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, vaultPrefix))
	if err != nil {
		return "", err
	}
	plain, err := openGCM(key, b, aadSecret)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// sealVault 把所有还是明文的敏感字段用数据密钥加密，返回失败数量。
func sealVault(d *Data, key []byte) int {
	failed := 0
	eachSecret(d, func(p *string) {
		v, err := vaultSeal(key, *p)
		if err != nil {
			failed++
			return
		}
		*p = v
	})
	return failed
}

// openVault 解密所有用数据密钥加密的字段，返回失败数量。解不开的字段保持密文原样（不清空），
// 这样换回正确的 lock.json 后还能恢复。
func openVault(d *Data, key []byte) int {
	failed := 0
	eachSecret(d, func(p *string) {
		v, err := vaultOpen(key, *p)
		if err != nil {
			failed++
			return
		}
		*p = v
	})
	return failed
}

func countSealed(d *Data) int {
	n := 0
	eachSecret(d, func(p *string) {
		if Sealed(*p) {
			n++
		}
	})
	return n
}

func keyCheck(key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("sshpilot/vault-check"))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newLockFile(password string, key []byte) (*lockFile, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	kek, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	wrapped, err := sealGCM(kek, key, aadKey)
	if err != nil {
		return nil, err
	}
	return &lockFile{Version: 1, Salt: base64.StdEncoding.EncodeToString(salt), Wrapped: base64.StdEncoding.EncodeToString(wrapped),
		Check: keyCheck(key), SetAt: Now()}, nil
}

// unwrap 用启动密码解出数据密钥；密码不对时返回 ErrWrongPassword。
func (l *lockFile) unwrap(password string) ([]byte, error) {
	salt, err1 := base64.StdEncoding.DecodeString(l.Salt)
	wrapped, err2 := base64.StdEncoding.DecodeString(l.Wrapped)
	if err1 != nil || err2 != nil {
		return nil, errors.New("lock.json 已损坏")
	}
	kek, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	key, err := openGCM(kek, wrapped, aadKey)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return key, nil
}

func (s *Store) lockPath() string { return filepath.Join(s.dir, lockFileName) }

// readLockFile 读取 lock.json；文件损坏时使用备份的那一份。
func (s *Store) readLockFile() *lockFile {
	for _, path := range []string{s.lockPath(), s.lockPath() + ".bak"} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var l lockFile
		if json.Unmarshal(b, &l) == nil && l.Salt != "" && l.Wrapped != "" {
			return &l
		}
	}
	return nil
}

func (s *Store) writeLockFile() error {
	if s.readOnly || s.lock == nil {
		return nil
	}
	b, err := json.MarshalIndent(s.lock, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(s.lockPath(), b, 0o600); err != nil {
		return err
	}
	return WriteFileAtomic(s.lockPath()+".bak", b, 0o600)
}

func (s *Store) removeLockFiles() {
	_ = os.Remove(s.lockPath())
	_ = os.Remove(s.lockPath() + ".bak")
}

func (s *Store) lockedLocked() bool {
	if s.lock != nil {
		return s.vaultKey == nil
	}
	return s.sealed > 0
}

// LockEnabled 表示是否设置了启动密码。
func (s *Store) LockEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lock != nil
}

// Locked 表示敏感字段还是密文：设置了启动密码但还没有输入，或者 lock.json 丢失。
// 此时不能用保存的密码、密钥去连接服务器。
func (s *Store) Locked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lockedLocked()
}

func (s *Store) LockInfo() LockInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := LockInfo{Enabled: s.lock != nil, Locked: s.lockedLocked(), Missing: s.lock == nil && s.sealed > 0}
	if s.lock != nil {
		info.AutoLock, info.SetAt = s.lock.AutoLock, s.lock.SetAt
		if s.lock.RetryAt > Now() {
			info.RetryAt = s.lock.RetryAt
		}
	}
	return info
}

// VaultKey 返回数据密钥（没有设置启动密码或还没有解锁时为空），主窗口把它交给自己拉起的文件管理窗口。
func (s *Store) VaultKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.vaultKey...)
}

// verifyLocked 校验启动密码并返回数据密钥；连续输错会被限速。调用时已持有写锁。
func (s *Store) verifyLocked(password string) ([]byte, error) {
	if s.lock == nil {
		return nil, errors.New("还没有设置启动密码")
	}
	now := Now()
	if s.lock.RetryAt > now {
		return nil, &RetryError{At: s.lock.RetryAt}
	}
	key, err := s.lock.unwrap(password)
	if err != nil {
		if errors.Is(err, ErrWrongPassword) {
			s.lock.Fails++
			if over := s.lock.Fails - lockFreeFails; over >= 0 {
				s.lock.RetryAt = now + (30 * time.Second << min(over, 5)).Milliseconds()
			}
			_ = s.writeLockFile()
		}
		return nil, err
	}
	if s.lock.Fails != 0 || s.lock.RetryAt != 0 {
		s.lock.Fails, s.lock.RetryAt = 0, 0
		_ = s.writeLockFile()
	}
	return key, nil
}

func (s *Store) adoptKey(key []byte) {
	s.vaultKey = key
	s.secretErrors += openVault(s.data, key)
	s.sealed = countSealed(s.data)
}

// Unlock 用启动密码解密敏感字段。
func (s *Store) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		if s.sealed > 0 {
			return ErrLockFileGone
		}
		return nil
	}
	key, err := s.verifyLocked(password)
	if err != nil {
		return err
	}
	s.adoptKey(key)
	return nil
}

// UnlockWithKey 用主窗口交过来的数据密钥解锁（文件管理窗口使用）。
func (s *Store) UnlockWithKey(key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	if len(key) != 32 || keyCheck(key) != s.lock.Check {
		return ErrWrongPassword
	}
	s.adoptKey(key)
	return nil
}

// CheckPassword 只校验启动密码（界面锁定后重新进入时使用）。
func (s *Store) CheckPassword(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.verifyLocked(password)
	return err
}

func checkLockPassword(pw string) error {
	switch n := len([]rune(pw)); {
	case n < minLockPassword:
		return fmt.Errorf("启动密码至少 %d 位", minLockPassword)
	case n > maxLockPassword:
		return fmt.Errorf("启动密码最长 %d 位", maxLockPassword)
	}
	return nil
}

// SetPassword 设置启动密码（还没有设置时 old 被忽略），或者把它从 old 改成 password。
func (s *Store) SetPassword(old, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return errors.New("请在主窗口里设置启动密码")
	}
	if err := checkLockPassword(password); err != nil {
		return err
	}
	if s.lock == nil {
		if s.sealed > 0 {
			return ErrLockFileGone
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		l, err := newLockFile(password, key)
		if err != nil {
			return err
		}
		// 先写 lock.json 再保存数据：中途出错时最多是“有启动密码、数据还没加密”，不会出现解不开的数据
		s.lock, s.vaultKey = l, key
		if err := s.writeLockFile(); err != nil {
			s.lock, s.vaultKey = nil, nil
			s.removeLockFiles()
			return err
		}
		if err := s.saveLocked(); err != nil {
			s.lock, s.vaultKey = nil, nil
			s.removeLockFiles()
			return err
		}
		// 再保存一次，让 store.json.bak 里也不再留着没有加密的那一份
		return s.saveLocked()
	}
	if s.vaultKey == nil {
		return ErrLocked
	}
	if _, err := s.verifyLocked(old); err != nil {
		return err
	}
	l, err := newLockFile(password, s.vaultKey)
	if err != nil {
		return err
	}
	l.AutoLock = s.lock.AutoLock
	prev := s.lock
	s.lock = l
	if err := s.writeLockFile(); err != nil {
		s.lock = prev
		return err
	}
	return nil
}

// RemovePassword 取消启动密码：敏感字段恢复为只由系统加密。
func (s *Store) RemovePassword(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	if s.readOnly {
		return errors.New("请在主窗口里取消启动密码")
	}
	if s.vaultKey == nil {
		return ErrLocked
	}
	if _, err := s.verifyLocked(password); err != nil {
		return err
	}
	key := s.vaultKey
	s.vaultKey = nil
	if err := s.saveLocked(); err != nil {
		s.vaultKey = key
		return err
	}
	_ = s.saveLocked()
	s.lock = nil
	s.removeLockFiles()
	return nil
}

// SetAutoLock 设置空闲多少分钟后自动锁定（0 表示关闭）。
func (s *Store) SetAutoLock(minutes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("还没有设置启动密码")
	}
	s.lock.AutoLock = min(max(minutes, 0), 24*60)
	return s.writeLockFile()
}

// ResetLock 用于忘记启动密码：仍是密文的敏感字段已经无法恢复，清空它们并取消启动密码，返回清空的数量。
// 清空前把 store.json 和 lock.json 各留一份副本（*.before-reset），以后想起密码时还能把它们换回去。
func (s *Store) ResetLock() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return 0, errors.New("请在主窗口里操作")
	}
	if s.lock != nil && s.vaultKey != nil {
		return 0, errors.New("程序已经解锁，不需要重置；要取消启动密码请到「设置 → 安全」")
	}
	for _, path := range []string{s.path(), s.lockPath()} {
		if b, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".before-reset", b, 0o600)
		}
	}
	n := 0
	eachSecret(s.data, func(p *string) {
		if Sealed(*p) {
			*p = ""
			n++
		}
	})
	s.lock, s.vaultKey, s.sealed = nil, nil, 0
	s.removeLockFiles()
	if err := s.saveLocked(); err != nil {
		return n, err
	}
	return n, s.saveLocked()
}
