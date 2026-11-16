package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

// ---- 连接线路：直连、经跳板机、经代理 ----
//
// 每台服务器可以保存一条固定的线路（服务器设置里的跳板机或代理）。直连不通的时候，还可以临时换一条线路：
// 临时线路只存在内存里，通过它打开的终端和文件管理窗口全部断开后自动取消，服务器的设置不变。

// envTempRoute 把这台服务器正在使用的临时线路交给文件管理窗口进程（JSON）。
const envTempRoute = "SSHPILOT_TEMP_ROUTE"

type ProxyInput struct {
	Type     string `json:"type"` // local | http | socks5
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// KeepPassword 为 true 表示沿用已经保存的代理密码（界面上没有重新填写）。
	KeepPassword bool `json:"keepPassword"`
}

type RouteInput struct {
	Mode         string     `json:"mode"` // direct | jump | proxy
	JumpServerID string     `json:"jumpServerId"`
	Proxy        ProxyInput `json:"proxy"`
}

// route 是到一台服务器的连接线路；jumpID 和 proxy 都为空表示直连。
type route struct {
	JumpID string             `json:"jumpId"`
	Proxy  *store.ProxyConfig `json:"proxy"`
	Since  int64              `json:"since"`
	// used 表示已经有终端或文件管理窗口通过这条临时线路连接过：它们全部断开后线路自动取消。
	used bool
}

// RouteView 是界面上显示的一条临时线路。
type RouteView struct {
	ServerID     string             `json:"serverId"`
	Mode         string             `json:"mode"`
	JumpServerID string             `json:"jumpServerId"`
	Proxy        *store.ProxyConfig `json:"proxy"`
	Label        string             `json:"label"`
	Since        int64              `json:"since"`
}

func proxyLabel(p *store.ProxyConfig) string {
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	switch p.Type {
	case "local":
		return "本地代理 " + addr
	case "socks5":
		return "SOCKS5 代理 " + addr
	default:
		return "HTTP 代理 " + addr
	}
}

func dialProxy(p *store.ProxyConfig) *sshx.Proxy {
	if p == nil {
		return nil
	}
	out := &sshx.Proxy{Host: p.Host, Port: p.Port, User: p.Username, Password: p.Password}
	switch p.Type {
	case "local":
		out.Type, out.Host = sshx.ProxyAuto, "127.0.0.1"
	case "socks5":
		out.Type = sshx.ProxySOCKS5
	default:
		out.Type = sshx.ProxyHTTP
	}
	return out
}

func sameProxy(a, b *store.ProxyConfig) bool {
	return a != nil && b != nil && a.Type == b.Type && strings.EqualFold(a.Host, b.Host) && a.Port == b.Port && a.Username == b.Username
}

// normalizeProxy 整理界面填写的代理。saved 是可能保存着密码的代理（服务器已保存的、上次使用的），
// 界面没有重新填写密码时从里面找同一个代理的密码。
func normalizeProxy(in ProxyInput, saved ...*store.ProxyConfig) (*store.ProxyConfig, error) {
	p := &store.ProxyConfig{Type: in.Type, Host: strings.TrimSpace(in.Host), Port: in.Port, Username: strings.TrimSpace(in.Username), Password: in.Password}
	switch p.Type {
	case "local":
		p.Host, p.Username, p.Password = "127.0.0.1", "", ""
	case "http", "socks5":
		// 允许直接粘贴 socks5://1.2.3.4:1080 这样的地址
		if i := strings.Index(p.Host, "://"); i >= 0 {
			p.Host = p.Host[i+3:]
		}
		p.Host = strings.TrimSuffix(p.Host, "/")
		if h, port, err := net.SplitHostPort(p.Host); err == nil {
			if n, err := strconv.Atoi(port); err == nil {
				p.Host, p.Port = h, n
			}
		}
		if p.Host == "" || strings.ContainsAny(p.Host, " \t/") {
			return nil, errors.New("请填写代理服务器的地址")
		}
	default:
		return nil, errors.New("请选择代理类型")
	}
	if p.Port < 1 || p.Port > 65535 {
		return nil, errors.New("请填写代理端口（1-65535）")
	}
	if p.Username == "" {
		p.Password = ""
	} else if p.Password == "" && in.KeepPassword {
		for _, s := range saved {
			if sameProxy(p, s) && s.Password != "" {
				p.Password = s.Password
				break
			}
		}
	}
	return p, nil
}

