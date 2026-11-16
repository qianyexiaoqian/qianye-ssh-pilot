package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

type OpResult struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	Output     string `json:"output,omitempty"`
}

type KeyView struct {
	store.PrivateKey
	PublicKeyID string   `json:"publicKeyId"`
	UsedBy      []string `json:"usedBy"`
}

type BindingView struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	BoundAt    int64  `json:"boundAt"`
}

type PublicKeyView struct {
	store.PublicKey
	Bindings []BindingView `json:"bindings"`
}

func (a *App) ListKeys() []KeyView {
	out := []KeyView{}
	a.store.Read(func(d *store.Data) {
		for _, k := range d.Keys {
			v := KeyView{PrivateKey: k.Sanitized(), UsedBy: []string{}}
			for _, p := range d.PublicKeys {
				if p.PrivateKeyID == k.ID {
					v.PublicKeyID = p.ID
				}
			}
			for _, s := range d.Servers {
				if s.AuthType == "key" && s.KeyID == k.ID {
					v.UsedBy = append(v.UsedBy, s.ID)
				}
			}
			out = append(out, v)
		}
	})
	return out
}

type GenerateKeyRequest struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // ed25519 | rsa | ecdsa
	Bits       int    `json:"bits"`
	Comment    string `json:"comment"`
	Passphrase string `json:"passphrase"`
}

// addKey 保存私钥并同步创建派生的公钥记录。
func (a *App) addKey(name, pem, passphrase string, info sshx.KeyInfo) (KeyView, error) {
	var view KeyView
	err := a.store.Update(func(d *store.Data) error {
		for _, k := range d.Keys {
			if k.Fingerprint == info.Fingerprint {
				return fmt.Errorf("该私钥已存在：%s", k.Name)
			}
		}
		now := store.Now()
		k := &store.PrivateKey{ID: store.NewID(), Name: name, Type: info.Type, PrivatePEM: pem, Passphrase: passphrase,
			Encrypted: info.Encrypted, PublicKey: info.PublicKey, Fingerprint: info.Fingerprint, Comment: info.Comment, CreatedAt: now}
		d.Keys = append(d.Keys, k)
		var pub *store.PublicKey
		for _, p := range d.PublicKeys {
			if p.Fingerprint == info.Fingerprint {
				pub = p
			}
		}
		if pub == nil {
			pub = &store.PublicKey{ID: store.NewID(), CreatedAt: now}
			d.PublicKeys = append(d.PublicKeys, pub)
		}
		pub.Name, pub.Key, pub.Type, pub.Fingerprint, pub.Comment, pub.PrivateKeyID = name, info.PublicKey, info.Type, info.Fingerprint, info.Comment, k.ID
		view = KeyView{PrivateKey: k.Sanitized(), PublicKeyID: pub.ID, UsedBy: []string{}}
		return nil
	})
	return view, err
}

func (a *App) GenerateKey(req GenerateKeyRequest) (KeyView, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "key-" + time.Now().Format("20060102-1504")
	}
	comment := strings.TrimSpace(req.Comment)
	if comment == "" {
		comment = "sshpilot-" + name
	}
	pem, info, err := sshx.GenerateKey(req.Kind, req.Bits, comment, req.Passphrase)
	if err != nil {
		return KeyView{}, err
	}
	return a.addKey(name, pem, req.Passphrase, info)
}

type ImportKeyRequest struct {
	Name       string `json:"name"`
	PEM        string `json:"pem"`
	Passphrase string `json:"passphrase"`
}

func (a *App) ImportKey(req ImportKeyRequest) (KeyView, error) {
	info, err := sshx.InspectPrivateKey(req.PEM, req.Passphrase, "")
	if err != nil {
		return KeyView{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "imported-" + time.Now().Format("20060102-1504")
	}
	info.Comment = name
	if info.PublicKey != "" {
		info.PublicKey = strings.Join(strings.Fields(info.PublicKey)[:2], " ") + " " + name
	}
	pem := strings.TrimSpace(strings.ReplaceAll(req.PEM, "\r\n", "\n")) + "\n"
	return a.addKey(name, pem, req.Passphrase, info)
}

func (a *App) RenameKey(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}
	return a.store.Update(func(d *store.Data) error {
		k := d.Key(id)
		if k == nil {
			return errors.New("私钥不存在")
		}
		k.Name = name
		for _, p := range d.PublicKeys {
			if p.PrivateKeyID == id {
				p.Name = name
			}
		}
		return nil
	})
}

