package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/store"
)

const (
	ProductName = "QianYe SSH Pilot"
	AuthorName  = "Qian Ye"

	// envFilesWindow 的值是服务器 ID：进程以这台服务器的「文件管理」独立窗口的方式启动。
	envFilesWindow = "SSHPILOT_FILES_WINDOW"
	// envParentPID 是拉起独立窗口的主窗口进程，主窗口退出后独立窗口跟着关闭。
	envParentPID = "SSHPILOT_PARENT_PID"

	// 窗口可以缩到很小（界面会跟着调整布局），这里只保留一个还能操作的下限
	minWindowWidth  = 480
	minWindowHeight = 360
)

// ---- 窗口大小的记忆 ----

type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

func windowStatePath(dataDir string) string { return filepath.Join(dataDir, "window.json") }

func defaultWindowState(kind string) windowState {
	if kind == "files" {
		return windowState{Width: 1080, Height: 720}
	}
	return windowState{Width: 1400, Height: 880}
}

// loadWindowState 读取上次关闭时的窗口大小；kind 为 main 或 files。
func loadWindowState(dataDir, kind string) windowState {
	ws := defaultWindowState(kind)
	b, err := os.ReadFile(windowStatePath(dataDir))
	if err != nil {
		return ws
	}
	var all map[string]windowState
	if json.Unmarshal(b, &all) != nil {
		return ws
	}
	if s, ok := all[kind]; ok && s.Width >= minWindowWidth && s.Height >= minWindowHeight && s.Width <= 20000 && s.Height <= 20000 {
		ws = s
	}
	return ws
}

func saveWindowState(dataDir, kind string, ws windowState) {
	all := map[string]windowState{}
	if b, err := os.ReadFile(windowStatePath(dataDir)); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all[kind] = ws
	if b, err := json.Marshal(all); err == nil {
		_ = store.WriteFileAtomic(windowStatePath(dataDir), b, 0o600)
	}
}

// webviewDataPath 是 WebView2 存放界面偏好（主题、面板开关等）的目录：放在数据目录里的 webview 文件夹，各实例互不影响。
// 数据还在 %AppData%\SSHPilot 时沿用旧版本的位置（%AppData%\SSHPilot.exe），偏好不丢。
func webviewDataPath(dataDir string) string {
	if base, err := os.UserConfigDir(); err == nil && sameDir(dataDir, filepath.Join(base, "SSHPilot")) {
		return filepath.Join(base, "SSHPilot.exe")
	}
	return filepath.Join(dataDir, "webview")
}

// domReady 在界面加载完成后调用：窗口比屏幕还大时（例如换到了分辨率更小的屏幕），缩小到屏幕以内并居中。
func (a *App) domReady(ctx context.Context) {
	if runtime.WindowIsMaximised(ctx) || runtime.WindowIsFullscreen(ctx) {
		return
	}
	screens, err := runtime.ScreenGetAll(ctx)
	if err != nil {
		return
	}
	for _, s := range screens {
		if !s.IsCurrent || s.Size.Width <= 0 || s.Size.Height <= 0 {
			continue
		}
		w, h := runtime.WindowGetSize(ctx)
		// 高度留出任务栏的位置
		fitW, fitH := min(w, s.Size.Width), min(h, s.Size.Height-48)
		if fitW < w || fitH < h {
			runtime.WindowSetSize(ctx, max(fitW, minWindowWidth), max(fitH, minWindowHeight))
			runtime.WindowCenter(ctx)
		}
		return
	}
}

func (a *App) windowKind() string {
	if a.filesWindow != "" {
		return "files"
	}
	return "main"
}

// beforeClose 在窗口关闭前调用，返回 true 表示取消关闭。
func (a *App) beforeClose(ctx context.Context) bool {
	if a.filesWindow != "" && !a.forceQuit.Load() && a.xfers.active() > 0 {
		choice, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type:    runtime.QuestionDialog,
			Title:   "还有文件正在传输",
			Message: "关闭窗口会中断正在进行的上传或下载。\n\n确定要关闭吗？",
		})
		if err == nil && choice != "Yes" {
			return true
		}
	}
	a.rememberWindow(ctx)
	return false
}

