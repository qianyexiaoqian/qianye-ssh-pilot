package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/store"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// 数据默认放在程序目录下的 data，备份默认放在旁边的 backup；旧版本留在 %AppData% 的数据第一次启动时复制过来。
func TestDataLocation(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "appdata", "SSHPilot")

	// 环境变量指定的目录优先
	custom := filepath.Join(root, "custom")
	if loc := resolveDataLocation(custom, filepath.Join(root, "prog"), legacy); loc.Dir != custom || loc.Mode != dataModeCustom {
		t.Fatalf("env override: %+v", loc)
	} else if got := backupDirectory(loc); got != filepath.Join(custom, "backup") {
		t.Fatalf("backup dir for a custom data dir: %s", got)
	}

	// 全新安装：在程序目录下创建 data
	prog := filepath.Join(root, "prog")
	if err := os.MkdirAll(prog, 0o700); err != nil {
		t.Fatal(err)
	}
	loc := resolveDataLocation("", prog, legacy)
	if loc.Dir != filepath.Join(prog, "data") || loc.Mode != dataModeProgram || loc.MigratedFrom != "" || !isDir(loc.Dir) {
		t.Fatalf("fresh install: %+v", loc)
	}
	if got := backupDirectory(loc); got != filepath.Join(prog, "backup") {
		t.Fatalf("backup dir next to the program: %s", got)
	}
	if entries, _ := os.ReadDir(prog); len(entries) != 1 {
		t.Fatalf("only the data folder should be created in the program dir: %v", entries)
	}

	// 旧版本的数据
	writeFiles(t, legacy, map[string]string{
		"store.json":               `{"version":1}`,
		"store.json.bak":           `{"version":0}`,
		"runs.json":                `[]`,
		"window.json":              `{"main":{"width":1200,"height":800}}`,
		"learning.jsonl":           "{}\n",
		"memory/MEMORY.md":         "web-01 是生产入口\n",
		"memory/longterm.json":     `{"version":1,"items":[]}`,
		"skills/my-skill/SKILL.md": "---\nname: my-skill\n---\n正文",
		"skills/.stats.json":       `{}`,
		"sessions/abc.json":        `{"id":"abc"}`,
		"sessions/half.json.tmp":   "写到一半",
		"qa/main-1.port":           "1234",
		"other.bin":                "不认识的文件不带走",
		// 界面偏好保存在 WebView2 的 Local Storage 里；其余缓存不复制
		"webview/EBWebView/Default/Local Storage/leveldb/000003.log": "theme=light",
		"webview/EBWebView/Default/Local Storage/leveldb/CURRENT":    "MANIFEST-000001",
		"webview/EBWebView/Default/Local Storage/leveldb/LOCK":       "",
		"webview/EBWebView/Default/Cache/data_0":                     "缓存",
	})

	// 程序目录下已经有 data：直接使用，不再迁移
	if loc := resolveDataLocation("", prog, legacy); loc.MigratedFrom != "" || exists(filepath.Join(loc.Dir, "store.json")) {
		t.Fatalf("an existing data folder must be used as is: %+v", loc)
	}

	// 升级：程序目录下还没有 data，旧目录里有数据
	prog2 := filepath.Join(root, "prog2")
	if err := os.MkdirAll(prog2, 0o700); err != nil {
		t.Fatal(err)
	}
	loc = resolveDataLocation("", prog2, legacy)
	data := filepath.Join(prog2, "data")
	if loc.Dir != data || loc.Mode != dataModeProgram || loc.MigratedFrom != legacy {
		t.Fatalf("upgrade: %+v", loc)
	}
	for _, name := range []string{"store.json", "store.json.bak", "runs.json", "window.json", "learning.jsonl", "memory/MEMORY.md", "memory/longterm.json",
		"skills/my-skill/SKILL.md", "skills/.stats.json", "sessions/abc.json", "migrated.json",
		"webview/EBWebView/Default/Local Storage/leveldb/000003.log", "webview/EBWebView/Default/Local Storage/leveldb/CURRENT"} {
		if !exists(filepath.Join(data, filepath.FromSlash(name))) {
			t.Fatalf("%s should have been copied", name)
		}
	}
	for _, name := range []string{"sessions/half.json.tmp", "qa", "other.bin", "webview/EBWebView/Default/Cache", "webview/EBWebView/Default/Local Storage/leveldb/LOCK"} {
		if exists(filepath.Join(data, filepath.FromSlash(name))) {
			t.Fatalf("%s must not be copied", name)
		}
	}
	if readFile(t, filepath.Join(data, "memory", "MEMORY.md")) != "web-01 是生产入口\n" || readFile(t, filepath.Join(data, "store.json")) != `{"version":1}` {
		t.Fatal("copied content differs")
	}
	if !exists(filepath.Join(legacy, "store.json")) || !exists(filepath.Join(legacy, "sessions", "abc.json")) {
		t.Fatal("the old data directory must be left untouched")
	}
	if exists(data + ".migrating") {
		t.Fatal("the temporary migration folder should be gone")
	}
	if got := migratedFrom(data); got != legacy {
		t.Fatalf("migration note: %q", got)
	}
	// 第二次启动：已经迁移过，不再提示
	if again := resolveDataLocation("", prog2, legacy); again.Dir != data || again.MigratedFrom != "" {
		t.Fatalf("second start: %+v", again)
	}
	if got := migratedTo(legacy); got != data {
		t.Fatalf("the old directory should remember where its data went: %q", got)
	}

	// 只把程序移到了新位置、没有带上 data：不能再把旧目录里早已过期的数据拿来用，新建空目录并指出数据在哪里
	writeFiles(t, data, map[string]string{"store.json": `{"version":2}`})
	prog3 := filepath.Join(root, "prog3")
	if err := os.MkdirAll(prog3, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := resolveDataLocation("", prog3, legacy)
	if moved.Dir != filepath.Join(prog3, "data") || moved.Mode != dataModeProgram || moved.MigratedFrom != "" || moved.PreviousDir != data {
		t.Fatalf("program moved without its data folder: %+v", moved)
	}
	if exists(filepath.Join(moved.Dir, "store.json")) {
		t.Fatal("stale data from the old directory must not be copied again")
	}
	if again := resolveDataLocation("", prog3, legacy); again.PreviousDir != "" {
		t.Fatalf("the hint is only for the first start in the new place: %+v", again)
	}
	// 搬过去的那份数据已经不在了（被删掉了）：旧目录是仅存的一份，照常迁移
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	prog4 := filepath.Join(root, "prog4")
	if err := os.MkdirAll(prog4, 0o700); err != nil {
		t.Fatal(err)
	}
	if again := resolveDataLocation("", prog4, legacy); again.MigratedFrom != legacy || readFile(t, filepath.Join(again.Dir, "store.json")) != `{"version":1}` || migratedTo(legacy) != again.Dir {
		t.Fatalf("migrating again after the migrated copy was deleted: %+v", again)
	}

	// 程序目录不可写：继续使用 %AppData%
	if loc := resolveDataLocation("", filepath.Join(root, "missing", "dir"), legacy); loc.Dir != legacy || loc.Mode != dataModeAppData {
		t.Fatalf("read-only program dir: %+v", loc)
	} else if got := backupDirectory(loc); got != filepath.Join(legacy, "backup") {
		t.Fatalf("backup dir in appdata mode: %s", got)
	}

	// 单实例锁：每个数据目录一把；数据在 %AppData% 时与旧版本共用同一把
	base := instanceID(dataLocation{Dir: legacy, Mode: dataModeAppData})
	a := instanceID(dataLocation{Dir: data, Mode: dataModeProgram})
	b := instanceID(dataLocation{Dir: filepath.Join(prog, "data"), Mode: dataModeProgram})
	if base != "8f6a2a4c-sshpilot-desktop" || a == base || a == b || a != instanceID(dataLocation{Dir: strings.ToUpper(data), Mode: dataModeProgram}) {
		t.Fatalf("instance ids: %s %s %s", base, a, b)
	}
	// WebView2 的目录跟着数据目录走
	if got := webviewDataPath(data); got != filepath.Join(data, "webview") {
		t.Fatalf("webview dir: %s", got)
	}
}