func (a *App) tempRoute(id string) *route {
	a.routeMu.Lock()
	defer a.routeMu.Unlock()
	if r := a.tempRoutes[id]; r != nil {
		cp := *r
		return &cp
	}
	return nil
}

// savedProxies 返回可能保存着密码的代理：这台服务器已保存的、它的临时线路里的，以及上次使用的。
func (a *App) savedProxies(serverID string) []*store.ProxyConfig {
	var out []*store.ProxyConfig
	a.store.Read(func(d *store.Data) {
		if s := d.Server(serverID); s != nil && s.Proxy != nil {
			cp := *s.Proxy
			out = append(out, &cp)
		}
		if p := d.Settings.General.LastProxy; p != nil {
			cp := *p
			out = append(out, &cp)
		}
	})
	if r := a.tempRoute(serverID); r != nil && r.Proxy != nil {
		out = append(out, r.Proxy)
	}
	return out
}

// checkJump 确认跳板机存在，并且顺着它的线路不会绕回 serverID。
func (a *App) checkJump(serverID, jumpID string) error {
	if jumpID == "" {
		return errors.New("请选择跳板机")
	}
	if jumpID == serverID && serverID != "" {
		return errors.New("不能把服务器自己当作跳板机")
	}
	id := jumpID
	for depth := 0; id != "" && depth < 5; depth++ {
		var next string
		exists := false
		a.store.Read(func(d *store.Data) {
			if s := d.Server(id); s != nil {
				exists, next = true, s.JumpServerID
			}
		})
		if !exists {
			return errors.New("跳板机不存在")
		}
		if r := a.tempRoute(id); r != nil {
			next = r.JumpID
		}
		if next == serverID && serverID != "" {
			return errors.New("这台跳板机本身要经过当前服务器才能连接，不能互相中转")
		}
		id = next
	}
	return nil
}

func (a *App) buildRoute(serverID string, in RouteInput) (*route, error) {
	r := &route{Since: store.Now()}
	switch in.Mode {
	case "direct":
	case "jump":
		if err := a.checkJump(serverID, in.JumpServerID); err != nil {
			return nil, err
		}
		r.JumpID = in.JumpServerID
	case "proxy":
		p, err := normalizeProxy(in.Proxy, a.savedProxies(serverID)...)
		if err != nil {
			return nil, err
		}
		r.Proxy = p
	default:
		return nil, errors.New("请选择连接线路")
	}
	return r, nil
}

func (a *App) rememberProxy(p *store.ProxyConfig) {
	if p == nil {
		return
	}
	_ = a.store.Update(func(d *store.Data) error {
		cp := *p
		d.Settings.General.LastProxy = &cp
		return nil
	})
}

func (a *App) routeView(id string, r *route) RouteView {
	v := RouteView{ServerID: id, Mode: "direct", Label: "直连", Since: r.Since}
	switch {
	case r.JumpID != "":
		v.Mode, v.JumpServerID, v.Label = "jump", r.JumpID, "经 "+a.serverName(r.JumpID)
	case r.Proxy != nil:
		p := *r.Proxy
		p.HasPassword, p.Password = p.Password != "", ""
		v.Mode, v.Proxy, v.Label = "proxy", &p, "经"+proxyLabel(r.Proxy)
	}
	return v
}

func (a *App) routesChanged(cleared string) {
	a.emit("routes:changed", cleared)
	a.RefreshStatus()
}

// SetTempRoute 给一台服务器设置临时线路：之后的新连接都走这条线路，直到通过它打开的终端和文件管理窗口
// 全部断开（或手动取消）。服务器保存的设置不变。
func (a *App) SetTempRoute(serverID string, in RouteInput) (RouteView, error) {
	if err := a.guard(); err != nil {
		return RouteView{}, err
	}
	exists := false
	a.store.Read(func(d *store.Data) { exists = d.Server(serverID) != nil })
	if !exists {
		return RouteView{}, errors.New("服务器不存在或已删除")
	}
	r, err := a.buildRoute(serverID, in)
	if err != nil {
		return RouteView{}, err
	}
	a.routeMu.Lock()
	a.tempRoutes[serverID] = r
	a.routeMu.Unlock()
	a.rememberProxy(r.Proxy)
	a.pool.Invalidate(serverID)
	a.forgetOwners(serverID)
	a.monitor.unblock(serverID)
	a.forgetOSAttempt(serverID)
	a.routesChanged("")
	return a.routeView(serverID, r), nil
}

