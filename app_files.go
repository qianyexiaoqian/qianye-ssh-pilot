package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

const (
	maxEditSize     = 2 << 20 // 在线查看 / 编辑的文件大小上限
	maxSudoEditSize = 60000   // 通过 sudo 保存时内容经命令行参数传递，受单个参数长度限制
	maxTransfers    = 60
)

// ---- 目录与文件操作 ----

type FileListing struct {
	Path    string           `json:"path"`
	Parent  string           `json:"parent"` // 已经是根目录时为空
	Entries []sshx.FileEntry `json:"entries"`
}

// ownerNames 缓存服务器上 uid / gid 对应的名称（SFTP 只返回数字）。
type ownerNames struct {
	users, groups map[int]string
}

func parseIDNames(text string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(text, "\n") {
		name, id, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || name == "" {
			continue
		}
		if n, err := strconv.Atoi(id); err == nil {
			if _, dup := out[n]; !dup {
				out[n] = name
			}
		}
	}
	return out
}

func (a *App) ownersOf(ctx context.Context, serverID string) *ownerNames {
	a.filesMu.Lock()
	cached := a.owners[serverID]
	a.filesMu.Unlock()
	if cached != nil {
		return cached
	}
	names := &ownerNames{users: map[int]string{}, groups: map[int]string{}}
	const cmd = `(getent passwd || cat /etc/passwd) 2>/dev/null | cut -d: -f1,3; echo "@@"; (getent group || cat /etc/group) 2>/dev/null | cut -d: -f1,3`
	if res, err := a.pool.Exec(ctx, serverID, cmd, sshx.ExecOptions{Timeout: 10 * time.Second}); err == nil {
		users, groups, _ := strings.Cut(res.Stdout, "@@")
		names.users, names.groups = parseIDNames(users), parseIDNames(groups)
	}
	a.filesMu.Lock()
	a.owners[serverID] = names
	a.filesMu.Unlock()
	return names
}

// forgetOwners 在服务器配置变化后丢弃名称缓存（登录用户可能已经不同）。
func (a *App) forgetOwners(serverID string) {
	a.filesMu.Lock()
	delete(a.owners, serverID)
	a.filesMu.Unlock()
}

// fsError 把常见的文件系统错误换成容易理解的说法。
func fsError(err error, p string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("没有权限访问 %s（文件管理以登录用户的身份操作）", p)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s 不存在", p)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("操作超时，请检查网络后重试")
	}
	return err
}

func parentOf(p string) string {
	if p == "/" || p == "" {
		return ""
	}
	return path.Dir(p)
}

// canSudo 表示这台服务器以非 root 用户登录并启用了 sudo。
func (a *App) canSudo(serverID string) bool {
	ep, err := a.resolveEndpoint(serverID)
	return err == nil && ep.UseSudo && !ep.IsRoot()
}

// FilesList 列出服务器上的目录；dir 为空或 "~" 时列出登录用户的主目录。
func (a *App) FilesList(serverID, dir string) (FileListing, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out FileListing
	target := strings.TrimSpace(dir)
	err := a.pool.WithSFTP(ctx, serverID, func(cl *sftp.Client) error {
		p := target
		if p == "" || p == "~" {
			wd, err := cl.Getwd()
			if err != nil {
				return err
			}
			p = wd
		}
		real, err := cl.RealPath(p)
		if err != nil {
			return err
		}
		target = real
		entries, err := sshx.ListDir(cl, real)
		if err != nil {
			return err
		}
		out = FileListing{Path: real, Parent: parentOf(real), Entries: entries}
		return nil
	})
	if err != nil {
		return FileListing{}, fsError(err, target)
	}
	names := a.ownersOf(ctx, serverID)
	for i := range out.Entries {
		e := &out.Entries[i]
		e.Owner, e.Group = names.users[e.UID], names.groups[e.GID]
	}
	return out, nil
}

func validName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("请填写名称")
	case name == "." || name == "..":
		return errors.New("名称不能是 . 或 ..")
	case strings.ContainsAny(name, "/\x00"):
		return errors.New("名称不能包含 /")
	}
	return nil
}

func (a *App) fileOp(serverID, p string, fn func(cl *sftp.Client) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return fsError(a.pool.WithSFTP(ctx, serverID, fn), p)
}