// 迁移中途失败时不能留下只有一半数据的 data 目录。
func TestDataMigrationIsAllOrNothing(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	// runs.json 是一个目录：复制到这里会失败（前面的 store.json 已经复制完了）
	writeFiles(t, legacy, map[string]string{"store.json": "{}", "sessions/a.json": "{}", "runs.json/oops": "x"})
	prog := filepath.Join(root, "prog")
	if err := os.MkdirAll(prog, 0o700); err != nil {
		t.Fatal(err)
	}
	loc := resolveDataLocation("", prog, legacy)
	if loc.Dir != legacy || loc.Mode != dataModeAppData || loc.MigratedFrom != "" {
		t.Fatalf("a failed migration should keep using the old directory: %+v", loc)
	}
	if entries, _ := os.ReadDir(prog); len(entries) != 0 {
		t.Fatalf("a failed migration must clean up after itself: %v", entries)
	}

	// data 这个名字被一个文件占着：不动它，继续使用旧目录
	prog2 := filepath.Join(root, "prog2")
	writeFiles(t, prog2, map[string]string{"data": "不是目录"})
	if loc := resolveDataLocation("", prog2, filepath.Join(root, "no-legacy")); loc.Mode != dataModeAppData || readFile(t, filepath.Join(prog2, "data")) != "不是目录" {
		t.Fatalf("a file named data must be left alone: %+v", loc)
	}
}

