package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/store"
)

func (a *App) GetSettings() store.Settings { return a.settings().Sanitized() }

// SaveSettings 保存设置；apiKey 为空时保留原有密钥，clearKey 为 true 时清除。
// 向量模型的 API Key 不经过这里（见 SaveEmbedKey），这里始终保留已保存的那一个。
func (a *App) SaveSettings(in store.Settings, apiKey string, clearKey bool) (store.Settings, error) {
	if in.AI.Provider != "openai" {
		in.AI.Provider = "anthropic"
	}
	if !slices.Contains([]string{"safe", "always", "auto"}, in.AI.ApprovalMode) {
		in.AI.ApprovalMode = "safe"
	}
	if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, in.AI.Effort) {
		in.AI.Effort = "medium"
	}
	in.AI.BaseURL = strings.TrimSpace(in.AI.BaseURL)
	in.AI.Model = strings.TrimSpace(in.AI.Model)
	in.AI.MaxTokens = min(max(in.AI.MaxTokens, 1024), 128000)
	in.AI.MaxSteps = min(max(in.AI.MaxSteps, 3), 100)
	in.AI.NudgeInterval = min(max(in.AI.NudgeInterval, 0), 50)
	in.AI.ReviewThreshold = min(max(in.AI.ReviewThreshold, 1), 50)
	in.Embed.BaseURL = strings.TrimSpace(in.Embed.BaseURL)
	in.Embed.Model = strings.TrimSpace(in.Embed.Model)
	in.Embed.Dimensions = min(max(in.Embed.Dimensions, 0), 8192)
	in.Embed.RecallCount = min(max(in.Embed.RecallCount, 1), 20)
	in.Embed.MinScore = min(max(in.Embed.MinScore, 0), 0.99)
	in.Terminal.FontSize = min(max(in.Terminal.FontSize, 10), 28)
	def := store.DefaultSettings().Terminal
	in.Terminal.KeyCopy = normalizeHotkey(in.Terminal.KeyCopy, def.KeyCopy)
	in.Terminal.KeyPaste = normalizeHotkey(in.Terminal.KeyPaste, def.KeyPaste)
	in.Terminal.KeyFind = normalizeHotkey(in.Terminal.KeyFind, def.KeyFind)
	if in.Terminal.KeyCopy == in.Terminal.KeyPaste || in.Terminal.KeyCopy == in.Terminal.KeyFind || in.Terminal.KeyPaste == in.Terminal.KeyFind {
		return store.Settings{}, errors.New("复制、粘贴、查找的快捷键不能相同")
	}
	in.General.Concurrency = min(max(in.General.Concurrency, 1), 32)
	in.General.StatusInterval = min(max(in.General.StatusInterval, 15), 3600)
	in.General.CommandTimeout = min(max(in.General.CommandTimeout, 10), 3600)
	in.General.CertWarnDays = min(max(in.General.CertWarnDays, 1), 120)
	in.General.MonitorInterval = min(max(in.General.MonitorInterval, 1), 60)
	in.General.InspectInterval = clampInspectInterval(in.General.InspectInterval)
	err := a.store.Update(func(d *store.Data) error {
		old, oldEmbed := d.Settings.AI.APIKey, d.Settings.Embed.APIKey
		d.Settings = in
		d.Settings.Embed.APIKey = oldEmbed
		switch {
		case apiKey != "":
			d.Settings.AI.APIKey = strings.TrimSpace(apiKey)
		case clearKey:
			d.Settings.AI.APIKey = ""
		default:
			d.Settings.AI.APIKey = old
		}
		return nil
	})
	if err != nil {
		return store.Settings{}, err
	}
	a.RefreshStatus()
	a.monitor.reconcile()
	// 向量模型的设置变了：换用新的模型并在后台重建索引
	a.agent.SyncIndex()
	return a.GetSettings(), nil
}

// SaveEmbedKey 保存或清除向量模型的 API Key（key 为空且 clear 为 false 时不做任何事）。
func (a *App) SaveEmbedKey(key string, clear bool) error {
	key = strings.TrimSpace(key)
	if key == "" && !clear {
		return nil
	}
	err := a.store.Update(func(d *store.Data) error {
		d.Settings.Embed.APIKey = key
		return nil
	})
	if err == nil {
		a.agent.SyncIndex()
	}
	return err
}