func (a *App) DeleteKey(id string) error {
	return a.store.Update(func(d *store.Data) error {
		if d.Key(id) == nil {
			return errors.New("私钥不存在")
		}
		var users []string
		for _, s := range d.Servers {
			if s.AuthType == "key" && s.KeyID == id {
				users = append(users, s.Name)
			}
		}
		if len(users) > 0 {
			return fmt.Errorf("以下服务器正在使用该私钥登录：%s。请先修改它们的登录方式", strings.Join(users, "、"))
		}
		var pubIDs []string
		for _, p := range d.PublicKeys {
			if p.PrivateKeyID == id {
				pubIDs = append(pubIDs, p.ID)
			}
		}
		d.Keys = slices.DeleteFunc(d.Keys, func(k *store.PrivateKey) bool { return k.ID == id })
		d.PublicKeys = slices.DeleteFunc(d.PublicKeys, func(p *store.PublicKey) bool { return slices.Contains(pubIDs, p.ID) })
		d.Bindings = slices.DeleteFunc(d.Bindings, func(b *store.KeyBinding) bool { return slices.Contains(pubIDs, b.PublicKeyID) })
		return nil
	})
}

// ExportPrivateKey 把私钥保存到用户选择的文件。
func (a *App) ExportPrivateKey(id string) (string, error) {
	if err := a.guard(); err != nil {
		return "", err
	}
	var pem, name string
	a.store.Read(func(d *store.Data) {
		if k := d.Key(id); k != nil {
			pem, name = k.PrivatePEM, k.Name
		}
	})
	if pem == "" {
		return "", errors.New("私钥不存在")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "导出私钥", DefaultFilename: "id_" + safeFileName(name)})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(pem), 0o600)
}