// ClearTempRoute 取消临时线路：新的连接恢复使用服务器保存的线路，已经打开的终端不受影响。
func (a *App) ClearTempRoute(serverID string) {
	a.routeMu.Lock()
	_, had := a.tempRoutes[serverID]
	delete(a.tempRoutes, serverID)
	a.routeMu.Unlock()
	if had {
		a.pool.Invalidate(serverID)
		a.forgetOwners(serverID)
		a.routesChanged("")
	}
}

func (a *App) ListTempRoutes() []RouteView {
	a.routeMu.Lock()
	routes := make(map[string]*route, len(a.tempRoutes))
	for id, r := range a.tempRoutes {
		cp := *r
		routes[id] = &cp
	}
	a.routeMu.Unlock()
	out := make([]RouteView, 0, len(routes))
	for id, r := range routes {
		out = append(out, a.routeView(id, r))
	}
	slices.SortFunc(out, func(x, y RouteView) int { return strings.Compare(x.ServerID, y.ServerID) })
	return out
}

// routeUsed 记录有终端或文件管理窗口通过临时线路连上了这台服务器。
func (a *App) routeUsed(serverID string) {
	a.routeMu.Lock()
	if r := a.tempRoutes[serverID]; r != nil {
		r.used = true
	}
	a.routeMu.Unlock()
}

// routeUseEnded 在一个终端或文件管理窗口关闭后调用：通过临时线路建立的连接都断开了，就取消这条临时线路。
func (a *App) routeUseEnded(serverID string) {
	a.routeMu.Lock()
	r := a.tempRoutes[serverID]
	a.routeMu.Unlock()
	if r == nil || !r.used {
		return
	}
	if slices.Contains(a.terms.OpenServers(), serverID) || slices.Contains(a.FilesWindows(), serverID) {
		return
	}
	a.routeMu.Lock()
	delete(a.tempRoutes, serverID)
	a.routeMu.Unlock()
	a.pool.Invalidate(serverID)
	a.forgetOwners(serverID)
	a.routesChanged(serverID)
}

// dropRoutesVia 在服务器被删除后清掉它自己的临时线路，以及把它当跳板机的临时线路。
func (a *App) dropRoutesVia(serverID string) {
	a.routeMu.Lock()
	delete(a.tempRoutes, serverID)
	for id, r := range a.tempRoutes {
		if r.JumpID == serverID {
			delete(a.tempRoutes, id)
		}
	}
	a.routeMu.Unlock()
}

// SaveServerRoute 把线路保存为这台服务器的固定设置，并取消它的临时线路。
func (a *App) SaveServerRoute(serverID string, in RouteInput) (store.Server, error) {
	if err := a.guard(); err != nil {
		return store.Server{}, err
	}
	r, err := a.buildRoute(serverID, in)
	if err != nil {
		return store.Server{}, err
	}
	var saved store.Server
	err = a.store.Update(func(d *store.Data) error {
		s := d.Server(serverID)
		if s == nil {
			return errors.New("服务器不存在或已删除")
		}
		s.JumpServerID, s.Proxy, s.UpdatedAt = r.JumpID, r.Proxy, store.Now()
		if r.Proxy != nil {
			cp := *r.Proxy
			d.Settings.General.LastProxy = &cp
		}
		saved = *s
		return nil
	})
	if err != nil {
		return store.Server{}, err
	}
	a.routeMu.Lock()
	delete(a.tempRoutes, serverID)
	a.routeMu.Unlock()
	a.pool.Invalidate(serverID)
	a.forgetOwners(serverID)
	a.monitor.unblock(serverID)
	a.forgetOSAttempt(serverID)
	a.routesChanged("")
	return a.serverView(saved), nil
}

// TestRoute 用指定的线路测试能否登录这台服务器（不改变任何设置）。
func (a *App) TestRoute(serverID string, in RouteInput) TestResult {
	if err := a.guard(); err != nil {
		return TestResult{Message: err.Error()}
	}
	r, err := a.buildRoute(serverID, in)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	ep, err := a.endpointVia(serverID, 0, r)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	res := a.runTest(ep, true)
	if res.OK && res.OS != "" {
		a.setServerOS(serverID, res.OS)
	}
	return res
}

