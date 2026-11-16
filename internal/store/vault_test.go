package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testServerPW = "r00t-Passw0rd!"
	testKeyPEM   = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"
	testAPIKey   = "sk-test-0123456789"
)

func seedStore(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = st.Update(func(d *Data) error {
		d.Servers = append(d.Servers, &Server{ID: "s1", Name: "web", Host: "10.0.0.1", Port: 22, Username: "root", AuthType: "password",
			Password: testServerPW, SudoPassword: "sudo-pw", Proxy: &ProxyConfig{Type: "socks5", Host: "1.2.3.4", Port: 1080, Username: "u", Password: "proxy-pw"}})
		d.Keys = append(d.Keys, &PrivateKey{ID: "k1", Name: "key", PrivatePEM: testKeyPEM, Passphrase: "key-phrase-xyz"})
		d.Certs = append(d.Certs, &Certificate{ID: "c1", Name: "cert", KeyPEM: "CERT-KEY"})
		d.Settings.AI.APIKey = testAPIKey
		d.Settings.Embed.APIKey = "embed-key"
		d.Settings.General.LastProxy = &ProxyConfig{Type: "http", Host: "h", Port: 8080, Password: "last-pw"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func secretsOf(st *Store) []string {
	var out []string
	st.Read(func(d *Data) { eachSecret(d, func(p *string) { out = append(out, *p) }) })
	return out
}

var plainSecrets = []string{testServerPW, "sudo-pw", "proxy-pw", testKeyPEM, "key-phrase-xyz", "CERT-KEY", testAPIKey, "embed-key", "last-pw"}

func assertPlain(t *testing.T, st *Store) {
	t.Helper()
	got := secretsOf(st)
	if strings.Join(got, "|") != strings.Join(plainSecrets, "|") {
		t.Fatalf("secrets are not the original plaintext: %q", got)
	}
}

// onDisk 读出磁盘上某个数据文件里的敏感字段（只解开系统加密这一层）。
func onDisk(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plainSecrets {
		if strings.Contains(string(b), strings.TrimSpace(strings.Split(s, "\n")[0])) {
			t.Fatalf("%s contains a secret in plain text", filepath.Base(path))
		}
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if n := transformSecrets(&d, decryptSecret); n != 0 {
		t.Fatalf("%d secrets failed to decrypt", n)
	}
	var out []string
	eachSecret(&d, func(p *string) { out = append(out, *p) })
	return out
}

func allSealed(vals []string) bool {
	for _, v := range vals {
		if !Sealed(v) {
			return false
		}
	}
	return len(vals) > 0
}

func TestStartupPasswordEncryptsSecrets(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	if st.LockEnabled() || st.Locked() {
		t.Fatal("a new store has no startup password")
	}
	if got := onDisk(t, filepath.Join(dir, "store.json")); allSealed(got) {
		t.Fatal("secrets should not be vault-encrypted before a password is set")
	}
	if err := st.SetPassword("", "abc"); err == nil {
		t.Fatal("a 3-character password should be rejected")
	}
	if err := st.SetPassword("", "开机密码1234"); err != nil {
		t.Fatal(err)
	}
	if !st.LockEnabled() || st.Locked() {
		t.Fatal("after setting the password the running program stays unlocked")
	}
	assertPlain(t, st)
	// 磁盘上的两份数据文件里，敏感字段都已经用数据密钥加密
	for _, name := range []string{"store.json", "store.json.bak"} {
		if got := onDisk(t, filepath.Join(dir, name)); !allSealed(got) || len(got) != len(plainSecrets) {
			t.Fatalf("%s: secrets not sealed: %q", name, got)
		}
	}
	lock, err := os.ReadFile(filepath.Join(dir, "lock.json"))
	if err != nil || strings.Contains(string(lock), "开机密码") {
		t.Fatalf("lock.json: %v %s", err, lock)
	}

	// 重新打开：没有密码时敏感字段是密文
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Locked() || !allSealed(secretsOf(st)) {
		t.Fatalf("reopened store should be locked: %q", secretsOf(st))
	}
	if info := st.LockInfo(); !info.Enabled || !info.Locked || info.Missing {
		t.Fatalf("lock info: %+v", info)
	}
	// 锁定期间的保存（例如记录主机密钥）不能破坏密文；锁定期间新填的密码也要保留
	err = st.Update(func(d *Data) error {
		d.Servers[0].HostKey = "ssh-ed25519 AAAA"
		d.Servers = append(d.Servers, &Server{ID: "s2", Name: "new", Password: "typed-while-locked"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _ = Open(dir)
	if err := st.Unlock("wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if !st.Locked() {
		t.Fatal("a wrong password must not unlock")
	}
	if err := st.Unlock("开机密码1234"); err != nil {
		t.Fatal(err)
	}
	if st.Locked() || st.SecretErrors() != 0 {
		t.Fatalf("unlock failed: locked=%v errors=%d", st.Locked(), st.SecretErrors())
	}
	var added string
	st.Read(func(d *Data) {
		added = d.Server("s2").Password
		if d.Servers[0].HostKey == "" {
			t.Fatal("update made while locked was lost")
		}
	})
	if added != "typed-while-locked" {
		t.Fatalf("secret typed while locked: %q", added)
	}
	_ = st.Update(func(d *Data) error { d.Servers = d.Servers[:1]; return nil })
	assertPlain(t, st)
	// 解锁后的保存仍然加密，包括新填的字段
	if got := onDisk(t, filepath.Join(dir, "store.json")); !allSealed(got) {
		t.Fatalf("secrets written after unlock are not sealed: %q", got)
	}
}

func TestStartupPasswordChangeAndRemove(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	if err := st.SetPassword("", "first-pass"); err != nil {
		t.Fatal(err)
	}
	key := st.VaultKey()
	if err := st.SetPassword("nope", "second-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("changing with a wrong old password: %v", err)
	}
	if err := st.SetAutoLock(15); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPassword("first-pass", "second-pass"); err != nil {
		t.Fatal(err)
	}
	if string(st.VaultKey()) != string(key) {
		t.Fatal("changing the password must keep the data key")
	}
	st, _ = Open(dir)
	if st.Unlock("first-pass") == nil {
		t.Fatal("the old password still works")
	}
	if err := st.Unlock("second-pass"); err != nil {
		t.Fatal(err)
	}
	assertPlain(t, st)
	if info := st.LockInfo(); info.AutoLock != 15 {
		t.Fatalf("auto-lock setting lost when changing the password: %+v", info)
	}
	if err := st.CheckPassword("second-pass"); err != nil {
		t.Fatal(err)
	}

	// 取消启动密码
	if err := st.RemovePassword("first-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("removing with a wrong password: %v", err)
	}
	if err := st.RemovePassword("second-pass"); err != nil {
		t.Fatal(err)
	}
	if st.LockEnabled() {
		t.Fatal("lock still enabled")
	}
	for _, name := range []string{"lock.json", "lock.json.bak"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s should be removed", name)
		}
	}
	for _, name := range []string{"store.json", "store.json.bak"} {
		for _, v := range onDisk(t, filepath.Join(dir, name)) {
			if Sealed(v) {
				t.Fatalf("%s still has sealed secrets after removing the password", name)
			}
		}
	}
	st, _ = Open(dir)
	if st.Locked() {
		t.Fatal("store should open unlocked")
	}
	assertPlain(t, st)
}

func TestStartupPasswordThrottle(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	if err := st.SetPassword("", "right-pass"); err != nil {
		t.Fatal(err)
	}
	st, _ = Open(dir)
	for i := range lockFreeFails {
		if err := st.Unlock("bad"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var retry *RetryError
	if err := st.Unlock("right-pass"); !errors.As(err, &retry) {
		t.Fatalf("after %d failures even the right password has to wait, got %v", lockFreeFails, err)
	}
	// 重启程序不能绕过等待
	st, _ = Open(dir)
	if err := st.Unlock("right-pass"); !errors.As(err, &retry) || st.LockInfo().RetryAt == 0 {
		t.Fatalf("restart should not reset the delay: %v", err)
	}
	st.lock.RetryAt = 1
	if err := st.Unlock("right-pass"); err != nil {
		t.Fatal(err)
	}
	if st.lock.Fails != 0 || st.LockInfo().RetryAt != 0 {
		t.Fatal("a successful unlock resets the counter")
	}
}

func TestStartupPasswordLockFileLossAndReset(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	if err := st.SetPassword("", "right-pass"); err != nil {
		t.Fatal(err)
	}
	// lock.json 损坏时用备份的那一份
	if err := os.WriteFile(filepath.Join(dir, "lock.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ = Open(dir)
	if err := st.Unlock("right-pass"); err != nil {
		t.Fatalf("lock.json.bak should be used: %v", err)
	}
	// 两份都没有了：数据解不开，但也不会被当成密码使用或被清空
	lockJSON, _ := os.ReadFile(filepath.Join(dir, "lock.json.bak"))
	os.Remove(filepath.Join(dir, "lock.json"))
	os.Remove(filepath.Join(dir, "lock.json.bak"))
	st, _ = Open(dir)
	if info := st.LockInfo(); !info.Locked || !info.Missing || info.Enabled {
		t.Fatalf("lock info without lock.json: %+v", info)
	}
	if err := st.Unlock("right-pass"); !errors.Is(err, ErrLockFileGone) {
		t.Fatalf("unlock without lock.json: %v", err)
	}
	if err := st.SetPassword("", "another-pass"); !errors.Is(err, ErrLockFileGone) {
		t.Fatalf("setting a new password over sealed data: %v", err)
	}
	_ = st.Update(func(d *Data) error { d.Servers[0].Notes = "x"; return nil })
	st, _ = Open(dir)
	if !allSealed(secretsOf(st)) {
		t.Fatal("sealed secrets must survive saves while the lock file is missing")
	}
	// 把 lock.json 放回去就恢复了
	if err := os.WriteFile(filepath.Join(dir, "lock.json"), lockJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ = Open(dir)
	if err := st.Unlock("right-pass"); err != nil {
		t.Fatal(err)
	}
	assertPlain(t, st)
	if _, err := st.ResetLock(); err == nil {
		t.Fatal("reset must be refused while unlocked")
	}

	// 忘记密码：重置会清空密文字段，保留其他数据，并留下可以换回去的副本
	st, _ = Open(dir)
	n, err := st.ResetLock()
	if err != nil || n != len(plainSecrets) {
		t.Fatalf("reset: n=%d err=%v", n, err)
	}
	if st.LockEnabled() || st.Locked() || len(secretsOf(st)) != 0 {
		t.Fatalf("after reset: enabled=%v locked=%v secrets=%q", st.LockEnabled(), st.Locked(), secretsOf(st))
	}
	st, _ = Open(dir)
	var name string
	st.Read(func(d *Data) { name = d.Servers[0].Name + "/" + d.Keys[0].Name })
	if st.Locked() || name != "web/key" {
		t.Fatalf("non-secret data should be kept: %q", name)
	}
	for _, f := range []string{"store.json", "lock.json"} {
		b, err := os.ReadFile(filepath.Join(dir, f+".before-reset"))
		if err != nil {
			t.Fatalf("%s.before-reset missing: %v", f, err)
		}
		_ = os.WriteFile(filepath.Join(dir, f), b, 0o600)
	}
	st, _ = Open(dir)
	if err := st.Unlock("right-pass"); err != nil {
		t.Fatalf("restoring the before-reset copies should bring the data back: %v", err)
	}
	assertPlain(t, st)
}

func TestReadOnlyStoreUnlocksWithKey(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	if err := st.SetPassword("", "right-pass"); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ro.Locked() {
		t.Fatal("a read-only store starts locked too")
	}
	if err := ro.UnlockWithKey(make([]byte, 32)); err == nil {
		t.Fatal("a wrong key must be rejected")
	}
	if err := ro.UnlockWithKey(st.VaultKey()); err != nil {
		t.Fatal(err)
	}
	assertPlain(t, ro)
	// 主窗口改了数据后重新读取，仍然保持解锁
	_ = st.Update(func(d *Data) error { d.Servers[0].Password = testServerPW; d.Servers[0].Notes = "changed"; return nil })
	ro.modTime = ro.modTime.Add(-1)
	if !ro.ReloadIfChanged() {
		t.Fatal("expected a reload")
	}
	if ro.Locked() {
		t.Fatal("reload lost the key")
	}
	assertPlain(t, ro)
	// 也可以直接输入密码；只读的一侧不写任何文件
	ro2, _ := OpenReadOnly(dir)
	before, _ := os.ReadFile(filepath.Join(dir, "lock.json"))
	if err := ro2.Unlock("bad"); !errors.Is(err, ErrWrongPassword) {
		t.Fatal(err)
	}
	if err := ro2.Unlock("right-pass"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "lock.json"))
	if string(before) != string(after) {
		t.Fatal("a read-only store must not write lock.json")
	}
	if err := ro2.SetPassword("right-pass", "new-pass-1"); err == nil {
		t.Fatal("changing the password from a read-only store should be refused")
	}
	// 主窗口取消了启动密码
	if err := st.RemovePassword("right-pass"); err != nil {
		t.Fatal(err)
	}
	ro.modTime = ro.modTime.Add(-1)
	ro.ReloadIfChanged()
	if ro.Locked() || ro.LockEnabled() {
		t.Fatal("removing the password should carry over on reload")
	}
	assertPlain(t, ro)
}