// FilesMkdir 在 dir 下新建文件夹。
func (a *App) FilesMkdir(serverID, dir, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	p := path.Join(dir, strings.TrimSpace(name))
	return a.fileOp(serverID, p, func(cl *sftp.Client) error {
		if _, err := cl.Lstat(p); err == nil {
			return fmt.Errorf("已存在同名的文件或文件夹：%s", name)
		}
		return cl.Mkdir(p)
	})
}

// FilesCreate 在 dir 下新建空文件。
func (a *App) FilesCreate(serverID, dir, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	p := path.Join(dir, strings.TrimSpace(name))
	return a.fileOp(serverID, p, func(cl *sftp.Client) error {
		if _, err := cl.Lstat(p); err == nil {
			return fmt.Errorf("已存在同名的文件或文件夹：%s", name)
		}
		f, err := cl.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

// FilesRename 重命名（在同一目录内）。
func (a *App) FilesRename(serverID, from, newName string) error {
	if err := validName(newName); err != nil {
		return err
	}
	to := path.Join(path.Dir(from), strings.TrimSpace(newName))
	if to == from {
		return nil
	}
	return a.fileOp(serverID, from, func(cl *sftp.Client) error {
		if _, err := cl.Lstat(to); err == nil {
			return fmt.Errorf("已存在同名的文件或文件夹：%s", newName)
		}
		return cl.Rename(from, to)
	})
}

// FilesDelete 删除文件或文件夹（文件夹连同其中的内容一起删除）。
func (a *App) FilesDelete(serverID string, paths []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cl, err := a.pool.SFTP(ctx, serverID)
	if err != nil {
		return err
	}
	for _, p := range paths {
		p = path.Clean(p)
		if p == "/" || p == "." || p == "" {
			return errors.New("不能删除根目录")
		}
		if err := cl.RemoveAll(p); err != nil {
			return fmt.Errorf("删除 %s 失败: %w", path.Base(p), fsError(err, p))
		}
	}
	return nil
}

// FilesChmod 修改权限（八进制，例如 644、755）。
func (a *App) FilesChmod(serverID, p, perm string) error {
	mode, err := sshx.ParsePerm(perm)
	if err != nil {
		return err
	}
	return a.fileOp(serverID, p, func(cl *sftp.Client) error { return cl.Chmod(p, mode) })
}

// ---- 在线查看 / 编辑 ----

type FileText struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
	Perm    string `json:"perm"`
	ViaSudo bool   `json:"viaSudo"` // 登录用户没有权限，内容是通过 sudo 读取的
}

func checkText(data []byte) error {
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return errors.New("这是二进制文件，不能在线查看，请下载后打开")
	}
	if !utf8.Valid(data) {
		return errors.New("文件不是 UTF-8 编码，不能在线编辑，请下载后打开")
	}
	return nil
}

// FilesRead 读取文本文件用于在线查看或编辑。登录用户没有读取权限而服务器启用了 sudo 时，改用 sudo 读取。
func (a *App) FilesRead(serverID, p string) (FileText, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := FileText{Path: p}
	var data []byte
	err := a.pool.WithSFTP(ctx, serverID, func(cl *sftp.Client) error {
		info, err := cl.Stat(p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errors.New("这是一个文件夹")
		}
		if !info.Mode().IsRegular() {
			return errors.New("这不是普通文件，不能在线查看")
		}
		if info.Size() > maxEditSize {
			return fmt.Errorf("文件有 %s，超过在线查看的上限（2 MB），请下载后打开", humanSize(info.Size()))
		}
		out.Size, out.Perm = info.Size(), sshx.OctalPerm(info.Mode())
		f, err := cl.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, maxEditSize+1))
		return err
	})
	if errors.Is(err, os.ErrPermission) && a.canSudo(serverID) {
		text, rerr := a.pool.ReadFile(ctx, serverID, p, maxEditSize)
		if rerr != nil {
			return FileText{}, rerr
		}
		data, out.ViaSudo, err = []byte(text), true, nil
		out.Size = int64(len(data))
	}
	if err != nil {
		return FileText{}, fsError(err, p)
	}
	if err := checkText(data); err != nil {
		return FileText{}, err
	}
	out.Content = string(data)
	return out, nil
}

