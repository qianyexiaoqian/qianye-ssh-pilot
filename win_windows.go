//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindow           = user32.NewProc("GetWindow")
)

const (
	swRestore = 9
	gwOwner   = 4
)

// focusProcessWindow 把另一个进程的主窗口提到前面（最小化时先还原），返回是否找到了窗口。
func focusProcessWindow(pid int) bool {
	var found windows.HWND
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var wpid uint32
		_, _ = windows.GetWindowThreadProcessId(hwnd, &wpid)
		if int(wpid) != pid || !windows.IsWindowVisible(hwnd) {
			return 1
		}
		// 只要没有所有者的顶层窗口，跳过 WebView2 等附属窗口
		if owner, _, _ := procGetWindow.Call(uintptr(hwnd), gwOwner); owner != 0 {
			return 1
		}
		found = hwnd
		return 0
	})
	_ = windows.EnumWindows(cb, unsafe.Pointer(nil))
	if found == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(uintptr(found)); iconic != 0 {
		_, _, _ = procShowWindow.Call(uintptr(found), swRestore)
	}
	_, _, _ = procSetForegroundWindow.Call(uintptr(found))
	return true
}

// waitProcessExit 阻塞到指定进程退出；进程已经不存在时立即返回。
func waitProcessExit(pid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, windows.INFINITE)
}
