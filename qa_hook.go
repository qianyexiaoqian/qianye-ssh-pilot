//go:build qa && windows

package main

// 仅用于界面验收的构建（go build -tags qa）：在本机回环地址上开一个只有测试脚本知道端口的 HTTP 接口，
// 把 DevTools 协议的调用转给窗口里的 WebView2（执行脚本、截图、模拟把文件拖进窗口），
// 这样可以在真实的窗口里做自动化验收，而不需要移动鼠标。正式发布的程序不包含这个文件。

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ICoreWebView2 虚表里 CallDevToolsProtocolMethod 的位置（IUnknown 3 个方法之后的第 34 个）。
const vtblCallDevToolsProtocolMethod = 36

type qaHandlerVtbl struct {
	QueryInterface, AddRef, Release, Invoke uintptr
}

// qaHandler 实现 ICoreWebView2CallDevToolsProtocolMethodCompletedHandler。
type qaHandler struct {
	vtbl *qaHandlerVtbl
	id   uintptr
}

var (
	qaMu      sync.Mutex
	qaSeq     uintptr
	qaPending = map[uintptr]chan qaResult{}
	qaKeep    = map[uintptr]*qaHandler{}
	qaVtbl    = &qaHandlerVtbl{
		QueryInterface: windows.NewCallback(func(this *qaHandler, _ uintptr, out *uintptr) uintptr {
			*out = uintptr(unsafe.Pointer(this))
			return 0
		}),
		AddRef:  windows.NewCallback(func(*qaHandler) uintptr { return 2 }),
		Release: windows.NewCallback(func(*qaHandler) uintptr { return 1 }),
		Invoke: windows.NewCallback(func(this *qaHandler, hr uintptr, ret *uint16) uintptr {
			qaMu.Lock()
			ch := qaPending[this.id]
			delete(qaPending, this.id)
			delete(qaKeep, this.id)
			qaMu.Unlock()
			if ch != nil {
				ch <- qaResult{hr: hr, json: windows.UTF16PtrToString(ret)}
			}
			return 0
		}),
	}
)

type qaResult struct {
	hr   uintptr
	json string
}

// unexported 读取结构体里未导出的字段（只在验收构建里使用）。
func unexported(v reflect.Value, name string) reflect.Value {
	f := v.FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func (a *App) startQA() {
	fe := reflect.ValueOf(a.ctx.Value("frontend"))
	if !fe.IsValid() || fe.Kind() != reflect.Ptr {
		return
	}
	window := unexported(fe.Elem(), "mainWindow")
	chromium := unexported(fe.Elem(), "chromium")
	invoke := window.MethodByName("Invoke")
	if !invoke.IsValid() || chromium.IsNil() {
		return
	}
	// 在界面线程上调用 WebView2
	call := func(method, params string) (string, error) {
		m, _ := windows.UTF16PtrFromString(method)
		p, _ := windows.UTF16PtrFromString(params)
		ch := make(chan qaResult, 1)
		qaMu.Lock()
		qaSeq++
		h := &qaHandler{vtbl: qaVtbl, id: qaSeq}
		qaPending[h.id], qaKeep[h.id] = ch, h
		qaMu.Unlock()
		var hr uintptr
		started := make(chan struct{})
		invoke.Call([]reflect.Value{reflect.ValueOf(func() {
			defer close(started)
			webview := unexported(chromium.Elem(), "webview")
			if webview.IsNil() {
				hr = 0x80004005
				return
			}
			obj := webview.UnsafePointer()
			vtbl := *(**[64]uintptr)(obj)
			hr, _, _ = syscall.SyscallN(vtbl[vtblCallDevToolsProtocolMethod], uintptr(obj), uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(h)))
		})})
		<-started
		if hr != 0 {
			return "", fmt.Errorf("CallDevToolsProtocolMethod: 0x%08x", hr)
		}
		select {
		case r := <-ch:
			if r.hr != 0 {
				return "", fmt.Errorf("%s failed: 0x%08x %s", method, r.hr, r.json)
			}
			return r.json, nil
		case <-time.After(60 * time.Second):
			return "", fmt.Errorf("%s timed out", method)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	dir := filepath.Join(a.dataDir, "qa")
	_ = os.MkdirAll(dir, 0o700)
	portFile := filepath.Join(dir, fmt.Sprintf("%s-%d.port", a.windowKind(), os.Getpid()))
	_ = os.WriteFile(portFile, []byte(fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)), 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc("/cdp", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var in struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &in) != nil || in.Method == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(in.Params) == 0 {
			in.Params = json.RawMessage("{}")
		}
		out, err := call(in.Method, string(in.Params))
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		fmt.Fprintf(w, `{"result":%s}`, out)
	})
	go func() { _ = http.Serve(ln, mux) }()
}
