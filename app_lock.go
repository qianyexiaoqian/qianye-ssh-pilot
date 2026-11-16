package main

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"sshpilot/internal/store"
)

// ---- 启动密码 ----
//
// 设置启动密码后有两种“锁定”：
//   - 刚启动、还没有输入密码：保存的密码、私钥、API Key 在内存里还是密文（store.Locked），
//     程序不连接任何服务器，后台任务也不启动；
//   - 运行中锁定（手动或空闲超时）：只挡住界面（uiLocked），已经在跑的终端和任务不受影响。

// envVaultKey 是主窗口交给文件管理窗口的数据密钥（十六进制），窗口进程用它直接解锁，不用再输入一次密码。
const envVaultKey = "SSHPILOT_VAULT_KEY"

type LockState struct {
	// Enabled 表示设置了启动密码；Locked 表示现在需要输入密码才能使用。
	Enabled bool `json:"enabled"`
	Locked  bool `json:"locked"`
	// Sealed 表示凭据还没有解密（启动后还没有输入过密码），此时界面只显示解锁页。
	Sealed bool `json:"sealed"`
	// Missing 表示数据里有加密的凭据，但解锁所需的 lock.json 不见了。
	Missing  bool  `json:"missing"`
	AutoLock int   `json:"autoLock"`
	RetryAt  int64 `json:"retryAt"`
	SetAt    int64 `json:"setAt"`
	// LegacyDir 不为空表示旧版本的数据目录里还留着一份没有被启动密码保护的数据。
	LegacyDir string `json:"legacyDir"`
}

func (a *App) lockState() LockState {
	info := a.store.LockInfo()
	st := LockState{Enabled: info.Enabled, Sealed: info.Locked, Missing: info.Missing, AutoLock: info.AutoLock, RetryAt: info.RetryAt, SetAt: info.SetAt}
	st.Locked = info.Locked || (info.Enabled && a.uiLocked.Load())
	if dir := migratedFrom(a.dataDir); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "store.json")); err == nil {
			st.LegacyDir = dir
		}
	}
	return st
}

func (a *App) GetLockState() LockState { return a.lockState() }

// guard 在程序锁定时拒绝查看密码、导出密钥、操作终端这类操作。
func (a *App) guard() error {
	if a.store.Locked() || (a.uiLocked.Load() && a.store.LockEnabled()) {
		return store.ErrLocked
	}
	return nil
}

func (a *App) lockChanged() LockState {
	st := a.lockState()
	a.emit("lock:changed", st)
	return st
}

// Unlock 输入启动密码：启动后第一次输入会解密凭据并启动后台任务，之后只是重新放开界面。
func (a *App) Unlock(password string) (LockState, error) {
	if a.store.Locked() {
		if err := a.store.Unlock(password); err != nil {
			return a.lockState(), err
		}
		a.afterUnlock()
	} else if a.store.LockEnabled() {
		if err := a.store.CheckPassword(password); err != nil {
			return a.lockState(), err
		}
	}
	a.uiLocked.Store(false)
	a.showFileWindows(true)
	return a.lockChanged(), nil
}

// afterUnlock 在凭据解密后调用：换上真正的 API Key，启动之前推迟的后台任务。
func (a *App) afterUnlock() {
	a.agent.SyncIndex()
	a.monitor.reconcile()
	a.RefreshStatus()
	// 后台任务只在真正运行的主窗口里启动（单元测试里没有界面，ctx 为空）
	if a.filesWindow == "" && a.ctx != nil {
		a.startBackground()
	}
}

// LockNow 立即锁定界面（需要先设置启动密码）。
func (a *App) LockNow() (LockState, error) {
	if !a.store.LockEnabled() {
		return a.lockState(), errors.New("还没有设置启动密码，请先到「设置 → 安全」设置")
	}
	a.uiLocked.Store(true)
	a.showFileWindows(false)
	return a.lockChanged(), nil
}

// SetLockPassword 设置启动密码（old 留空），或者修改它。
func (a *App) SetLockPassword(old, password string) (LockState, error) {
	if a.filesWindow != "" {
		return a.lockState(), errors.New("请在主窗口里设置启动密码")
	}
	if err := a.guard(); err != nil {
		return a.lockState(), err
	}
	if err := a.store.SetPassword(old, password); err != nil {
		return a.lockState(), err
	}
	return a.lockChanged(), nil
}

