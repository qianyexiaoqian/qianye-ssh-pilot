package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

type sftpEntry struct {
	client *sftp.Client
	owner  *Client // 所依附的 SSH 连接；连接重建后需要重新打开 SFTP
}

// SFTP 返回服务器的 SFTP 客户端。它建立在连接池的 SSH 连接之上并被缓存，连接重建后自动重新打开。
func (p *Pool) SFTP(ctx context.Context, serverID string) (*sftp.Client, error) {
	c, err := p.Get(ctx, serverID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	e := p.sftps[serverID]
	p.mu.Unlock()
	if e != nil && e.owner == c {
		return e.client, nil
	}
	cl, err := sftp.NewClient(c.Client, sftp.UseConcurrentWrites(true))
	if err != nil {
		return nil, fmt.Errorf("无法打开文件传输通道（服务器可能没有启用 SFTP）: %w", err)
	}
	p.mu.Lock()
	old := p.sftps[serverID]
	p.sftps[serverID] = &sftpEntry{client: cl, owner: c}
	p.mu.Unlock()
	if old != nil {
		old.client.Close()
	}
	return cl, nil
}

func (p *Pool) dropSFTP(serverID string) {
	p.mu.Lock()
	e := p.sftps[serverID]
	delete(p.sftps, serverID)
	p.mu.Unlock()
	if e != nil {
		e.client.Close()
	}
}

func sftpDead(err error) bool {
	if errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection lost") || strings.Contains(msg, "closed network connection")
}

// WithSFTP 用服务器的 SFTP 客户端执行 fn；通道已经失效时重新打开并重试一次（只用于可以安全重做的操作）。
func (p *Pool) WithSFTP(ctx context.Context, serverID string, fn func(cl *sftp.Client) error) error {
	for attempt := 0; ; attempt++ {
		cl, err := p.SFTP(ctx, serverID)
		if err != nil {
			return err
		}
		err = fn(cl)
		if err == nil || attempt > 0 || !sftpDead(err) {
			return err
		}
		p.dropSFTP(serverID)
	}
}

// FileEntry 是远程目录中的一项。
type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"` // ls 风格，例如 drwxr-xr-x
	Perm       string `json:"perm"` // 八进制权限，例如 755
	ModTime    int64  `json:"modTime"`
	IsDir      bool   `json:"isDir"`  // 目录，或指向目录的符号链接
	IsLink     bool   `json:"isLink"` // 符号链接
	LinkTarget string `json:"linkTarget"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
	Owner      string `json:"owner"`
	Group      string `json:"group"`
}

// LsMode 把文件模式格式化成 ls -l 的写法。
func LsMode(m os.FileMode) string {
	b := []byte("----------")
	switch {
	case m&os.ModeSymlink != 0:
		b[0] = 'l'
	case m.IsDir():
		b[0] = 'd'
	case m&os.ModeCharDevice != 0:
		b[0] = 'c'
	case m&os.ModeDevice != 0:
		b[0] = 'b'
	case m&os.ModeNamedPipe != 0:
		b[0] = 'p'
	case m&os.ModeSocket != 0:
		b[0] = 's'
	}
	const rwx = "rwxrwxrwx"
	for i := range 9 {
		if m&(1<<uint(8-i)) != 0 {
			b[i+1] = rwx[i]
		}
	}
	special := func(pos int, on bool, lower, upper byte) {
		if !on {
			return
		}
		if b[pos] == 'x' {
			b[pos] = lower
		} else {
			b[pos] = upper
		}
	}
	special(3, m&os.ModeSetuid != 0, 's', 'S')
	special(6, m&os.ModeSetgid != 0, 's', 'S')
	special(9, m&os.ModeSticky != 0, 't', 'T')
	return string(b)
}

// OctalPerm 返回八进制权限（包含 setuid / setgid / sticky 位）。
func OctalPerm(m os.FileMode) string {
	v := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		v |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		v |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		v |= 0o1000
	}
	return fmt.Sprintf("%o", v)
}

// ParsePerm 解析八进制权限字符串（如 644、0755、4755）。
func ParsePerm(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 4 {
		return 0, errors.New("权限应为 3 到 4 位八进制数字，例如 644 或 755")
	}
	var v uint32
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, errors.New("权限应为 3 到 4 位八进制数字，例如 644 或 755")
		}
		v = v*8 + uint32(c-'0')
	}
	m := os.FileMode(v & 0o777)
	if v&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if v&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if v&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m, nil
}

func entryOf(dir string, info os.FileInfo) FileEntry {
	e := FileEntry{Name: info.Name(), Path: path.Join(dir, info.Name()), Size: info.Size(), Mode: LsMode(info.Mode()), Perm: OctalPerm(info.Mode()),
		ModTime: info.ModTime().UnixMilli(), IsDir: info.IsDir(), IsLink: info.Mode()&os.ModeSymlink != 0, UID: -1, GID: -1}
	if st, ok := info.Sys().(*sftp.FileStat); ok && st != nil {
		e.UID, e.GID = int(st.UID), int(st.GID)
	}
	return e
}

// ListDir 列出远程目录：目录在前，其余按名称排序；符号链接会解析出目标以及它是否指向目录。
func ListDir(cl *sftp.Client, dir string) ([]FileEntry, error) {
	infos, err := cl.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(infos))
	for _, info := range infos {
		if info.Name() == "." || info.Name() == ".." {
			continue
		}
		e := entryOf(dir, info)
		if e.IsLink {
			e.LinkTarget, _ = cl.ReadLink(e.Path)
			if target, err := cl.Stat(e.Path); err == nil {
				e.IsDir, e.Size = target.IsDir(), target.Size()
			}
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b FileEntry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// ---- 传输 ----

// countWriter / countReader 统计传输的字节数，并在 ctx 取消后让下一次读写返回错误来中断传输。
// （不能靠关闭远程文件来中断：sftp.File 在传输期间持有读锁，Close 会一直等到传输结束。）
type countWriter struct {
	ctx context.Context
	w   io.Writer
	fn  func(int64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.w.Write(p)
	if n > 0 && c.fn != nil {
		c.fn(int64(n))
	}
	return n, err
}

type countReader struct {
	ctx context.Context
	r   io.Reader
	fn  func(int64)
}

func (c *countReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if n > 0 && c.fn != nil {
		c.fn(int64(n))
	}
	return n, err
}

const partSuffix = ".sshpilot-part"

// DownloadFile 把远程文件下载到本地路径。先写入临时文件，完成后再改名，中途失败不会留下残缺的文件。
func DownloadFile(ctx context.Context, cl *sftp.Client, remote, local string, onBytes func(int64)) error {
	src, err := cl.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	tmp := local + partSuffix
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(&countWriter{ctx: ctx, w: dst, fn: onBytes}, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if info, err := cl.Stat(remote); err == nil {
		_ = os.Chtimes(tmp, time.Now(), info.ModTime())
	}
	return os.Rename(tmp, local)
}

// UploadFile 把本地文件上传到远程路径。先写入临时文件再原子替换，保留已有文件的权限；
// 中途失败或取消时删除临时文件，原有文件不受影响。
func UploadFile(ctx context.Context, cl *sftp.Client, local, remote string, onBytes func(int64)) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()
	var keepMode os.FileMode
	if old, err := cl.Stat(remote); err == nil {
		if old.IsDir() {
			return fmt.Errorf("目标 %s 是一个目录", remote)
		}
		keepMode = old.Mode().Perm()
	}
	tmp := remote + partSuffix
	dst, err := cl.Create(tmp)
	if err != nil {
		return err
	}
	_, err = dst.ReadFromWithConcurrency(&countReader{ctx: ctx, r: src, fn: onBytes}, 0)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = cl.Remove(tmp)
		return err
	}
	if keepMode != 0 {
		_ = cl.Chmod(tmp, keepMode)
	}
	if err := cl.PosixRename(tmp, remote); err != nil {
		// 服务器不支持 posix-rename 扩展时退回「删除再改名」
		_ = cl.Remove(remote)
		if err2 := cl.Rename(tmp, remote); err2 != nil {
			_ = cl.Remove(tmp)
			return err2
		}
	}
	return nil
}

func writeAll(cl *sftp.Client, p string, data []byte) error {
	f, err := cl.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteText 保存在线编辑的文件。已有文件时先写临时文件、恢复原来的权限与属主，再原子替换，
// 网络中断不会留下只写了一半的配置文件；做不到（目录不可写、无权恢复属主等）时退回直接改写原文件。
// 路径是符号链接时写入它指向的文件，不会把链接本身替换掉。
func WriteText(cl *sftp.Client, p string, data []byte) error {
	target := p
	if real, err := cl.RealPath(p); err == nil && real != "" {
		target = real
	}
	info, err := cl.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return writeAll(cl, target, data)
	}
	tmp := target + partSuffix
	replace := func() error {
		if err := writeAll(cl, tmp, data); err != nil {
			return err
		}
		if err := cl.Chmod(tmp, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
			return err
		}
		if st, ok := info.Sys().(*sftp.FileStat); ok && st != nil {
			if err := cl.Chown(tmp, int(st.UID), int(st.GID)); err != nil {
				return err
			}
		}
		return cl.PosixRename(tmp, target)
	}
	if err := replace(); err != nil {
		_ = cl.Remove(tmp)
		return writeAll(cl, target, data)
	}
	return nil
}

// SafeLocalName 把远程文件名转换成可以安全用作本地文件名的形式：
// 去掉路径分隔符与 Windows 不允许的字符，避免恶意服务器用特殊文件名把文件写到目标目录之外。
func SafeLocalName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimRight(b.String(), " .")
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "_" + s
	}
	return s
}

// TransferItem 是一次传输中的一个文件或需要创建的目录。
type TransferItem struct {
	Src   string
	Dst   string
	Size  int64
	IsDir bool
}

// PlanDownload 展开要下载的远程路径（目录递归展开）；localFor 给出每个顶层项目保存到本地的路径。
// 目录中的符号链接：指向普通文件的按文件下载，指向目录的跳过（避免循环）；设备、套接字等特殊文件跳过。
func PlanDownload(cl *sftp.Client, remotes []string, localFor func(remote string) string) (items []TransferItem, skipped int, err error) {
	var walk func(remote, local string, top bool) error
	walk = func(remote, local string, top bool) error {
		info, err := cl.Lstat(remote)
		if err != nil {
			return &os.PathError{Op: "stat", Path: remote, Err: err}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := cl.Stat(remote)
			if err != nil || (target.IsDir() && !top) {
				skipped++
				return nil
			}
			info = target
		}
		switch {
		case info.IsDir():
			items = append(items, TransferItem{Src: remote, Dst: local, IsDir: true})
			children, err := cl.ReadDir(remote)
			if err != nil {
				return &os.PathError{Op: "readdir", Path: remote, Err: err}
			}
			for _, c := range children {
				if c.Name() == "." || c.Name() == ".." {
					continue
				}
				if err := walk(path.Join(remote, c.Name()), filepath.Join(local, SafeLocalName(c.Name())), false); err != nil {
					return err
				}
			}
		case info.Mode().IsRegular():
			items = append(items, TransferItem{Src: remote, Dst: local, Size: info.Size()})
		default:
			skipped++
		}
		return nil
	}
	for _, r := range remotes {
		r = path.Clean(r)
		if err := walk(r, localFor(r), true); err != nil {
			return nil, 0, err
		}
	}
	return items, skipped, nil
}

// PlanUpload 展开要上传的本地路径（目录递归展开），生成远程目标路径；符号链接与特殊文件跳过。
func PlanUpload(locals []string, remoteDir string) (items []TransferItem, skipped int, err error) {
	for _, l := range locals {
		root := filepath.Clean(l)
		base := path.Join(remoteDir, filepath.Base(root))
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			dst := base
			if rel != "." {
				dst = path.Join(base, filepath.ToSlash(rel))
			}
			switch {
			case d.IsDir():
				items = append(items, TransferItem{Src: p, Dst: dst, IsDir: true})
			case d.Type().IsRegular():
				info, err := d.Info()
				if err != nil {
					return err
				}
				items = append(items, TransferItem{Src: p, Dst: dst, Size: info.Size()})
			default:
				skipped++
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return items, skipped, nil
}