// FilesWrite 保存文本文件（保留属主与权限）。登录用户没有写入权限而服务器启用了 sudo 时，改用 sudo 写入。
func (a *App) FilesWrite(serverID, p, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if len(content) > maxEditSize {
		return errors.New("内容超过 2 MB，不能在线保存")
	}
	err := a.pool.WithSFTP(ctx, serverID, func(cl *sftp.Client) error { return sshx.WriteText(cl, p, []byte(content)) })
	if !errors.Is(err, os.ErrPermission) || !a.canSudo(serverID) {
		return fsError(err, p)
	}
	if len(content) > maxSudoEditSize {
		return fmt.Errorf("登录用户没有写入权限；通过 sudo 保存仅支持 %d KB 以内的文件", maxSudoEditSize/1000)
	}
	script := fmt.Sprintf("set -e\necho '%s' | base64 -d > %s\n", base64.StdEncoding.EncodeToString([]byte(content)), sshx.ShellQuote(p))
	res, rerr := a.pool.RunScript(ctx, serverID, sshx.Script{Content: script, Interpreter: "sh"}, sshx.ExecOptions{Privileged: true, Timeout: 30 * time.Second})
	if rerr != nil {
		return rerr
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("通过 sudo 保存失败: %s", strings.TrimSpace(res.Combined()))
	}
	return nil
}

// ---- 上传 / 下载 ----

// Transfer 是一次上传或下载任务（可以包含多个文件与文件夹）。
type Transfer struct {
	ID         string  `json:"id"`
	ServerID   string  `json:"serverId"`
	ServerName string  `json:"serverName"`
	Kind       string  `json:"kind"`   // upload | download
	Title      string  `json:"title"`  // 例如「nginx.conf」或「nginx.conf 等 3 项」
	Target     string  `json:"target"` // 上传：远程目录；下载：本地目录
	Total      int64   `json:"total"`
	Done       int64   `json:"done"`
	Files      int     `json:"files"`
	FilesDone  int     `json:"filesDone"`
	Current    string  `json:"current"`
	Skipped    int     `json:"skipped"` // 跳过的符号链接与特殊文件
	Status     string  `json:"status"`  // preparing | running | done | failed | cancelled
	Error      string  `json:"error"`
	Speed      float64 `json:"speed"` // 字节/秒
	StartedAt  int64   `json:"startedAt"`
	FinishedAt int64   `json:"finishedAt"`
}

type xferState struct {
	Transfer
	cancel   context.CancelFunc
	lastDone int64
	lastAt   time.Time
}

type transferHub struct {
	mu   sync.Mutex
	list []*xferState
}

func (h *transferHub) snapshot(t *xferState) Transfer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return t.Transfer
}

func (h *transferHub) update(t *xferState, fn func(t *xferState)) {
	h.mu.Lock()
	fn(t)
	h.mu.Unlock()
}

func transferTitle(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	return fmt.Sprintf("%s 等 %d 项", names[0], len(names))
}

// startTransfer 登记一个传输任务并在后台执行。plan 展开要传输的条目，each 传输其中一个文件。
func (a *App) startTransfer(kind, serverID, title, target string,
	plan func(cl *sftp.Client) ([]sshx.TransferItem, int, error),
	each func(ctx context.Context, cl *sftp.Client, it sshx.TransferItem, onBytes func(int64)) error) string {
	ctx, cancel := context.WithCancel(context.Background())
	t := &xferState{Transfer: Transfer{ID: store.NewID(), ServerID: serverID, ServerName: a.serverName(serverID), Kind: kind, Title: title, Target: target,
		Status: "preparing", StartedAt: store.Now()}, cancel: cancel, lastAt: time.Now()}
	h := a.xfers
	h.mu.Lock()
	h.list = append([]*xferState{t}, h.list...)
	if len(h.list) > maxTransfers {
		h.list = h.list[:maxTransfers]
	}
	h.mu.Unlock()
	a.emit("xfer:update", h.snapshot(t))

	finished := make(chan struct{})
	// 定时推送进度并计算速率
	go func() {
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-finished:
				return
			case now := <-tick.C:
				h.update(t, func(t *xferState) {
					if dt := now.Sub(t.lastAt).Seconds(); dt > 0 {
						cur := float64(t.Done-t.lastDone) / dt
						if t.Speed == 0 {
							t.Speed = cur
						} else {
							t.Speed = t.Speed*0.6 + cur*0.4
						}
					}
					t.lastDone, t.lastAt = t.Done, now
				})
				a.emit("xfer:update", h.snapshot(t))
			}
		}
	}()
	go func() {
		err := a.runTransfer(ctx, t, serverID, plan, each)
		close(finished)
		cancel()
		h.update(t, func(t *xferState) {
			t.FinishedAt, t.Speed, t.Current = store.Now(), 0, ""
			switch {
			case errors.Is(err, context.Canceled):
				t.Status = "cancelled"
			case err != nil:
				t.Status, t.Error = "failed", err.Error()
			default:
				t.Status = "done"
			}
		})
		a.emit("xfer:update", h.snapshot(t))
	}()
	return t.ID
}

