package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

// monitorGrace 是界面不再需要某台服务器的监控后，保留采样会话的时间。
// 页面切换时前一个页面先取消订阅、后一个页面再订阅，短暂保留可以避免会话被反复重建。
const monitorGrace = 3 * time.Second

// monitorSubTTL 是订阅的有效期。界面每 20 秒续订一次；界面异常退出（没有机会取消订阅）时，
// 订阅到期后采样会话自动停止，不会一直占用服务器连接。
const monitorSubTTL = 60 * time.Second

// monSub 是某个界面的一次订阅。seq 由前端递增：前端的调用在后端并发处理，可能乱序到达，
// 序号较小的旧请求会被忽略（否则「取消订阅」可能覆盖紧随其后的「重新订阅」）。
type monSub struct {
	ids []string
	seq int
	at  time.Time
}

type monWatcher struct {
	cancel    context.CancelFunc
	interval  int
	idleSince time.Time // 非零表示已没有界面需要它，等待宽限期结束
}

// monitorHub 管理各服务器的实时采样会话：界面按 key 声明需要监控的服务器集合，
// hub 取并集后为每台服务器维持一个采样循环，并把采样通过 monitor:sample 事件推送给前端。
type monitorHub struct {
	app *App

	mu       sync.Mutex
	subs     map[string]*monSub
	watchers map[string]*monWatcher
	blocked  map[string]string // 因认证失败等原因暂停的服务器 → 错误信息
	last     map[string]ops.Sample
	grace    time.Duration
	ttl      time.Duration
	expiry   *time.Timer // 订阅到期检查
}

func newMonitorHub(a *App) *monitorHub {
	return &monitorHub{app: a, subs: map[string]*monSub{}, watchers: map[string]*monWatcher{}, blocked: map[string]string{},
		last: map[string]ops.Sample{}, grace: monitorGrace, ttl: monitorSubTTL}
}

func (h *monitorHub) config() (interval int, off bool) {
	g := h.app.settings().General
	return min(max(g.MonitorInterval, 1), 60), g.MonitorOff
}

// reconcile 让正在运行的采样会话与当前订阅、设置保持一致。
func (h *monitorHub) reconcile() {
	interval, off := h.config()
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	want := map[string]bool{}
	var oldest time.Time
	for key, sub := range h.subs {
		if now.Sub(sub.at) > h.ttl {
			delete(h.subs, key)
			continue
		}
		if oldest.IsZero() || sub.at.Before(oldest) {
			oldest = sub.at
		}
		if off {
			continue
		}
		for _, id := range sub.ids {
			want[id] = true
		}
	}
	// 在最早的一条订阅到期时再检查一次（界面续订后会重新计时）
	if h.expiry != nil {
		h.expiry.Stop()
		h.expiry = nil
	}
	if !oldest.IsZero() {
		h.expiry = time.AfterFunc(oldest.Add(h.ttl).Sub(now)+50*time.Millisecond, h.reconcile)
	}
	recheck := false
	for id, w := range h.watchers {
		switch {
		case w.interval != interval || off:
			w.cancel()
			delete(h.watchers, id)
		case want[id]:
			w.idleSince = time.Time{}
		case w.idleSince.IsZero():
			w.idleSince = now
			recheck = true
		case now.Sub(w.idleSince) >= h.grace:
			w.cancel()
			delete(h.watchers, id)
		default:
			recheck = true
		}
	}
	for id := range want {
		if h.watchers[id] != nil || h.blocked[id] != "" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		w := &monWatcher{cancel: cancel, interval: interval}
		h.watchers[id] = w
		go h.run(ctx, w, id, interval)
	}
	if recheck {
		time.AfterFunc(h.grace+50*time.Millisecond, h.reconcile)
	}
}

func (h *monitorHub) publish(s ops.Sample) {
	h.mu.Lock()
	h.last[s.ServerID] = s
	h.mu.Unlock()
	h.app.emit("monitor:sample", s)
}