// RemoveLockPassword 取消启动密码。
func (a *App) RemoveLockPassword(password string) (LockState, error) {
	if err := a.guard(); err != nil {
		return a.lockState(), err
	}
	if err := a.store.RemovePassword(password); err != nil {
		return a.lockState(), err
	}
	a.uiLocked.Store(false)
	return a.lockChanged(), nil
}

// SetAutoLock 设置电脑空闲多少分钟后自动锁定，0 表示关闭。
func (a *App) SetAutoLock(minutes int) (LockState, error) {
	if err := a.guard(); err != nil {
		return a.lockState(), err
	}
	if err := a.store.SetAutoLock(minutes); err != nil {
		return a.lockState(), err
	}
	return a.lockChanged(), nil
}

type ResetLockResult struct {
	State LockState `json:"state"`
	// Wiped 是清空的凭据数量；Keys、Servers 是受影响的私钥和服务器数量。
	Wiped   int `json:"wiped"`
	Keys    int `json:"keys"`
	Servers int `json:"servers"`
}

// ResetLock 用于忘记启动密码：清空所有解不开的凭据并取消启动密码，服务器列表等其他数据保留。
func (a *App) ResetLock() (ResetLockResult, error) {
	var res ResetLockResult
	if a.filesWindow != "" {
		return res, errors.New("请在主窗口里操作")
	}
	if !a.store.Locked() {
		res.State = a.lockState()
		return res, errors.New("程序没有被锁定，不需要重置")
	}
	n, err := a.store.ResetLock()
	if err != nil {
		res.State = a.lockState()
		return res, err
	}
	res.Wiped = n
	// 私钥内容没有了的密钥条目不再有用：删掉，引用它们的服务器需要重新选择登录方式
	_ = a.store.Update(func(d *store.Data) error {
		gone := map[string]bool{}
		d.Keys = slices.DeleteFunc(d.Keys, func(k *store.PrivateKey) bool {
			if k.PrivatePEM != "" {
				return false
			}
			gone[k.ID] = true
			return true
		})
		res.Keys = len(gone)
		for _, p := range d.PublicKeys {
			if gone[p.PrivateKeyID] {
				p.PrivateKeyID = ""
			}
		}
		for _, s := range d.Servers {
			if (s.AuthType == "password" && s.Password == "") || (s.AuthType != "password" && gone[s.KeyID]) {
				res.Servers++
			}
			if gone[s.KeyID] {
				s.KeyID = ""
			}
		}
		return nil
	})
	a.uiLocked.Store(false)
	a.pool.Close()
	a.afterUnlock()
	res.State = a.lockChanged()
	return res, nil
}

// idleLockLoop 在电脑空闲（没有键盘鼠标操作）超过设定的时间后锁定界面。
func (a *App) idleLockLoop() {
	for range time.Tick(10 * time.Second) {
		info := a.store.LockInfo()
		if !info.Enabled || info.Locked || info.AutoLock <= 0 || a.uiLocked.Load() {
			continue
		}
		if idleSeconds() >= info.AutoLock*60 {
			_, _ = a.LockNow()
		}
	}
}

// unlockFromParent 在文件管理窗口进程启动时调用：用主窗口交过来的数据密钥解锁。
func (a *App) unlockFromParent() {
	raw := os.Getenv(envVaultKey)
	if raw == "" || !a.store.Locked() {
		return
	}
	if key, err := hex.DecodeString(raw); err == nil {
		_ = a.store.UnlockWithKey(key)
	}
}

// showFileWindows 在界面锁定时把已经打开的文件管理窗口藏起来，解锁后再显示。
func (a *App) showFileWindows(show bool) {
	a.winMu.Lock()
	pids := make([]int, 0, len(a.fileWins))
	for _, w := range a.fileWins {
		pids = append(pids, w.pid)
	}
	a.winMu.Unlock()
	for _, pid := range pids {
		showProcessWindows(pid, show)
	}
}