func (a *App) runTransfer(ctx context.Context, t *xferState, serverID string,
	plan func(cl *sftp.Client) ([]sshx.TransferItem, int, error),
	each func(ctx context.Context, cl *sftp.Client, it sshx.TransferItem, onBytes func(int64)) error) error {
	cl, err := a.pool.SFTP(ctx, serverID)
	if err != nil {
		return err
	}
	items, skipped, err := plan(cl)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			return fsError(pe.Err, pe.Path)
		}
		return err
	}
	h := a.xfers
	h.update(t, func(t *xferState) {
		for _, it := range items {
			if !it.IsDir {
				t.Total += it.Size
				t.Files++
			}
		}
		t.Skipped, t.Status = skipped, "running"
	})
	onBytes := func(n int64) { h.update(t, func(t *xferState) { t.Done += n }) }
	for _, it := range items {
		if ctx.Err() != nil {
			return context.Canceled
		}
		h.update(t, func(t *xferState) { t.Current = it.Src })
		if err := each(ctx, cl, it, onBytes); err != nil {
			if ctx.Err() != nil {
				return context.Canceled
			}
			return fmt.Errorf("%s: %w", filepath.Base(filepath.FromSlash(it.Src)), fsError(err, it.Src))
		}
		if !it.IsDir {
			h.update(t, func(t *xferState) { t.FilesDone++ })
		}
	}
	return nil
}

// UploadStart 是发起上传的结果：Conflicts 非空表示目标目录已有同名项目，需要确认覆盖后重新发起。
type UploadStart struct {
	ID        string   `json:"id"`
	Conflicts []string `json:"conflicts"`
}

// FilesUpload 把本地文件 / 文件夹上传到 remoteDir。overwrite 为 false 且存在同名项目时不会开始传输。
func (a *App) FilesUpload(serverID, remoteDir string, locals []string, overwrite bool) (UploadStart, error) {
	res := UploadStart{Conflicts: []string{}}
	var names []string
	for _, l := range locals {
		if _, err := os.Stat(l); err != nil {
			return res, fmt.Errorf("本地文件不存在: %s", l)
		}
		names = append(names, filepath.Base(l))
	}
	if len(names) == 0 {
		return res, errors.New("没有选择要上传的文件")
	}
	if strings.TrimSpace(remoteDir) == "" {
		return res, errors.New("没有指定上传到哪个目录")
	}
	if !overwrite {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := a.pool.WithSFTP(ctx, serverID, func(cl *sftp.Client) error {
			res.Conflicts = res.Conflicts[:0]
			for _, n := range names {
				if _, err := cl.Lstat(path.Join(remoteDir, n)); err == nil {
					res.Conflicts = append(res.Conflicts, n)
				}
			}
			return nil
		})
		if err != nil {
			return res, fsError(err, remoteDir)
		}
		if len(res.Conflicts) > 0 {
			return res, nil
		}
	}
	res.ID = a.startTransfer("upload", serverID, transferTitle(names), remoteDir,
		func(*sftp.Client) ([]sshx.TransferItem, int, error) { return sshx.PlanUpload(locals, remoteDir) },
		func(ctx context.Context, cl *sftp.Client, it sshx.TransferItem, onBytes func(int64)) error {
			if it.IsDir {
				return cl.MkdirAll(it.Dst)
			}
			return sshx.UploadFile(ctx, cl, it.Src, it.Dst, onBytes)
		})
	return res, nil
}