// permanent 判断错误是否无法通过重试恢复：认证失败、配置缺失或系统不支持。
// 这类错误会暂停该服务器的监控，直到用户修改配置或手动重试，避免反复失败的登录触发服务器封禁。
func (h *monitorHub) permanent(id string, err error) bool {
	if sshx.IsAuthError(err) || errors.Is(err, ops.ErrMonitorUnsupported) {
		return true
	}
	_, cfgErr := h.app.resolveEndpoint(id)
	return cfgErr != nil
}

func (h *monitorHub) run(ctx context.Context, w *monWatcher, id string, interval int) {
	backoff := 3 * time.Second
	for {
		started := time.Now()
		err := ops.Watch(ctx, h.app.pool, id, interval, h.publish)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			continue // 采样循环到达运行上限后正常退出，重新发起
		}
		stop := h.permanent(id, err)
		h.publish(ops.Sample{ServerID: id, Time: time.Now().UnixMilli(), Error: err.Error(), Stopped: stop})
		if stop {
			h.mu.Lock()
			if h.watchers[id] == w {
				delete(h.watchers, id)
				h.blocked[id] = err.Error()
			}
			h.mu.Unlock()
			return
		}
		if time.Since(started) > time.Minute {
			backoff = 3 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// unblock 清除暂停状态（服务器配置变化、连接测试通过或用户手动重试后调用）。
func (h *monitorHub) unblock(id string) {
	h.mu.Lock()
	_, was := h.blocked[id]
	delete(h.blocked, id)
	h.mu.Unlock()
	if was {
		h.reconcile()
	}
}

// forget 在服务器被删除后清理它的全部监控状态。
func (h *monitorHub) forget(id string) {
	h.mu.Lock()
	for _, sub := range h.subs {
		sub.ids = slices.DeleteFunc(slices.Clone(sub.ids), func(x string) bool { return x == id })
	}
	if w := h.watchers[id]; w != nil {
		w.cancel()
		delete(h.watchers, id)
	}
	delete(h.blocked, id)
	delete(h.last, id)
	h.mu.Unlock()
}

func (h *monitorHub) stopAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs = map[string]*monSub{}
	if h.expiry != nil {
		h.expiry.Stop()
		h.expiry = nil
	}
	for id, w := range h.watchers {
		w.cancel()
		delete(h.watchers, id)
	}
}

func (h *monitorHub) active() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.watchers))
	for id := range h.watchers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// ---- 暴露给前端 ----

// MonitorWatch 声明界面 key 需要实时监控的服务器；传空列表表示这个界面不再需要监控。
// seq 是前端递增的序号，用来丢弃乱序到达的旧请求；界面需要每隔一段时间续订，否则订阅会到期。
func (a *App) MonitorWatch(key string, serverIDs []string, seq int) {
	h := a.monitor
	h.mu.Lock()
	if cur := h.subs[key]; cur != nil && seq < cur.seq {
		h.mu.Unlock()
		return
	}
	// 取消订阅也保留一条空记录（到期后清理），这样更早发出却更晚到达的订阅请求不会生效
	h.subs[key] = &monSub{ids: uniqueIDs(serverIDs), seq: seq, at: time.Now()}
	h.mu.Unlock()
	h.reconcile()
}

// MonitorSnapshot 返回每台服务器最近一次采样（包含出错、已暂停的状态）。
func (a *App) MonitorSnapshot() []ops.Sample {
	a.monitor.mu.Lock()
	defer a.monitor.mu.Unlock()
	out := make([]ops.Sample, 0, len(a.monitor.last))
	for _, s := range a.monitor.last {
		out = append(out, s)
	}
	return out
}

// MonitorRetry 让因认证失败等原因暂停的服务器重新开始监控。
func (a *App) MonitorRetry(serverID string) { a.monitor.unblock(serverID) }

// SetMonitor 修改实时监控的采样间隔（秒）与开关，立即生效。
func (a *App) SetMonitor(interval int, off bool) (store.Settings, error) {
	err := a.store.Update(func(d *store.Data) error {
		d.Settings.General.MonitorInterval = min(max(interval, 1), 60)
		d.Settings.General.MonitorOff = off
		return nil
	})
	if err != nil {
		return store.Settings{}, err
	}
	a.monitor.reconcile()
	return a.GetSettings(), nil
}
