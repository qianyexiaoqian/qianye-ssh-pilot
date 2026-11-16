package sshx_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"

	"sshpilot/internal/sshx"
	"sshpilot/internal/testsshd"
)

func TestModeAndNameHelpers(t *testing.T) {
	cases := map[os.FileMode]string{
		0o644:                                 "-rw-r--r--",
		os.ModeDir | 0o755:                    "drwxr-xr-x",
		os.ModeSymlink | 0o777:                "lrwxrwxrwx",
		os.ModeSetuid | 0o755:                 "-rwsr-xr-x",
		os.ModeDir | os.ModeSticky | 0o777:    "drwxrwxrwt",
		os.ModeSetgid | 0o644:                 "-rw-r-Sr--",
		os.ModeSocket | 0o600:                 "srw-------",
		os.ModeDevice | os.ModeCharDevice | 0: "c---------",
	}
	for mode, want := range cases {
		if got := sshx.LsMode(mode); got != want {
			t.Errorf("LsMode(%v) = %q, want %q", mode, got, want)
		}
	}
	if got := sshx.OctalPerm(os.ModeSetuid | 0o755); got != "4755" {
		t.Errorf("OctalPerm: %s", got)
	}
	if m, err := sshx.ParsePerm("0644"); err != nil || m != 0o644 {
		t.Errorf("ParsePerm 0644: %v %v", m, err)
	}
	if m, err := sshx.ParsePerm("1777"); err != nil || m != os.ModeSticky|0o777 {
		t.Errorf("ParsePerm 1777: %v %v", m, err)
	}
	for _, bad := range []string{"", "7", "888", "rwx", "77777"} {
		if _, err := sshx.ParsePerm(bad); err == nil {
			t.Errorf("ParsePerm(%q) should fail", bad)
		}
	}
	// 远程文件名不能把文件写到目标目录之外，也不能使用 Windows 的保留名
	names := map[string]string{
		"nginx.conf":       "nginx.conf",
		`..\..\evil.exe`:   ".._.._evil.exe",
		"a/b":              "a_b",
		"..":               "_",
		"C:evil":           "C_evil",
		"con":              "_con",
		"NUL.txt":          "_NUL.txt",
		"trailing. ":       "trailing",
		"报告 2026?.txt":     "报告 2026_.txt",
		"tab\tname":        "tab_name",
		"":                 "_",
		"normal name.log":  "normal name.log",
		"lpt1.backup.tar":  "_lpt1.backup.tar",
		"console.log":      "console.log",
		"<script>.sh":      "_script_.sh",
		"semi;colon&amp;$": "semi;colon&amp;$",
	}
	for in, want := range names {
		if got := sshx.SafeLocalName(in); got != want {
			t.Errorf("SafeLocalName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSFTPListAndTransfer(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})
	ctx := context.Background()

	cl, err := pool.SFTP(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := pool.SFTP(ctx, "s1"); again != cl {
		t.Fatal("SFTP client should be cached per connection")
	}
	home, err := cl.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// 准备本地文件：一个 3 MB 的随机文件和一个带子目录的文件夹
	local := t.TempDir()
	big := make([]byte, 3<<20)
	_, _ = rand.Read(big)
	bigPath := filepath.Join(local, "big.bin")
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(local, "site")
	for name, body := range map[string]string{"index.html": "<h1>hi</h1>", "conf/nginx.conf": "server {}", "conf/ssl/readme.txt": "证书目录"} {
		p := filepath.Join(tree, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(tree, "empty"), 0o755)

	// 上传单个文件：进度累计等于文件大小，临时文件被清理
	var sent int64
	remoteBig := path.Join(home, "big.bin")
	if err := sshx.UploadFile(ctx, cl, bigPath, remoteBig, func(n int64) { sent += n }); err != nil {
		t.Fatal(err)
	}
	if sent != int64(len(big)) {
		t.Fatalf("progress %d != %d", sent, len(big))
	}
	if _, err := cl.Stat(remoteBig + ".sshpilot-part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file should be gone: %v", err)
	}
	// 覆盖已有文件
	if err := os.WriteFile(bigPath, []byte("small now"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sshx.UploadFile(ctx, cl, bigPath, remoteBig, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := cl.Stat(remoteBig); err != nil || info.Size() != 9 {
		t.Fatalf("overwrite: %v %v", info, err)
	}
	_ = os.WriteFile(bigPath, big, 0o644)
	if err := sshx.UploadFile(ctx, cl, bigPath, remoteBig, nil); err != nil {
		t.Fatal(err)
	}

	// 上传文件夹：先展开计划，再逐项执行
	items, skipped, err := sshx.PlanUpload([]string{tree}, home)
	if err != nil || skipped != 0 {
		t.Fatalf("plan upload: %v skipped=%d", err, skipped)
	}
	var dirs, files int
	for _, it := range items {
		if it.IsDir {
			dirs++
			if err := cl.MkdirAll(it.Dst); err != nil {
				t.Fatal(err)
			}
			continue
		}
		files++
		if err := sshx.UploadFile(ctx, cl, it.Src, it.Dst, nil); err != nil {
			t.Fatal(err)
		}
	}
	if dirs != 4 || files != 3 {
		t.Fatalf("plan should contain 4 dirs and 3 files, got %d / %d: %+v", dirs, files, items)
	}

	// 列目录：文件夹在前，其余按名称排序
	list, err := sshx.ListDir(cl, home)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		if e.Name == ".ssh" {
			continue
		}
		names = append(names, e.Name)
	}
	if len(names) != 2 || names[0] != "site" || names[1] != "big.bin" {
		t.Fatalf("listing order: %v", names)
	}
	if !list[0].IsDir || list[0].Mode[0] != 'd' || list[len(list)-1].Size != int64(len(big)) || list[len(list)-1].ModTime == 0 {
		t.Fatalf("entry details: %+v", list)
	}

	// 下载：单个大文件内容一致；文件夹递归下载
	out := t.TempDir()
	var got int64
	if err := sshx.DownloadFile(ctx, cl, remoteBig, filepath.Join(out, "big.bin"), func(n int64) { got += n }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(out, "big.bin")); !bytes.Equal(data, big) || got != int64(len(big)) {
		t.Fatalf("downloaded content differs (%d bytes, progress %d)", len(data), got)
	}
	if _, err := os.Stat(filepath.Join(out, "big.bin.sshpilot-part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("local temp file should be gone")
	}
	plan, _, err := sshx.PlanDownload(cl, []string{path.Join(home, "site")}, func(string) string { return filepath.Join(out, "site-copy") })
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range plan {
		if it.IsDir {
			_ = os.MkdirAll(it.Dst, 0o755)
			continue
		}
		if err := sshx.DownloadFile(ctx, cl, it.Src, it.Dst, nil); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(out, "site-copy", "conf", "ssl", "readme.txt")); err != nil || string(b) != "证书目录" {
		t.Fatalf("nested download: %q %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(out, "site-copy", "empty")); err != nil || !st.IsDir() {
		t.Fatal("empty directories should be created")
	}

	// 取消：不留下临时文件，原有文件不受影响
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := sshx.UploadFile(cctx, cl, bigPath, remoteBig, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload: %v", err)
	}
	if info, err := cl.Stat(remoteBig); err != nil || info.Size() != int64(len(big)) {
		t.Fatalf("existing remote file must survive a cancelled upload: %v %v", info, err)
	}
	if _, err := cl.Stat(remoteBig + ".sshpilot-part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("remote temp file should be removed after cancel")
	}
	if err := sshx.DownloadFile(cctx, cl, remoteBig, filepath.Join(out, "never.bin"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 2 {
		t.Fatalf("cancelled download must not leave files behind: %v", entries)
	}

	// 在线编辑保存：内容被替换，不留下临时文件；新文件也能直接创建
	conf := path.Join(home, "site", "conf", "nginx.conf")
	if err := sshx.WriteText(cl, conf, []byte("server { listen 443; }\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(srv.Home, "site", "conf", "nginx.conf")); string(b) != "server { listen 443; }\n" {
		t.Fatalf("WriteText content: %q", b)
	}
	if err := sshx.WriteText(cl, path.Join(home, "site", "conf", "new.conf"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if confDir, _ := sshx.ListDir(cl, path.Join(home, "site", "conf")); len(confDir) != 3 {
		t.Fatalf("WriteText must not leave temp files: %+v", confDir)
	}

	// 传输到一半时取消：及时停止，同样不留下临时文件
	mctx, mcancel := context.WithCancel(ctx)
	var moved int64
	stopAfter := func(n int64) {
		if moved += n; moved > 1<<20 {
			mcancel()
		}
	}
	if err := sshx.DownloadFile(mctx, cl, remoteBig, filepath.Join(out, "half.bin"), stopAfter); !errors.Is(err, context.Canceled) {
		t.Fatalf("download cancelled midway: %v", err)
	}
	if moved >= int64(len(big)) {
		t.Fatalf("download should stop early, moved %d of %d bytes", moved, len(big))
	}
	if entries, _ := os.ReadDir(out); len(entries) != 2 {
		t.Fatalf("a download cancelled midway must not leave files behind: %v", entries)
	}
	mctx, mcancel = context.WithCancel(ctx)
	moved = 0
	if err := sshx.UploadFile(mctx, cl, bigPath, path.Join(home, "half.bin"), stopAfter); !errors.Is(err, context.Canceled) {
		t.Fatalf("upload cancelled midway: %v", err)
	}
	for _, name := range []string{"half.bin", "half.bin.sshpilot-part"} {
		if _, err := cl.Stat(path.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should not exist after a cancelled upload: %v", name, err)
		}
	}
	mcancel()

	// 连接重建后自动重新打开 SFTP
	pool.Invalidate("s1")
	err = pool.WithSFTP(ctx, "s1", func(c *sftp.Client) error {
		_, err := c.Stat(remoteBig)
		return err
	})
	if err != nil {
		t.Fatalf("sftp after reconnect: %v", err)
	}
}
