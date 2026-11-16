package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	maxRuns         = 300
	maxResultOutput = 32 * 1024
)

// Store 持有全部结构化数据，所有读写都通过 Read / Update 串行化。
type Store struct {
	mu           sync.RWMutex
	dir          string
	data         *Data
	secretErrors int
	// readOnly 为 true 时所有修改只保留在内存里，不写回磁盘（文件管理独立窗口使用，数据由主窗口负责保存）。
	readOnly bool
	modTime  time.Time

	// 启动密码（见 vault.go）：lock 不为空表示设置了启动密码；vaultKey 是解锁后得到的数据密钥；
	// sealed 是内存里还没有解密的敏感字段数量。
	lock     *lockFile
	vaultKey []byte
	sealed   int

	runsMu sync.Mutex
	runs   []*RunRecord
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	keyDir = dir
	s := &Store{dir: dir}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.loadRuns()
	return s, nil
}

// OpenReadOnly 打开另一个进程（主窗口）正在使用的数据目录：只读取，永远不写回。
func OpenReadOnly(dir string) (*Store, error) {
	keyDir = dir
	s := &Store{dir: dir, readOnly: true}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// ReloadIfChanged 在数据文件被其他进程改动后重新读取（只用于只读模式），返回是否发生了重新加载。
func (s *Store) ReloadIfChanged() bool {
	if !s.readOnly {
		return false
	}
	st, err := os.Stat(s.path())
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.ModTime().Equal(s.modTime) {
		return false
	}
	old := s.data
	if err := s.load(); err != nil {
		s.data = old
		return false
	}
	return true
}

func (s *Store) Dir() string { return s.dir }

// SecretErrors 返回加载时无法解密的敏感字段数量（例如数据目录从其他电脑复制而来）。
func (s *Store) SecretErrors() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secretErrors
}

func (s *Store) path() string { return filepath.Join(s.dir, "store.json") }

func (s *Store) load() error {
	s.lock = s.readLockFile()
	if s.lock == nil || keyCheck(s.vaultKey) != s.lock.Check {
		s.vaultKey = nil
	}
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		s.data = &Data{Version: 1, Settings: DefaultSettings(), AIDefaultV2: true}
		ensureBuiltinScripts(s.data)
		ensureQuickCommands(s.data)
		return s.saveLocked()
	}
	if err != nil {
		return err
	}
	if st, err := os.Stat(s.path()); err == nil {
		s.modTime = st.ModTime()
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		bak, berr := os.ReadFile(s.path() + ".bak")
		if berr != nil || json.Unmarshal(bak, &d) != nil {
			return fmt.Errorf("数据文件损坏: %w", err)
		}
	}
	s.secretErrors = transformSecrets(&d, decryptSecret)
	if s.vaultKey != nil {
		s.secretErrors += openVault(&d, s.vaultKey)
	}
	s.sealed = countSealed(&d)
	migrateAIDefault(&d)
	d.Settings.fillDefaults()
	ensureBuiltinScripts(&d)
	ensureQuickCommands(&d)
	s.data = &d
	return nil
}

