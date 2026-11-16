package main

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/testsshd"
)

func waitTransfer(t *testing.T, a *App, id string) Transfer {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, tr := range a.FilesTransfers() {
			if tr.ID == id && tr.Status != "preparing" && tr.Status != "running" {
				return tr
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("transfer %s did not finish", id)
	return Transfer{}
}

func TestFileManager(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}

	// 留空时打开主目录
	home, err := a.FilesList(s.ID, "")
	if err != nil || home.Path == "" || home.Parent == "" {
		t.Fatalf("home listing: %+v %v", home, err)
	}
	dir := home.Path

	// 新建文件夹 / 文件、重命名，以及名称校验
	if err := a.FilesMkdir(s.ID, dir, " conf "); err != nil {
		t.Fatal(err)
	}
	if err := a.FilesMkdir(s.ID, dir, "conf"); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("duplicate folder: %v", err)
	}
	for _, bad := range []string{"", "  ", "..", "a/b"} {
		if err := a.FilesMkdir(s.ID, dir, bad); err == nil {
			t.Fatalf("name %q should be rejected", bad)
		}
	}
	if err := a.FilesCreate(s.ID, dir, "notes.txt"); err != nil {
		t.Fatal(err)
	}
	if err := a.FilesCreate(s.ID, dir, "notes.txt"); err == nil {
		t.Fatal("creating an existing file should fail")
	}
	if err := a.FilesRename(s.ID, path.Join(dir, "notes.txt"), "conf"); err == nil {
		t.Fatal("renaming onto an existing name should fail")
	}
	if err := a.FilesRename(s.ID, path.Join(dir, "notes.txt"), "readme.md"); err != nil {
		t.Fatal(err)
	}
	readme := path.Join(dir, "readme.md")

	// 在线编辑：写入后读回，中文不乱码
	text := "# 说明\nserver_name example.com;\n"
	if err := a.FilesWrite(s.ID, readme, text); err != nil {
		t.Fatal(err)
	}
	got, err := a.FilesRead(s.ID, readme)
	if err != nil || got.Content != text || got.Size != int64(len(text)) || got.ViaSudo {
		t.Fatalf("read back: %+v %v", got, err)
	}
	// 二进制文件、过大的文件、文件夹都不能在线打开
	_ = os.WriteFile(filepath.Join(srv.Home, "app.bin"), []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, 0o644)
	if _, err := a.FilesRead(s.ID, path.Join(dir, "app.bin")); err == nil || !strings.Contains(err.Error(), "二进制") {
		t.Fatalf("binary file: %v", err)
	}
	_ = os.WriteFile(filepath.Join(srv.Home, "huge.log"), bytes.Repeat([]byte("x"), maxEditSize+10), 0o644)
	if _, err := a.FilesRead(s.ID, path.Join(dir, "huge.log")); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("oversized file: %v", err)
	}
	if _, err := a.FilesRead(s.ID, path.Join(dir, "conf")); err == nil {
		t.Fatal("a folder cannot be opened as text")
	}
	if _, err := a.FilesRead(s.ID, path.Join(dir, "missing.txt")); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing file: %v", err)
	}

	// 列表：文件夹在前
	list, err := a.FilesList(s.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"conf", "app.bin", "huge.log", "readme.md"}) {
		t.Fatalf("listing: %v", names)
	}
	if _, err := a.FilesList(s.ID, path.Join(dir, "nope")); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing dir: %v", err)
	}
	if err := a.FilesChmod(s.ID, readme, "600"); err != nil {
		t.Fatal(err)
	}
	if err := a.FilesChmod(s.ID, readme, "rw-"); err == nil {
		t.Fatal("invalid permission should be rejected")
	}

	// 上传：目标已有同名文件时先提示，不会直接覆盖
	local := t.TempDir()
	newReadme := filepath.Join(local, "readme.md")
	_ = os.WriteFile(newReadme, []byte("新的内容\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(local, "pkg", "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(local, "pkg", "a.txt"), []byte("aaa"), 0o644)
	_ = os.WriteFile(filepath.Join(local, "pkg", "sub", "b.txt"), bytes.Repeat([]byte("b"), 200000), 0o644)
	up, err := a.FilesUpload(s.ID, dir, []string{newReadme, filepath.Join(local, "pkg")}, false)
	if err != nil || up.ID != "" || !slices.Equal(up.Conflicts, []string{"readme.md"}) {
		t.Fatalf("conflict check: %+v %v", up, err)
	}
	if got, _ := a.FilesRead(s.ID, readme); got.Content != text {
		t.Fatal("nothing may be uploaded before the overwrite is confirmed")
	}
	up, err = a.FilesUpload(s.ID, dir, []string{newReadme, filepath.Join(local, "pkg")}, true)
	if err != nil || up.ID == "" {
		t.Fatalf("upload: %+v %v", up, err)
	}
	tr := waitTransfer(t, a, up.ID)
	if tr.Status != "done" || tr.Kind != "upload" || tr.Files != 3 || tr.FilesDone != 3 || tr.Total != 200000+3+int64(len("新的内容\n")) || tr.Done != tr.Total || tr.Title != "readme.md 等 2 项" {
		t.Fatalf("upload transfer: %+v", tr)
	}
	if got, _ := a.FilesRead(s.ID, readme); got.Content != "新的内容\n" {
		t.Fatalf("overwritten content: %q", got.Content)
	}
	if b, err := os.ReadFile(filepath.Join(srv.Home, "pkg", "sub", "b.txt")); err != nil || len(b) != 200000 {
		t.Fatalf("nested upload: %d %v", len(b), err)
	}
	if _, err := a.FilesUpload(s.ID, dir, []string{filepath.Join(local, "ghost.txt")}, true); err == nil {
		t.Fatal("uploading a missing local file should fail")
	}

	// 下载：本地已有同名文件时自动改名，不覆盖
	out := t.TempDir()
	_ = os.WriteFile(filepath.Join(out, "readme.md"), []byte("本地原有"), 0o644)
	id, err := a.FilesDownload(s.ID, []string{readme, path.Join(dir, "pkg")}, out)
	if err != nil {
		t.Fatal(err)
	}
	tr = waitTransfer(t, a, id)
	if tr.Status != "done" || tr.Kind != "download" || tr.Files != 3 || tr.FilesDone != 3 || tr.Done != tr.Total {
		t.Fatalf("download transfer: %+v", tr)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "readme.md")); string(b) != "本地原有" {
		t.Fatal("existing local file must not be overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(out, "readme (1).md")); string(b) != "新的内容\n" {
		t.Fatalf("renamed download: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "pkg", "sub", "b.txt")); len(b) != 200000 {
		t.Fatalf("nested download: %d", len(b))
	}
	if _, err := a.FilesDownload(s.ID, []string{readme}, filepath.Join(out, "no-such-dir")); err == nil {
		t.Fatal("download into a missing local folder should fail")
	}
	// 远程文件不存在：任务以失败结束并说明原因
	id, err = a.FilesDownload(s.ID, []string{path.Join(dir, "gone.txt")}, out)
	if err != nil {
		t.Fatal(err)
	}
	if tr = waitTransfer(t, a, id); tr.Status != "failed" || !strings.Contains(tr.Error, "不存在") {
		t.Fatalf("failed transfer: %+v", tr)
	}
	if n := len(a.FilesTransfers()); n != 3 {
		t.Fatalf("expected 3 transfer records, got %d", n)
	}
	a.FilesClearTransfers()
	if n := len(a.FilesTransfers()); n != 0 {
		t.Fatalf("finished transfers should be cleared, %d left", n)
	}

	// 删除：文件夹连同内容一起删除；根目录不能删除
	if err := a.FilesDelete(s.ID, []string{path.Join(dir, "pkg"), readme}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.Home, "pkg")); !os.IsNotExist(err) {
		t.Fatal("folder should be removed recursively")
	}
	if err := a.FilesDelete(s.ID, []string{"/"}); err == nil {
		t.Fatal("deleting the root must be refused")
	}
	if err := a.FilesDelete(s.ID, []string{path.Join(dir, "gone.txt")}); err == nil {
		t.Fatal("deleting a missing file should report an error")
	}
}