// normalizeHotkey 把快捷键整理成 "Ctrl+Shift+C" 的写法；写法无效时使用默认值。
// 必须带 Ctrl / Alt，或者是功能键、Insert（例如 Shift+Insert），避免占用正常输入的按键。
func normalizeHotkey(s, def string) string {
	var ctrl, alt, shift bool
	key := ""
	for _, part := range strings.Split(s, "+") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "":
			continue
		case "ctrl", "control":
			ctrl = true
		case "alt":
			alt = true
		case "shift":
			shift = true
		default:
			if key != "" {
				return def
			}
			key = part
		}
	}
	if key == "" || len(key) > 12 {
		return def
	}
	if len(key) == 1 {
		key = strings.ToUpper(key)
	} else {
		key = strings.ToUpper(key[:1]) + strings.ToLower(key[1:])
	}
	special := key == "Insert" || (len(key) >= 2 && key[0] == 'F' && strings.Trim(key[1:], "0123456789") == "")
	if !ctrl && !alt && !special {
		return def
	}
	out := ""
	if ctrl {
		out += "Ctrl+"
	}
	if alt {
		out += "Alt+"
	}
	if shift {
		out += "Shift+"
	}
	return out + key
}

// ExportBackup 把全部数据（含解密后的密钥、记忆与技能）用口令加密导出。
func (a *App) ExportBackup(password string) (string, error) {
	if err := a.guard(); err != nil {
		return "", err
	}
	data, err := a.store.Snapshot()
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	for k, v := range a.agent.Memory.Files() {
		files[k] = v
	}
	for k, v := range a.agent.Skills.Files() {
		files[k] = v
	}
	for k, v := range a.agent.Long.Files() {
		files[k] = v
	}
	raw, err := store.EncryptBackup(&store.Backup{Version: 1, CreatedAt: store.Now(), Data: data, Files: files}, password)
	if err != nil {
		return "", err
	}
	// 默认保存到备份目录（程序目录下的 backup），文件名带上时间
	dir := backupDirectory(a.dataLocation())
	_ = os.MkdirAll(dir, 0o700)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "导出加密备份", DefaultDirectory: dir, DefaultFilename: "sshpilot-backup-" + time.Now().Format("20060102-1504") + ".spbak",
		Filters: []runtime.FileFilter{{DisplayName: ProductName + " 备份 (*.spbak)", Pattern: "*.spbak"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, raw, 0o600)
}

// ImportBackup 从加密备份恢复数据（覆盖当前数据）。
func (a *App) ImportBackup(password string) (bool, error) {
	if err := a.guard(); err != nil {
		return false, err
	}
	opts := runtime.OpenDialogOptions{
		Title:   "选择备份文件",
		Filters: []runtime.FileFilter{{DisplayName: ProductName + " 备份 (*.spbak)", Pattern: "*.spbak"}},
	}
	if dir := backupDirectory(a.dataLocation()); isDir(dir) {
		opts.DefaultDirectory = dir
	}
	path, err := runtime.OpenFileDialog(a.ctx, opts)
	if err != nil || path == "" {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	b, err := store.DecryptBackup(raw, password)
	if err != nil {
		return false, err
	}
	if err := a.store.Replace(b.Data); err != nil {
		return false, err
	}
	// 备份里没有长期记忆时，恢复后也不应该留着当前的
	_ = os.Remove(filepath.Join(a.dataDir, "memory", "longterm.json"))
	for rel, content := range b.Files {
		clean := filepath.ToSlash(filepath.Clean(rel))
		if strings.Contains(clean, "..") || !(strings.HasPrefix(clean, "memory/") || strings.HasPrefix(clean, "skills/")) {
			continue
		}
		_ = store.WriteFileAtomic(filepath.Join(a.dataDir, filepath.FromSlash(clean)), []byte(content), 0o600)
	}
	a.terms.CloseAll()
	a.monitor.stopAll()
	a.pool.Close()
	a.RefreshStatus()
	// 长期记忆从备份里的文件重新读取；向量不在备份里，后台重新计算
	a.agent.Long.Reload()
	a.agent.Long.ResetIndex()
	a.agent.StartIndexing()
	return true, nil
}

type FileContent struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// PickTextFile 打开文件选择框并读取文本内容（用于导入私钥、证书等）。
func (a *App) PickTextFile(title string) (*FileContent, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil || path == "" {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > 4<<20 {
		return nil, errors.New("文件过大（超过 4MB）")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &FileContent{Path: path, Name: filepath.Base(path), Content: string(b)}, nil
}

// SaveTextFile 打开保存对话框并写入文本。
func (a *App) SaveTextFile(title, defaultName, content string) (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: title, DefaultFilename: defaultName})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(content), 0o600)
}