// tempRouteEnv 返回交给文件管理窗口进程的临时线路；没有临时线路时返回空字符串。
func (a *App) tempRouteEnv(serverID string) string {
	r := a.tempRoute(serverID)
	if r == nil {
		return ""
	}
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

// adoptParentRoute 在文件管理窗口进程启动时调用：沿用主窗口里这台服务器的临时线路。
func (a *App) adoptParentRoute() {
	raw := os.Getenv(envTempRoute)
	if raw == "" {
		return
	}
	var r route
	if json.Unmarshal([]byte(raw), &r) == nil {
		a.routeMu.Lock()
		a.tempRoutes[a.filesWindow] = &r
		a.routeMu.Unlock()
	}
}

// probeTarget 返回探测在线状态时实际要连接的地址：经跳板机访问的服务器以最外层的跳板机为准，
// 并带上连接那一跳要用的代理。
func (a *App) probeTarget(s store.Server) (host string, port int, proxy *sshx.Proxy) {
	host, port = s.Host, s.Port
	jumpID, px := s.JumpServerID, s.Proxy
	if r := a.tempRoute(s.ID); r != nil {
		jumpID, px = r.JumpID, r.Proxy
	}
	for depth := 0; jumpID != "" && depth < 4; depth++ {
		var j *store.Server
		a.store.Read(func(d *store.Data) {
			if x := d.Server(jumpID); x != nil {
				cp := *x
				j = &cp
			}
		})
		if j == nil {
			break
		}
		host, port = j.Host, j.Port
		jumpID, px = j.JumpServerID, j.Proxy
		if r := a.tempRoute(j.ID); r != nil {
			jumpID, px = r.JumpID, r.Proxy
		}
	}
	return host, port, dialProxy(px)
}

// ---- 本地代理 ----

type LocalProxy struct {
	Found bool `json:"found"`
	Port  int  `json:"port"`
	// Type 是识别出的协议（socks5 / http）；Source 说明是从哪里找到的。
	Type   string `json:"type"`
	Source string `json:"source"`
}

// commonProxyPorts 是常见代理软件默认监听的本机端口（Clash、Clash Verge、v2rayN、sing-box、Surge 等）。
var commonProxyPorts = []int{7890, 7897, 7891, 10808, 10809, 2080, 1080, 20171, 6152, 8889}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseSystemProxy 解析 Windows 系统代理的写法："127.0.0.1:7890"，或 "http=…;https=…;socks=…"。
// 返回其中指向本机的端口，socks 的排在前面。
func parseSystemProxy(server string) []int {
	var socks, others []int
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		scheme := ""
		if k, v, ok := strings.Cut(part, "="); ok {
			scheme, part = strings.ToLower(k), v
		}
		if i := strings.Index(part, "://"); i >= 0 {
			part = part[i+3:]
		}
		host, port, err := net.SplitHostPort(strings.TrimSuffix(part, "/"))
		n, _ := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 || !isLoopback(host) {
			continue
		}
		if scheme == "socks" {
			socks = append(socks, n)
		} else if !slices.Contains(others, n) {
			others = append(others, n)
		}
	}
	return append(socks, others...)
}

// DetectLocalProxy 找出本机正在运行的代理软件监听的端口：先看 Windows 的系统代理设置，再试常见端口。
func (a *App) DetectLocalProxy() LocalProxy {
	sniff := func(port int) string {
		return sshx.SniffProxy(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	}
	if server, ok := systemProxy(); ok {
		for _, port := range parseSystemProxy(server) {
			if typ := sniff(port); typ != "" {
				return LocalProxy{Found: true, Port: port, Type: typ, Source: "系统代理"}
			}
		}
	}
	found := make([]string, len(commonProxyPorts))
	var wg sync.WaitGroup
	for i, port := range commonProxyPorts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = sniff(port)
		}()
	}
	wg.Wait()
	for i, typ := range found {
		if typ != "" {
			return LocalProxy{Found: true, Port: commonProxyPorts[i], Type: typ, Source: fmt.Sprintf("本机端口 %d", commonProxyPorts[i])}
		}
	}
	return LocalProxy{}
}
