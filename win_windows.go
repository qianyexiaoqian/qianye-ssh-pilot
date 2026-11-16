//go:build windows

package main

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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

var (
	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procGetTickCount     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")

	hiddenMu      sync.Mutex
	hiddenWindows = map[int][]windows.HWND{}
)

const (
	swHide   = 0
	swShowNA = 8
)

// showProcessWindows 隐藏或重新显示另一个进程的顶层窗口（界面锁定时用来收起文件管理窗口）。
// 隐藏时记下窗口，重新显示时只恢复这些窗口。
func showProcessWindows(pid int, show bool) {
	hiddenMu.Lock()
	defer hiddenMu.Unlock()
	if show {
		for _, hwnd := range hiddenWindows[pid] {
			_, _, _ = procShowWindow.Call(uintptr(hwnd), swShowNA)
		}
		delete(hiddenWindows, pid)
		return
	}
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var wpid uint32
		_, _ = windows.GetWindowThreadProcessId(hwnd, &wpid)
		if int(wpid) == pid && windows.IsWindowVisible(hwnd) {
			hiddenWindows[pid] = append(hiddenWindows[pid], hwnd)
		}
		return 1
	})
	_ = windows.EnumWindows(cb, unsafe.Pointer(nil))
	for _, hwnd := range hiddenWindows[pid] {
		_, _, _ = procShowWindow.Call(uintptr(hwnd), swHide)
	}
}

// idleSeconds 返回当前登录会话里距离上一次键盘鼠标操作过了多少秒。
func idleSeconds() int {
	info := struct{ size, time uint32 }{size: 8}
	if ok, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return 0
	}
	tick, _, _ := procGetTickCount.Call()
	return int((uint32(tick) - info.time) / 1000)
}

// systemProxy 读取 Windows 的系统代理设置（代理软件开启「系统代理」后会写在这里）。
func systemProxy() (server string, ok bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	if on, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || on == 0 {
		return "", false
	}
	server, _, err = k.GetStringValue("ProxyServer")
	return server, err == nil && server != ""
}
