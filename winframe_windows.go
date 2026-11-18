//go:build windows

package main

import (
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 无边框窗口的缩放边框。
//
// Wails 的无边框窗口把整个窗口都算作客户区，窗口外侧没有系统的缩放边框：想调整大小，只能把鼠标停在窗口内侧
// 几个像素宽的地方（由界面里的脚本判断），不好找，还和贴着窗口边缘的滚动条抢位置。
// 这里接管 WM_NCCALCSIZE，把左、右、下三边的系统边框还给窗口。从 Windows 10 起这部分边框是透明的，位置在
// 可见窗口的外侧，于是拖动这三条边和两个下角完全由系统处理，手感和普通窗口一样。
// 上边不能留系统边框（会露出一条系统标题栏），仍然由界面处理（见 frontend/src/lib/chrome.ts）。

var (
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procIsZoomed         = user32.NewProc("IsZoomed")
)

const (
	wmNCCalcSize = 0x0083
	wsThickFrame = 0x00040000
	// SWP_NOSIZE | SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED：只让系统重新计算边框
	swpFrameChanged = 0x0001 | 0x0002 | 0x0004 | 0x0010 | 0x0020

	wailsWindowClass = "wailsWindow"
)

// GetWindowLongPtr 的索引是负数，不能直接写成 uintptr 常量
var (
	gwlpWndProc = int32(-4)
	gwlStyle    = int32(-16)
)

type winRect struct{ Left, Top, Right, Bottom int32 }

// prevWndProc 是 Wails 原来的窗口过程。
var prevWndProc atomic.Uintptr

// rectAt 把窗口消息里的指针参数当作 RECT 读写。
func rectAt(p uintptr) *winRect { return (*winRect)(*(*unsafe.Pointer)(unsafe.Pointer(&p))) }

// resizableClient 计算带缩放边框的客户区：win 是整个窗口，def 是系统默认的客户区（四边减去边框，上边还减去标题栏）。
// 左、右、下沿用系统的边框，上边不留。底部和 Wails 一样多算 1 个像素：Wails 给 WebView 留了 1 像素的底部内边距
// （用来避免缩放时闪烁），多出来的这一行正好落在边框线的位置。
// def 不合理时返回 ok=false，调用方保持 Wails 原来的结果。
func resizableClient(win, def winRect) (rc winRect, ok bool) {
	if def.Left <= win.Left || def.Right >= win.Right || def.Bottom >= win.Bottom || def.Right <= def.Left || def.Bottom < win.Top {
		return win, false
	}
	return winRect{Left: def.Left, Top: win.Top, Right: def.Right, Bottom: def.Bottom + 1}, true
}

func frameWndProc(hwnd windows.HWND, msg uint32, wparam, lparam uintptr) uintptr {
	prev := prevWndProc.Load()
	if msg != wmNCCalcSize || wparam == 0 || lparam == 0 {
		ret, _, _ := procCallWindowProc.Call(prev, uintptr(hwnd), uintptr(msg), wparam, lparam)
		return ret
	}
	rc := rectAt(lparam)
	win := *rc
	// 问系统默认的客户区是多大（wParam 为 0 时参数就是一个 RECT：传入窗口的位置，传出客户区的位置）
	def := win
	_, _, _ = procDefWindowProc.Call(uintptr(hwnd), wmNCCalcSize, 0, uintptr(unsafe.Pointer(&def)))
	// 先让 Wails 处理：最大化时它把客户区限制在工作区以内，并维护 WebView 的内边距
	ret, _, _ := procCallWindowProc.Call(prev, uintptr(hwnd), uintptr(msg), wparam, lparam)
	if resizableNow(hwnd) {
		if client, ok := resizableClient(win, def); ok {
			*rc = client
		}
	}
	return ret
}

// resizableNow 判断窗口现在是不是可以拖动边框缩放的普通状态（不是最大化、最小化或全屏）。
func resizableNow(hwnd windows.HWND) bool {
	if zoomed, _, _ := procIsZoomed.Call(uintptr(hwnd)); zoomed != 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(uintptr(hwnd)); iconic != 0 {
		return false
	}
	style, _, _ := procGetWindowLongPtr.Call(uintptr(hwnd), uintptr(gwlStyle))
	return style&wsThickFrame != 0
}

// ownWindow 查找本进程里指定窗口类的顶层窗口（窗口还没有显示时也能找到）。
func ownWindow(class string) windows.HWND {
	pid := uint32(os.Getpid())
	var found windows.HWND
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var wpid uint32
		_, _ = windows.GetWindowThreadProcessId(hwnd, &wpid)
		if wpid != pid {
			return 1
		}
		name := make([]uint16, 64)
		if n, _ := windows.GetClassName(hwnd, &name[0], int32(len(name))); n == 0 || windows.UTF16ToString(name) != class {
			return 1
		}
		found = hwnd
		return 0
	})
	_ = windows.EnumWindows(cb, unsafe.Pointer(nil))
	return found
}

// installResizeBorders 让本进程的无边框窗口在左、右、下三边使用系统的缩放边框，返回是否成功。
// 失败时窗口保持 Wails 原来的行为（由界面在窗口内侧处理缩放）。
func installResizeBorders() bool {
	hwnd := ownWindow(wailsWindowClass)
	if hwnd == 0 || prevWndProc.Load() != 0 {
		return false
	}
	// 先记下原来的窗口过程：替换完成的那一刻消息就会交给新的过程
	prev, _, _ := procGetWindowLongPtr.Call(uintptr(hwnd), uintptr(gwlpWndProc))
	if prev == 0 {
		return false
	}
	prevWndProc.Store(prev)
	if old, _, _ := procSetWindowLongPtr.Call(uintptr(hwnd), uintptr(gwlpWndProc), windows.NewCallback(frameWndProc)); old == 0 {
		prevWndProc.Store(0)
		return false
	}
	_, _, _ = procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, swpFrameChanged)
	return true
}