// Read 在读锁内访问数据；回调中不要保留指针。
func (s *Store) Read(fn func(d *Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(s.data)
}

// Update 在写锁内修改数据，回调返回 nil 时持久化。
func (s *Store) Update(fn func(d *Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(s.data); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.readOnly {
		return nil
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	var cp Data
	if err := json.Unmarshal(raw, &cp); err != nil {
		return err
	}
	// 设置了启动密码时，敏感字段先用数据密钥加密，再交给系统加密
	if s.vaultKey != nil {
		if n := sealVault(&cp, s.vaultKey); n > 0 {
			return fmt.Errorf("加密敏感数据失败（%d 项）", n)
		}
	}
	if n := transformSecrets(&cp, encryptSecret); n > 0 {
		return fmt.Errorf("加密敏感数据失败（%d 项）", n)
	}
	out, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(s.path()); err == nil {
		_ = os.WriteFile(s.path()+".bak", old, 0o600)
	}
	return WriteFileAtomic(s.path(), out, 0o600)
}

// Snapshot 返回数据的深拷贝（包含明文敏感字段），用于备份导出。
func (s *Store) Snapshot() (*Data, error) {
	s.mu.RLock()
	raw, err := json.Marshal(s.data)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	var cp Data
	return &cp, json.Unmarshal(raw, &cp)
}

// Replace 用备份中的数据整体替换当前数据。
func (s *Store) Replace(d *Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	migrateAIDefault(d)
	d.Settings.fillDefaults()
	ensureBuiltinScripts(d)
	ensureQuickCommands(d)
	s.data = d
	s.secretErrors = 0
	s.sealed = countSealed(d)
	return s.saveLocked()
}

// eachSecret 依次访问所有非空的敏感字段。
func eachSecret(d *Data, fn func(p *string)) {
	apply := func(p *string) {
		if *p != "" {
			fn(p)
		}
	}
	for _, s := range d.Servers {
		apply(&s.Password)
		apply(&s.SudoPassword)
		if s.Proxy != nil {
			apply(&s.Proxy.Password)
		}
	}
	for _, k := range d.Keys {
		apply(&k.PrivatePEM)
		apply(&k.Passphrase)
	}
	for _, c := range d.Certs {
		apply(&c.KeyPEM)
	}
	apply(&d.Settings.AI.APIKey)
	apply(&d.Settings.Embed.APIKey)
	if p := d.Settings.General.LastProxy; p != nil {
		apply(&p.Password)
	}
}

// transformSecrets 对所有敏感字段应用 fn，返回失败数量；失败的字段被清空。
func transformSecrets(d *Data, fn func(string) (string, error)) int {
	failed := 0
	eachSecret(d, func(p *string) {
		v, err := fn(*p)
		if err != nil {
			failed++
			*p = ""
			return
		}
		*p = v
	})
	return failed
}

const secretPrefix = "enc:v1:"

func encryptSecret(plain string) (string, error) {
	if strings.HasPrefix(plain, secretPrefix) {
		return plain, nil
	}
	b, err := protect([]byte(plain))
	if err != nil {
		return "", err
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(b), nil
}

func decryptSecret(v string) (string, error) {
	if !strings.HasPrefix(v, secretPrefix) {
		return v, nil // 旧版本的明文数据
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, secretPrefix))
	if err != nil {
		return "", err
	}
	plain, err := unprotect(b)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ---- 执行记录 ----

func (s *Store) runsPath() string { return filepath.Join(s.dir, "runs.json") }

func (s *Store) loadRuns() {
	b, err := os.ReadFile(s.runsPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.runs)
	// 上次退出时仍在执行的任务标记为中断
	for _, r := range s.runs {
		if r.Status == "running" {
			r.Status = "cancelled"
		}
	}
}

func (s *Store) saveRunsLocked() {
	if s.readOnly {
		return
	}
	b, err := json.Marshal(s.runs)
	if err == nil {
		_ = WriteFileAtomic(s.runsPath(), b, 0o600)
	}
}

func (s *Store) AddRun(r *RunRecord) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	s.runs = append([]*RunRecord{r}, s.runs...)
	if len(s.runs) > maxRuns {
		s.runs = s.runs[:maxRuns]
	}
	s.saveRunsLocked()
}

// FinishRun 写入执行结果并根据结果计算整体状态。
func (s *Store) FinishRun(id string, results []RunResult, cancelled bool) *RunRecord {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	for _, r := range s.runs {
		if r.ID != id {
			continue
		}
		ok := 0
		for i := range results {
			results[i].Output = TruncateMiddle(results[i].Output, maxResultOutput)
			if results[i].Error == "" && results[i].ExitCode == 0 {
				ok++
			}
		}
		r.Results = results
		r.FinishedAt = time.Now().UnixMilli()
		switch {
		case cancelled:
			r.Status = "cancelled"
		case ok == len(results):
			r.Status = "success"
		case ok == 0:
			r.Status = "failed"
		default:
			r.Status = "partial"
		}
		s.saveRunsLocked()
		cp := *r
		return &cp
	}
	return nil
}

func (s *Store) ListRuns(limit int) []RunRecord {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	out := make([]RunRecord, 0, min(limit, len(s.runs)))
	for _, r := range s.runs {
		if len(out) >= limit {
			break
		}
		cp := *r
		cp.Results = nil
		out = append(out, cp)
	}
	return out
}

func (s *Store) GetRun(id string) (RunRecord, bool) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			cp := *r
			cp.Results = slices.Clone(r.Results)
			return cp, true
		}
	}
	return RunRecord{}, false
}

// ---- 工具函数 ----

func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func Now() int64 { return time.Now().UnixMilli() }

func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TruncateMiddle 保留开头和结尾，截断中间部分。
func TruncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	return toValidUTF8(s[:head]) + fmt.Sprintf("\n…（已省略 %d 字节）…\n", len(s)-max) + toValidUTF8(s[len(s)-tail:])
}

func toValidUTF8(s string) string { return strings.ToValidUTF8(s, "") }