// uniqueLocal 返回 dir 下不与已有文件、本批次其他项目冲突的路径：name、name (1)、name (2)…
func uniqueLocal(dir, name string, taken map[string]bool) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, candidate)
		key := strings.ToLower(p)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) && !taken[key] {
			taken[key] = true
			return p
		}
	}
}

// FilesDownload 把远程文件 / 文件夹下载到本地目录；本地已有同名项目时自动改名，不覆盖。
func (a *App) FilesDownload(serverID string, remotes []string, localDir string) (string, error) {
	if len(remotes) == 0 {
		return "", errors.New("没有选择要下载的文件")
	}
	if st, err := os.Stat(localDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("本地目录不存在: %s", localDir)
	}
	var names []string
	for _, r := range remotes {
		names = append(names, path.Base(path.Clean(r)))
	}
	taken := map[string]bool{}
	id := a.startTransfer("download", serverID, transferTitle(names), localDir,
		func(cl *sftp.Client) ([]sshx.TransferItem, int, error) {
			return sshx.PlanDownload(cl, remotes, func(remote string) string {
				return uniqueLocal(localDir, sshx.SafeLocalName(path.Base(remote)), taken)
			})
		},
		func(ctx context.Context, cl *sftp.Client, it sshx.TransferItem, onBytes func(int64)) error {
			if it.IsDir {
				return os.MkdirAll(it.Dst, 0o755)
			}
			return sshx.DownloadFile(ctx, cl, it.Src, it.Dst, onBytes)
		})
	return id, nil
}

// FilesTransfers 返回传输任务列表（最新的在前）。
func (a *App) FilesTransfers() []Transfer {
	h := a.xfers
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Transfer, 0, len(h.list))
	for _, t := range h.list {
		out = append(out, t.Transfer)
	}
	return out
}

// FilesCancel 取消正在进行的传输。
func (a *App) FilesCancel(id string) {
	h := a.xfers
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.list {
		if t.ID == id {
			t.cancel()
		}
	}
}

// FilesClearTransfers 清除已经结束的传输记录。
func (a *App) FilesClearTransfers() {
	h := a.xfers
	h.mu.Lock()
	defer h.mu.Unlock()
	h.list = slices.DeleteFunc(h.list, func(t *xferState) bool { return t.Status != "preparing" && t.Status != "running" })
}

// active 返回还在进行中的传输任务数量。
func (h *transferHub) active() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, t := range h.list {
		if t.Status == "running" || t.Status == "preparing" {
			n++
		}
	}
	return n
}

func (h *transferHub) cancelAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.list {
		t.cancel()
	}
}

// ---- 本地文件对话框 ----

// FilesPickUpload 打开对话框选择要上传的本地文件（可多选）或文件夹。
func (a *App) FilesPickUpload(folder bool) ([]string, error) {
	if folder {
		dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传的文件夹"})
		if err != nil || dir == "" {
			return []string{}, err
		}
		return []string{dir}, nil
	}
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传的文件（可多选）"})
	if err != nil || files == nil {
		return []string{}, err
	}
	return files, nil
}

// FilesPickSaveDir 打开对话框选择下载到哪个本地文件夹（默认是上次使用的文件夹或「下载」）。
func (a *App) FilesPickSaveDir() (string, error) {
	a.filesMu.Lock()
	def := a.lastSaveDir
	a.filesMu.Unlock()
	if def == "" {
		if home, err := os.UserHomeDir(); err == nil {
			def = filepath.Join(home, "Downloads")
		}
	}
	if st, err := os.Stat(def); err != nil || !st.IsDir() {
		def = ""
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "下载到哪个文件夹", DefaultDirectory: def})
	if err != nil || dir == "" {
		return "", err
	}
	a.filesMu.Lock()
	a.lastSaveDir = dir
	a.filesMu.Unlock()
	return dir, nil
}

// RevealLocal 在资源管理器中打开本地文件夹。
func (a *App) RevealLocal(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("本地路径不存在: %s", p)
	}
	if !st.IsDir() {
		p = filepath.Dir(p)
	}
	if goruntime.GOOS == "windows" {
		return exec.Command("explorer", p).Start()
	}
	runtime.BrowserOpenURL(a.ctx, "file://"+p)
	return nil
}
