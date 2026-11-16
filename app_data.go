package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 数据放在哪里：
//  1. 环境变量 SSHPILOT_DATA_DIR 指定的目录（测试、同时运行多个实例时使用）；
//  2. 默认：程序所在目录下的 data 文件夹，备份默认放在旁边的 backup 文件夹——整个程序文件夹可以一起拷走；
//  3. 程序目录不可写（例如装在 Program Files 里）时，退回 %AppData%\SSHPilot。
//
// 1.3 及更早的版本把数据放在 %AppData%\SSHPilot。第一次用新版本启动时，如果程序目录下还没有 data，
// 会把旧目录里的数据复制过来（旧目录原样保留，不删除）。
const envDataDir = "SSHPILOT_DATA_DIR"

const (
	dataModeProgram = "program"
	dataModeCustom  = "custom"
	dataModeAppData = "appdata"
)

type dataLocation struct {
	Dir  string
	Mode string
	// MigratedFrom 不为空表示这次启动时刚从这个旧目录复制了数据。
	MigratedFrom string
	// PreviousDir 不为空表示这次启动新建了一个空的数据目录，而之前的数据还留在这个目录里
	// （只把程序移到了新位置、没有带上 data 文件夹）。
	PreviousDir string
}

var (
	dataLocOnce sync.Once
	dataLoc     dataLocation
)

// legacyDataDir 是旧版本的数据目录 %AppData%\SSHPilot。
func legacyDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "SSHPilot")
}

// currentDataLocation 在进程内只解析一次（数据迁移也只在这时发生）。
func currentDataLocation() dataLocation {
	dataLocOnce.Do(func() {
		if isBindingsBuild {
			dataLoc = dataLocation{Dir: filepath.Join(os.TempDir(), "SSHPilot-bindings"), Mode: dataModeCustom}
			return
		}
		exeDir := ""
		if exe, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exe)
			// go test 编译出的测试程序：绝不能去碰真实的数据目录（更不能把它复制到临时目录里）
			if name := strings.ToLower(filepath.Base(exe)); strings.HasSuffix(name, ".test.exe") || strings.HasSuffix(name, ".test") {
				dataLoc = dataLocation{Dir: filepath.Join(os.TempDir(), "SSHPilot-test"), Mode: dataModeCustom}
				return
			}
		}
		dataLoc = resolveDataLocation(strings.TrimSpace(os.Getenv(envDataDir)), exeDir, legacyDataDir())
	})
	return dataLoc
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// dirWritable 通过真的创建一个文件来判断目录是否可写（只看权限位在 Windows 上不可靠）。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name) == nil
}

func resolveDataLocation(env, exeDir, legacy string) dataLocation {
	if env != "" {
		return dataLocation{Dir: env, Mode: dataModeCustom}
	}
	if exeDir != "" {
		dir := filepath.Join(exeDir, "data")
		if isDir(dir) {
			return dataLocation{Dir: dir, Mode: dataModeProgram}
		}
		// data 这个名字被一个文件占着时不去动它
		if _, err := os.Lstat(dir); os.IsNotExist(err) && dirWritable(exeDir) {
			if _, err := os.Stat(filepath.Join(legacy, "store.json")); err == nil && !sameDir(legacy, dir) {
				// 旧目录的数据以前已经搬到过别处，而且那份数据还在：旧目录里的内容早就过期了，不能再拿来用。
				// 这种情况是用户只移动了程序、没有带上 data 文件夹——新建一个空目录，并告诉用户数据在哪里。
				if prev := migratedTo(legacy); prev != "" && !sameDir(prev, dir) && isDir(prev) {
					if os.MkdirAll(dir, 0o700) == nil {
						return dataLocation{Dir: dir, Mode: dataModeProgram, PreviousDir: prev}
					}
					return dataLocation{Dir: legacy, Mode: dataModeAppData}
				}
				if migrateLegacyData(legacy, dir) == nil {
					return dataLocation{Dir: dir, Mode: dataModeProgram, MigratedFrom: legacy}
				}
				// 复制失败：继续使用旧目录，下次启动再试
				return dataLocation{Dir: legacy, Mode: dataModeAppData}
			}
			if os.MkdirAll(dir, 0o700) == nil {
				return dataLocation{Dir: dir, Mode: dataModeProgram}
			}
		}
	}
	return dataLocation{Dir: legacy, Mode: dataModeAppData}
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// 旧数据目录里需要带走的内容；WebView2 的缓存等不复制。
var (
	legacyFiles = []string{"store.json", "store.json.bak", "runs.json", "inspection.json", "learning.jsonl", "window.json"}
	legacyDirs  = []string{"memory", "skills", "sessions"}
)

type migrationNote struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	At   int64  `json:"at"`
}