func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>| `, r) {
			return '_'
		}
		return r
	}, s)
}

// ---- 公钥与绑定 ----

func (a *App) ListPublicKeys() []PublicKeyView {
	out := []PublicKeyView{}
	a.store.Read(func(d *store.Data) {
		for _, p := range d.PublicKeys {
			v := PublicKeyView{PublicKey: *p, Bindings: []BindingView{}}
			for _, b := range d.Bindings {
				if b.PublicKeyID != p.ID {
					continue
				}
				name := b.ServerID
				if s := d.Server(b.ServerID); s != nil {
					name = s.Name
				}
				v.Bindings = append(v.Bindings, BindingView{ServerID: b.ServerID, ServerName: name, BoundAt: b.BoundAt})
			}
			out = append(out, v)
		}
	})
	return out
}

func (a *App) ImportPublicKey(name, line string) (store.PublicKey, error) {
	info, err := sshx.ParsePublicKey(line)
	if err != nil {
		return store.PublicKey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = info.Comment
	}
	if name == "" {
		name = info.Type + " 公钥"
	}
	var saved store.PublicKey
	err = a.store.Update(func(d *store.Data) error {
		for _, p := range d.PublicKeys {
			if p.Fingerprint == info.Fingerprint {
				return fmt.Errorf("该公钥已存在：%s", p.Name)
			}
		}
		p := &store.PublicKey{ID: store.NewID(), Name: name, Key: info.PublicKey, Type: info.Type, Fingerprint: info.Fingerprint,
			Comment: info.Comment, CreatedAt: store.Now()}
		d.PublicKeys = append(d.PublicKeys, p)
		saved = *p
		return nil
	})
	return saved, err
}

func (a *App) RenamePublicKey(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}
	return a.store.Update(func(d *store.Data) error {
		p := d.PublicKey(id)
		if p == nil {
			return errors.New("公钥不存在")
		}
		p.Name = name
		return nil
	})
}

func (a *App) DeletePublicKey(id string) error {
	return a.store.Update(func(d *store.Data) error {
		p := d.PublicKey(id)
		if p == nil {
			return errors.New("公钥不存在")
		}
		if p.PrivateKeyID != "" && d.Key(p.PrivateKeyID) != nil {
			return errors.New("该公钥由本地私钥派生，删除私钥时会一并删除")
		}
		d.PublicKeys = slices.DeleteFunc(d.PublicKeys, func(x *store.PublicKey) bool { return x.ID == id })
		d.Bindings = slices.DeleteFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.PublicKeyID == id })
		return nil
	})
}

func (a *App) setBinding(pubID, serverID string, bound bool) {
	_ = a.store.Update(func(d *store.Data) error {
		exists := slices.ContainsFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.PublicKeyID == pubID && b.ServerID == serverID })
		switch {
		case bound && !exists:
			d.Bindings = append(d.Bindings, &store.KeyBinding{PublicKeyID: pubID, ServerID: serverID, BoundAt: store.Now()})
		case !bound && exists:
			d.Bindings = slices.DeleteFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.PublicKeyID == pubID && b.ServerID == serverID })
		}
		return nil
	})
}

// loginBlob 返回服务器当前登录私钥对应的公钥主体（用于防止误删自己的登录公钥）。
func (a *App) loginBlob(serverID string) string {
	var line string
	a.store.Read(func(d *store.Data) {
		if s := d.Server(serverID); s != nil && s.AuthType == "key" {
			if k := d.Key(s.KeyID); k != nil {
				line = k.PublicKey
			}
		}
	})
	return sshx.KeyBlob(line)
}

// BindPublicKey 把公钥部署到服务器的 authorized_keys。
func (a *App) BindPublicKey(pubID string, serverIDs []string) []OpResult {
	var line string
	a.store.Read(func(d *store.Data) {
		if p := d.PublicKey(pubID); p != nil {
			line = p.Key
		}
	})
	return a.eachServer(serverIDs, func(ctx context.Context, id string) (string, error) {
		if line == "" {
			return "", errors.New("公钥不存在")
		}
		added, err := a.pool.AddAuthorizedKey(ctx, id, line)
		if err != nil {
			return "", err
		}
		a.setBinding(pubID, id, true)
		if added {
			return "已添加到 authorized_keys", nil
		}
		return "已存在，无需重复添加", nil
	})
}

// UnbindPublicKey 从服务器的 authorized_keys 中移除公钥。
func (a *App) UnbindPublicKey(pubID string, serverIDs []string) []OpResult {
	var line string
	a.store.Read(func(d *store.Data) {
		if p := d.PublicKey(pubID); p != nil {
			line = p.Key
		}
	})
	blob := sshx.KeyBlob(line)
	return a.eachServer(serverIDs, func(ctx context.Context, id string) (string, error) {
		if blob == "" {
			return "", errors.New("公钥不存在")
		}
		if blob == a.loginBlob(id) {
			return "", errors.New("这是该服务器当前的登录密钥，移除后将无法登录。请先切换登录密钥")
		}
		removed, err := a.pool.RemoveAuthorizedKey(ctx, id, blob)
		if err != nil {
			return "", err
		}
		a.setBinding(pubID, id, false)
		if removed {
			return "已从 authorized_keys 移除", nil
		}
		return "服务器上本来就没有该公钥", nil
	})
}

type AuthorizedKeyView struct {
	sshx.AuthorizedKey
	PublicKeyID   string `json:"publicKeyId"`
	PublicKeyName string `json:"publicKeyName"`
	IsLoginKey    bool   `json:"isLoginKey"`
}

// ScanAuthorizedKeys 读取服务器的 authorized_keys，并与本地公钥库对账（同步绑定关系）。
func (a *App) ScanAuthorizedKeys(serverID string) ([]AuthorizedKeyView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	keys, err := a.pool.ListAuthorizedKeys(ctx, serverID)
	if err != nil {
		return nil, err
	}
	login := a.loginBlob(serverID)
	out := []AuthorizedKeyView{}
	present := map[string]bool{}
	_ = a.store.Update(func(d *store.Data) error {
		for _, k := range keys {
			v := AuthorizedKeyView{AuthorizedKey: k, IsLoginKey: k.Blob == login}
			for _, p := range d.PublicKeys {
				if p.Fingerprint == k.Fingerprint {
					v.PublicKeyID, v.PublicKeyName = p.ID, p.Name
					present[p.ID] = true
					if !slices.ContainsFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.PublicKeyID == p.ID && b.ServerID == serverID }) {
						d.Bindings = append(d.Bindings, &store.KeyBinding{PublicKeyID: p.ID, ServerID: serverID, BoundAt: store.Now()})
					}
				}
			}
			out = append(out, v)
		}
		d.Bindings = slices.DeleteFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.ServerID == serverID && !present[b.PublicKeyID] })
		return nil
	})
	return out, nil
}

// RemoveAuthorizedKey 删除服务器 authorized_keys 中的某一条公钥（例如未知来源的公钥）。
func (a *App) RemoveAuthorizedKey(serverID, blob string) error {
	if blob == a.loginBlob(serverID) {
		return errors.New("这是该服务器当前的登录密钥，不能删除")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err := a.pool.RemoveAuthorizedKey(ctx, serverID, blob)
	return err
}

// RotateKey 把一组服务器的登录密钥从旧私钥切换到新私钥：部署新公钥 → 验证新密钥能登录 → 切换 → 可选移除旧公钥。
func (a *App) RotateKey(newKeyID string, serverIDs []string, removeOld bool) []OpResult {
	var newKey store.PrivateKey
	a.store.Read(func(d *store.Data) {
		if k := d.Key(newKeyID); k != nil {
			newKey = *k
		}
	})
	return a.eachServer(serverIDs, func(ctx context.Context, id string) (string, error) {
		if newKey.ID == "" {
			return "", errors.New("新私钥不存在")
		}
		oldBlob := a.loginBlob(id)
		if _, err := a.pool.AddAuthorizedKey(ctx, id, newKey.PublicKey); err != nil {
			return "", fmt.Errorf("部署新公钥失败: %w", err)
		}
		ep, err := a.resolveEndpoint(id)
		if err != nil {
			return "", err
		}
		ep.Password, ep.PrivateKey, ep.Passphrase = "", newKey.PrivatePEM, newKey.Passphrase
		cl, err := sshx.Dial(ctx, ep, nil, 12*time.Second)
		if err != nil {
			return "", fmt.Errorf("新密钥登录验证失败，已保持原登录方式: %w", err)
		}
		cl.Close()
		var pubID string
		_ = a.store.Update(func(d *store.Data) error {
			if s := d.Server(id); s != nil {
				s.AuthType, s.KeyID = "key", newKey.ID
			}
			for _, p := range d.PublicKeys {
				if p.PrivateKeyID == newKey.ID {
					pubID = p.ID
				}
			}
			return nil
		})
		a.pool.Invalidate(id)
		if pubID != "" {
			a.setBinding(pubID, id, true)
		}
		msg := "已切换到新密钥"
		if removeOld && oldBlob != "" && oldBlob != sshx.KeyBlob(newKey.PublicKey) {
			if _, err := a.pool.RemoveAuthorizedKey(ctx, id, oldBlob); err != nil {
				msg += "；移除旧公钥失败：" + err.Error()
			} else {
				msg += "，旧公钥已移除"
				var oldPub string
				a.store.Read(func(d *store.Data) {
					for _, p := range d.PublicKeys {
						if sshx.KeyBlob(p.Key) == oldBlob {
							oldPub = p.ID
						}
					}
				})
				if oldPub != "" {
					a.setBinding(oldPub, id, false)
				}
			}
		}
		return msg, nil
	})
}

// eachServer 并发地对多台服务器执行操作并收集结果。
func (a *App) eachServer(serverIDs []string, fn func(ctx context.Context, id string) (string, error)) []OpResult {
	ids := uniqueIDs(serverIDs)
	results := make([]OpResult, len(ids))
	var mu sync.Mutex
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	a.parallel(ids, func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		msg, err := fn(ctx, id)
		r := OpResult{ServerID: id, ServerName: a.serverName(id), OK: err == nil, Message: msg}
		if err != nil {
			r.Message = err.Error()
		}
		mu.Lock()
		results[index[id]] = r
		mu.Unlock()
	})
	return results
}