func (a *App) rememberWindow(ctx context.Context) {
	if a.dataDir == "" || runtime.WindowIsMinimised(ctx) || runtime.WindowIsFullscreen(ctx) {
		return
	}
	ws := loadWindowState(a.dataDir, a.windowKind())
	ws.Maximised = runtime.WindowIsMaximised(ctx)
	if !ws.Maximised {
		if w, h := runtime.WindowGetSize(ctx); w >= minWindowWidth && h >= minWindowHeight {
			ws.Width, ws.Height = w, h
		}
	}
	saveWindowState(a.dataDir, a.windowKind(), ws)
}

// ---- 文件管理独立窗口（主窗口一侧） ----

type fileWindow struct {
	serverID string
	pid      int
}

// OpenFilesWindow 为一台服务器打开「文件管理」独立窗口；已经打开时把那个窗口提到前面。
func (a *App) OpenFilesWindow(serverID string) error {
	if a.filesWindow != "" {
		return errors.New("请从主窗口打开其他服务器的文件管理")
	}
	exists := false
	a.store.Read(func(d *store.Data) { exists = d.Server(serverID) != nil })
	if !exists {
		return errors.New("服务器不存在或已删除")
	}
	if a.ctx != nil && runtime.Environment(a.ctx).BuildType == "dev" {
		return errors.New("开发模式下不能打开独立窗口，请使用编译后的程序")
	}
	a.winMu.Lock()
	defer a.winMu.Unlock()
	if i := slices.IndexFunc(a.fileWins, func(w *fileWindow) bool { return w.serverID == serverID }); i >= 0 {
		// 窗口还在启动时可能暂时找不到，这种情况什么都不用做
		focusProcessWindow(a.fileWins[i].pid)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	// 明确告诉窗口进程使用同一个数据目录（两个进程共用 WebView2 的界面偏好，主题等设置才能同步）
	cmd.Env = append(os.Environ(), envFilesWindow+"="+serverID, envParentPID+"="+strconv.Itoa(os.Getpid()), envDataDir+"="+a.dataDir)
	if err := cmd.Start(); err != nil {
		return errors.New("无法打开文件管理窗口：" + err.Error())
	}
	w := &fileWindow{serverID: serverID, pid: cmd.Process.Pid}
	a.fileWins = append(a.fileWins, w)
	a.emit("fileswin:changed", a.fileWindowIDsLocked())
	go func() {
		_ = cmd.Wait()
		a.winMu.Lock()
		a.fileWins = slices.DeleteFunc(a.fileWins, func(x *fileWindow) bool { return x == w })
		ids := a.fileWindowIDsLocked()
		a.winMu.Unlock()
		a.emit("fileswin:changed", ids)
	}()
	return nil
}

func (a *App) fileWindowIDsLocked() []string {
	ids := make([]string, 0, len(a.fileWins))
	for _, w := range a.fileWins {
		ids = append(ids, w.serverID)
	}
	return ids
}

// FilesWindows 返回已经打开了文件管理窗口的服务器。
func (a *App) FilesWindows() []string {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	return a.fileWindowIDsLocked()
}

// ---- 文件管理独立窗口（窗口进程一侧） ----

// startFilesWindow 在独立窗口进程启动后调用：设置标题，跟随主窗口退出，跟随主窗口对数据的修改。
func (a *App) startFilesWindow() {
	runtime.WindowSetTitle(a.ctx, a.serverName(a.filesWindow)+" - 文件管理 - "+ProductName)
	go a.followParent()
	go func() {
		for range time.Tick(3 * time.Second) {
			if a.store.ReloadIfChanged() {
				a.emit("store:reloaded")
			}
		}
	}()
}

// followParent 等主窗口退出后关闭本窗口；还有传输任务时等它们结束。
func (a *App) followParent() {
	if a.parentPID <= 0 {
		return
	}
	waitProcessExit(a.parentPID)
	if a.xfers.active() > 0 {
		a.emit("parent:exit")
		for a.xfers.active() > 0 {
			time.Sleep(time.Second)
		}
		time.Sleep(1500 * time.Millisecond) // 让“传输完成”的提示显示出来
	}
	a.forceQuit.Store(true)
	runtime.Quit(a.ctx)
}