// migrateLegacyData 把旧数据目录的内容复制到 dst。先复制到临时目录，全部成功后再改名，
// 这样中途失败（磁盘满、被杀掉）不会留下一个只有一半数据的 data 目录。
func migrateLegacyData(legacy, dst string) error {
	tmp := dst + ".migrating"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = os.RemoveAll(tmp)
		return err
	}
	for _, name := range legacyFiles {
		if err := copyFile(filepath.Join(legacy, name), filepath.Join(tmp, name)); err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
	}
	for _, name := range legacyDirs {
		if err := copyTree(filepath.Join(legacy, name), filepath.Join(tmp, name)); err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
	}
	note, _ := json.Marshal(migrationNote{From: legacy, At: time.Now().UnixMilli()})
	if err := os.WriteFile(filepath.Join(tmp, "migrated.json"), note, 0o600); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fail(err)
	}
	migrateWebviewPrefs(legacy, dst)
	// 在旧目录里留一个记号：它的内容已经搬到了 dst，以后不要再把这份（会越来越旧的）数据当成新的来用
	if to, err := json.Marshal(migrationNote{To: dst, At: time.Now().UnixMilli()}); err == nil {
		_ = os.WriteFile(filepath.Join(legacy, "migrated-to.json"), to, 0o600)
	}
	return nil
}

// migrateWebviewPrefs 把界面偏好（主题、面板开关等，保存在 WebView2 的 Local Storage 里）带到新位置。
// 只是锦上添花：旧程序还开着、文件被占用时复制不了，那就让界面偏好恢复默认。
func migrateWebviewPrefs(legacy, dst string) {
	sub := filepath.Join("EBWebView", "Default", "Local Storage", "leveldb")
	from := filepath.Join(webviewDataPath(legacy), sub)
	to := filepath.Join(webviewDataPath(dst), sub)
	entries, err := os.ReadDir(from)
	if err != nil {
		return
	}
	if os.MkdirAll(to, 0o700) != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if copyFile(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())) != nil {
			// 复制不完整的数据库比没有更糟：全部丢弃
			_ = os.RemoveAll(filepath.Join(webviewDataPath(dst), "EBWebView"))
			return
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		if strings.HasSuffix(d.Name(), ".tmp") || !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

// migratedFrom 返回这个数据目录是从哪里迁移来的（没有迁移过则为空）。
func migratedFrom(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, "migrated.json"))
	if err != nil {
		return ""
	}
	var n migrationNote
	if json.Unmarshal(b, &n) != nil {
		return ""
	}
	return n.From
}

// migratedTo 返回旧数据目录的内容已经搬到了哪里（没有搬过则为空）。
func migratedTo(legacy string) string {
	b, err := os.ReadFile(filepath.Join(legacy, "migrated-to.json"))
	if err != nil {
		return ""
	}
	var n migrationNote
	if json.Unmarshal(b, &n) != nil {
		return ""
	}
	return n.To
}

// backupDirectory 是导出备份时默认打开的目录：数据在程序目录下时是程序目录下的 backup，否则是数据目录里的 backup。
func backupDirectory(loc dataLocation) string {
	if loc.Mode == dataModeProgram {
		return filepath.Join(filepath.Dir(loc.Dir), "backup")
	}
	return filepath.Join(loc.Dir, "backup")
}