func waitIndex(t *testing.T, a *App, mode string) agent.IndexStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := a.GetMemoryIndex(); st.Mode == mode && !st.Indexing && st.Indexed == st.Total {
			return st
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("index not ready: %+v", a.GetMemoryIndex())
	return agent.IndexStatus{}
}

// 向量模型单独配置：默认预设是阿里云百炼，没有填 Key 时使用内置的关键词匹配；Key 加密保存且不会被普通的设置保存冲掉。
func TestEmbeddingSettingsAndLongMemory(t *testing.T) {
	a := newTestApp(t)
	s := a.GetSettings()
	if s.Embed.Preset != "aliyun" || s.Embed.BaseURL != store.AliyunEmbedBaseURL || s.Embed.Model != store.AliyunEmbedModel || s.Embed.RecallCount != 6 || s.Embed.HasAPIKey {
		t.Fatalf("default embedding settings: %+v", s.Embed)
	}
	if st := a.GetMemoryIndex(); st.Mode != "lexical" || st.Threshold <= 0 {
		t.Fatalf("without a key the built-in keyword matching is used: %+v", st)
	}
	if _, err := a.TestEmbedding(); err == nil {
		t.Fatal("testing an unconfigured embedding model should fail with a hint")
	}
	if err := a.ReindexMemory(); err == nil {
		t.Fatal("there is nothing to rebuild without an embedding model")
	}
	info := a.GetAppInfo()
	if info.DataMode != dataModeCustom || info.BackupDir != filepath.Join(a.dataDir, "backup") || info.MigratedFrom != "" || info.JustMigrated {
		t.Fatalf("app info: %+v", info)
	}

	// 先在关键词匹配下存一条，之后切换到向量模型时由后台补上向量
	srv, err := a.SaveServer(ServerInput{Name: "web-01", Host: "127.0.0.1", Port: 22, Username: "root", AuthType: "password", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.SaveLongMemory("", "Nginx 证书统一放在 /etc/nginx/ssl，由 certbot 续期", []string{srv.ID, "ghost-server"})
	if err != nil || len(first.ServerNames) != 1 || first.ServerNames[0] != "web-01" || first.Kind != "fact" || first.Source != "user" {
		t.Fatalf("save long memory: %+v %v", first, err)
	}
	if _, err := a.SaveLongMemory("", "Nginx 证书统一放在 /etc/nginx/ssl，由 certbot 续期", nil); err == nil {
		t.Fatal("an identical entry should be rejected")
	}

	mock := startMockModel(t)
	s.Embed.Preset, s.Embed.BaseURL, s.Embed.Model = "custom", mock+"/v1", "mock-embed"
	s.Embed.RecallCount, s.Embed.Dimensions = 99, -5
	saved, err := a.SaveSettings(s, "", false)
	if err != nil || saved.Embed.RecallCount != 20 || saved.Embed.Dimensions != 0 {
		t.Fatalf("embedding settings should be clamped: %+v %v", saved.Embed, err)
	}
	if err := a.SaveEmbedKey("  sk-embed-secret ", false); err != nil {
		t.Fatal(err)
	}
	if got := a.GetSettings().Embed; !got.HasAPIKey || got.APIKey != "" {
		t.Fatalf("the key must never be sent to the UI: %+v", got)
	}
	if raw := readFile(t, filepath.Join(a.dataDir, "store.json")); strings.Contains(raw, "sk-embed-secret") {
		t.Fatal("the embedding API key must be encrypted on disk")
	}
	// 其他页面保存设置（拿到的是不含 Key 的设置）不会把 Key 冲掉；空的 SaveEmbedKey 也不会
	if _, err := a.SaveSettings(a.GetSettings(), "", false); err != nil {
		t.Fatal(err)
	}
	_ = a.SaveEmbedKey("", false)
	if a.settings().Embed.APIKey != "sk-embed-secret" {
		t.Fatalf("embedding key lost: %q", a.settings().Embed.APIKey)
	}
	// 重新打开数据目录后 Key 仍能解密
	st2, err := store.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	st2.Read(func(d *store.Data) {
		if d.Settings.Embed.APIKey != "sk-embed-secret" || d.Settings.Embed.Model != "mock-embed" {
			t.Errorf("reloaded embedding settings: %+v", d.Settings.Embed)
		}
	})

	res, err := a.TestEmbedding()
	if err != nil || res.Dims != 64 || res.Model != "mock-embed" || res.Related <= res.Unrelated || res.Threshold <= res.Unrelated || res.Warning != "" {
		t.Fatalf("embedding test: %+v %v", res, err)
	}
	st := waitIndex(t, a, "semantic")
	if st.Model != "mock-embed" || st.Dims != 64 || st.Facts != 1 || st.Indexed < 1+len(a.ListSkills()) {
		t.Fatalf("index after switching to the embedding model: %+v", st)
	}
	if !exists(filepath.Join(a.dataDir, "memory", "vectors.bin")) {
		t.Fatal("vectors should be stored next to the memory files")
	}

	second, err := a.SaveLongMemory("", "数据盘挂载在 /data，日常占用 80% 属于正常", nil)
	if err != nil || len(second.Servers) != 0 {
		t.Fatalf("second entry: %+v %v", second, err)
	}
	if list := a.ListLongMemory("fact", ""); len(list) != 2 || list[0].Score != 0 {
		t.Fatalf("list: %+v", list)
	}
	found := a.ListLongMemory("fact", "nginx 的证书在哪个目录")
	if len(found) == 0 || found[0].ID != first.ID || found[0].Score <= 0 || found[0].Hits != 0 {
		t.Fatalf("search from the UI should rank the certificate entry first without counting as a hit: %+v", found)
	}
	// 修改内容与标注的服务器
	edited, err := a.SaveLongMemory(first.ID, "Nginx 证书统一放在 /etc/nginx/ssl，由 acme.sh 续期", []string{})
	if err != nil || !strings.Contains(edited.Text, "acme.sh") || len(edited.Servers) != 0 || edited.ID != first.ID {
		t.Fatalf("edit: %+v %v", edited, err)
	}
	if _, err := a.SaveLongMemory("missing-id", "随便", nil); err == nil {
		t.Fatal("editing a missing entry should fail")
	}
	if d := a.GetDashboard(); d.LongMemory.Facts != 2 || d.LongMemory.Mode != "semantic" {
		t.Fatalf("dashboard: %+v", d.LongMemory)
	}
	// 备份里带上长期记忆（向量可以重新计算，不放进备份）
	files := a.agent.Long.Files()
	if body := files["memory/longterm.json"]; len(files) != 1 || !strings.Contains(body, "acme.sh") || !strings.Contains(body, "/data") {
		t.Fatalf("backup files: %v", files)
	}
	// 重建索引
	if err := a.ReindexMemory(); err != nil {
		t.Fatal(err)
	}
	waitIndex(t, a, "semantic")

	// 清除 Key、去掉模型：回到内置的关键词匹配，已有内容仍然可以检索
	if err := a.SaveEmbedKey("", true); err != nil || a.GetSettings().Embed.HasAPIKey {
		t.Fatalf("clear key: %v", err)
	}
	off := a.GetSettings()
	off.Embed.Model = ""
	if _, err := a.SaveSettings(off, "", false); err != nil {
		t.Fatal(err)
	}
	if st := a.GetMemoryIndex(); st.Mode != "lexical" || st.Facts != 2 {
		t.Fatalf("after removing the embedding model: %+v", st)
	}
	if found := a.ListLongMemory("fact", "数据盘占用多少算正常"); len(found) == 0 || found[0].ID != second.ID {
		t.Fatalf("keyword search: %+v", found)
	}
	if err := a.DeleteLongMemory(second.ID); err != nil || len(a.ListLongMemory("fact", "")) != 1 {
		t.Fatalf("delete: %v", err)
	}

	// 智能体能用到长期记忆相关的工具，并能把服务器名称解析成 ID
	if names := strings.Join(a.agent.ToolNames(), " "); !strings.Contains(names, "recall") || !strings.Contains(names, "memory") {
		t.Fatalf("tools: %s", names)
	}
	if got := a.serverNameMap(); got[srv.ID] != "web-01" {
		t.Fatalf("server names: %v", got)
	}
}

// 技能可以固定、归档和恢复。
func TestSkillPinAndArchiveBindings(t *testing.T) {
	a := newTestApp(t)
	if err := a.SaveSkill("my-flow", "---\nname: my-flow\ndescription: 我的流程\n---\n\n## 步骤\n1. 做事"); err != nil {
		t.Fatal(err)
	}
	if err := a.PinSkill("my-flow", true); err != nil {
		t.Fatal(err)
	}
	if sk, _ := a.GetSkill("my-flow"); !sk.Pinned {
		t.Fatal("skill should be pinned")
	}
	if err := a.ArchiveSkill("my-flow"); err != nil {
		t.Fatal(err)
	}
	for _, s := range a.ListSkills() {
		if s.Name == "my-flow" {
			t.Fatal("an archived skill must leave the library")
		}
	}
	if arch := a.ListArchivedSkills(); len(arch) != 1 || arch[0].Name != "my-flow" || arch[0].ArchivedAt == 0 {
		t.Fatalf("archived: %+v", arch)
	}
	if err := a.RestoreSkill("my-flow"); err != nil || len(a.ListArchivedSkills()) != 0 {
		t.Fatalf("restore: %v", err)
	}
	if sk, err := a.GetSkill("my-flow"); err != nil || !sk.Pinned {
		t.Fatalf("a restored skill keeps its pin: %+v %v", sk, err)
	}
	if err := a.ArchiveSkill("my-flow"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteArchivedSkill("my-flow"); err != nil || len(a.ListArchivedSkills()) != 0 {
		t.Fatalf("delete archived: %v", err)
	}
	log := a.GetLearningLog(10)
	var actions []string
	for _, l := range log {
		actions = append(actions, l.Action)
	}
	if got := strings.Join(actions, ","); got != "archive,restore,archive,edit" {
		t.Fatalf("learning log: %s", got)
	}
}

// 测试程序自己解析数据目录时只会得到一个临时目录：绝不能碰真实的数据，也不能触发迁移。
func TestTestBinaryNeverResolvesRealData(t *testing.T) {
	loc := currentDataLocation()
	if loc.Mode != dataModeCustom || loc.MigratedFrom != "" || filepath.Base(loc.Dir) != "SSHPilot-test" {
		t.Fatalf("data location inside a test binary: %+v", loc)
	}
}
