package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// instanceID 是单实例锁的标识：每个数据目录一把锁，使用不同数据目录的实例互不影响，可以同时运行。
// 数据仍在 %AppData% 时沿用旧版本的标识，避免新旧版本同时打开同一份数据。
func instanceID(loc dataLocation) string {
	id := "8f6a2a4c-sshpilot-desktop"
	if loc.Mode != dataModeAppData {
		sum := sha256.Sum256([]byte(strings.ToLower(loc.Dir)))
		id += "-" + hex.EncodeToString(sum[:6])
	}
	return id
}

func main() {
	app := NewApp()
	// 带着 SSHPILOT_FILES_WINDOW 启动的进程是某台服务器的「文件管理」独立窗口，由主窗口拉起
	app.filesWindow = strings.TrimSpace(os.Getenv(envFilesWindow))
	app.parentPID, _ = strconv.Atoi(os.Getenv(envParentPID))
	loc := currentDataLocation()
	dataDir := loc.Dir
	ws := loadWindowState(dataDir, app.windowKind())

	opts := &options.App{
		Title:            ProductName,
		Width:            ws.Width,
		Height:           ws.Height,
		MinWidth:         minWindowWidth,
		MinHeight:        minWindowHeight,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 10, G: 11, B: 16, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets, Handler: app.assetHandler()},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		Bind:             []any{app},
		// 从资源管理器拖进来的文件由界面接收（文件管理窗口用它上传），路径由 Wails 解析
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
		Windows: &windows.Options{
			Theme:                             windows.Dark,
			DisableFramelessWindowDecorations: false,
			WebviewUserDataPath:               webviewDataPath(dataDir),
		},
	}
	if ws.Maximised {
		opts.WindowStartState = options.Maximised
	}
	if app.filesWindow == "" {
		opts.SingleInstanceLock = &options.SingleInstanceLock{
			UniqueId: instanceID(loc),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				runtime.WindowUnminimise(app.ctx)
				runtime.Show(app.ctx)
			},
		}
	} else {
		opts.Title = "文件管理 - " + ProductName
	}
	if err := wails.Run(opts); err != nil {
		log.Fatal(err)
	}
}
