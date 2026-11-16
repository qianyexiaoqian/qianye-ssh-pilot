package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

func TestQuickCommands(t *testing.T) {
	a := newTestApp(t)
	defaults := a.ListQuickCommands()
	if len(defaults) < 10 {
		t.Fatalf("default quick commands should be seeded, got %d", len(defaults))
	}
	// 名称留空时取命令首行，首尾空白与 Windows 换行被整理
	q, err := a.SaveQuickCommand(store.QuickCommand{Command: "  docker ps -a \r\n", Group: " 容器 ", AutoEnter: true})
	if err != nil || q.ID == "" || q.Name != "docker ps -a" || q.Command != "docker ps -a" || q.Group != "容器" {
		t.Fatalf("save: %+v %v", q, err)
	}
	if _, err := a.SaveQuickCommand(store.QuickCommand{Name: "空命令"}); err == nil {
		t.Fatal("empty command should be rejected")
	}
	if _, err := a.SaveQuickCommand(store.QuickCommand{ID: "missing", Command: "ls"}); err == nil {
		t.Fatal("updating a missing command should fail")
	}
	q.Name, q.Confirm = "全部容器", true
	up, err := a.SaveQuickCommand(q)
	if err != nil || up.ID != q.ID || up.Name != "全部容器" || !up.Confirm || up.CreatedAt != q.CreatedAt {
		t.Fatalf("update: %+v %v", up, err)
	}
	if n := len(a.ListQuickCommands()); n != len(defaults)+1 {
		t.Fatalf("update must not add a command: %d", n)
	}
	// 排序：指定的排在前面，其余保持原有相对顺序
	if err := a.ReorderQuickCommands([]string{q.ID, defaults[2].ID}); err != nil {
		t.Fatal(err)
	}
	list := a.ListQuickCommands()
	if list[0].ID != q.ID || list[1].ID != defaults[2].ID || list[2].ID != defaults[0].ID || list[3].ID != defaults[1].ID {
		t.Fatalf("reorder: %v %v %v %v", list[0].Name, list[1].Name, list[2].Name, list[3].Name)
	}
	// 用户删除的默认命令不会在下次启动时重新出现
	for _, c := range a.ListQuickCommands() {
		if err := a.DeleteQuickCommand(c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.DeleteQuickCommand(q.ID); err == nil {
		t.Fatal("deleting twice should fail")
	}
	st, err := store.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	st.Read(func(d *store.Data) {
		if len(d.QuickCommands) != 0 || !d.QuickSeeded {
			t.Fatalf("deleted defaults came back: %d", len(d.QuickCommands))
		}
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func lastSample(a *App, id string) (ops.Sample, bool) {
	for _, s := range a.MonitorSnapshot() {
		if s.ServerID == id {
			return s, true
		}
	}
	return ops.Sample{}, false
}

func TestMonitorHub(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	a.monitor.grace = 200 * time.Millisecond
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := a.SetMonitor(0, false); err != nil || st.General.MonitorInterval != 1 {
		t.Fatalf("interval should be clamped to 1: %+v %v", st.General, err)
	}

	// 订阅后持续收到采样
	seq := 0
	watch := func(key string, ids ...string) {
		seq++
		a.MonitorWatch(key, ids, seq)
	}
	watch("page", s.ID, s.ID)
	var first ops.Sample
	waitFor(t, "first sample", func() bool {
		sm, ok := lastSample(a, s.ID)
		first = sm
		return ok && sm.Error == "" && sm.MemTotal > 0
	})
	if first.MemPct <= 0 || first.MemPct > 100 || first.CPU < 0 || first.CPU > 100 || first.CPUs < 1 || len(first.Load) != 3 {
		t.Fatalf("sample: %+v", first)
	}
	waitFor(t, "next sample", func() bool {
		sm, _ := lastSample(a, s.ID)
		return sm.Time > first.Time
	})
	if got := a.monitor.active(); len(got) != 1 {
		t.Fatalf("one watcher per server: %v", got)
	}

	// 两个界面都订阅时，其中一个取消不影响采样；全部取消后经过宽限期才停止
	watch("drawer", s.ID)
	watch("page")
	time.Sleep(500 * time.Millisecond)
	if len(a.monitor.active()) != 1 {
		t.Fatal("watcher should stay while another view needs it")
	}
	watch("drawer")
	if len(a.monitor.active()) != 1 {
		t.Fatal("watcher should survive the grace period")
	}
	waitFor(t, "watcher stop", func() bool { return len(a.monitor.active()) == 0 })

	// 前端调用可能乱序到达：先发出的「取消」晚于后发出的「订阅」到达时不能生效
	a.MonitorWatch("page", []string{s.ID}, seq+2)
	a.MonitorWatch("page", nil, seq+1)
	seq += 2
	time.Sleep(400 * time.Millisecond)
	if len(a.monitor.active()) != 1 {
		t.Fatal("a stale unsubscribe must not cancel a newer subscription")
	}
	// 反过来：先发出的「订阅」晚于后发出的「取消」到达时也不能生效
	a.MonitorWatch("page", nil, seq+2)
	a.MonitorWatch("page", []string{s.ID}, seq+1)
	seq += 2
	waitFor(t, "stale subscribe ignored", func() bool { return len(a.monitor.active()) == 0 })

	// 界面异常退出（不再续订）时，订阅到期后自动停止采样
	a.monitor.ttl = 300 * time.Millisecond
	watch("ghost", s.ID)
	waitFor(t, "ghost watcher start", func() bool { return len(a.monitor.active()) == 1 })
	waitFor(t, "expired subscription cleanup", func() bool { return len(a.monitor.active()) == 0 })
	a.monitor.ttl = monitorSubTTL

	// 关闭实时监控后不再建立会话
	if _, err := a.SetMonitor(1, true); err != nil {
		t.Fatal(err)
	}
	watch("page", s.ID)
	time.Sleep(200 * time.Millisecond)
	if len(a.monitor.active()) != 0 {
		t.Fatal("monitor is off, no watcher expected")
	}
	if _, err := a.SetMonitor(1, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watcher restart", func() bool { return len(a.monitor.active()) == 1 })
	watch("page")

	// 认证失败：报告错误后暂停，不自动重试；修正配置后恢复
	bad, err := a.SaveServer(ServerInput{Name: "bad", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: "wrong-password"})
	if err != nil {
		t.Fatal(err)
	}
	watch("page", bad.ID)
	waitFor(t, "auth failure", func() bool {
		sm, ok := lastSample(a, bad.ID)
		return ok && sm.Stopped && sm.Error != ""
	})
	watch("page")
	watch("page", bad.ID)
	time.Sleep(300 * time.Millisecond)
	for _, id := range a.monitor.active() {
		if id == bad.ID {
			t.Fatal("a server paused by an auth failure must not be retried automatically")
		}
	}
	if _, err := a.SaveServer(ServerInput{ID: bad.ID, Name: "bad", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "recovery after fixing the password", func() bool {
		sm, ok := lastSample(a, bad.ID)
		return ok && sm.Error == "" && sm.MemTotal > 0
	})

	// 删除服务器后清理监控状态
	if err := a.DeleteServer(bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastSample(a, bad.ID); ok {
		t.Fatal("samples of a deleted server should be dropped")
	}
}

func TestCertAutoSync(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	ctx := context.Background()
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	base := "/tmp/sshpilot-auto-" + time.Now().Format("150405.000000")
	srcCert, srcKey := base+"/live/fullchain.pem", base+"/live/privkey.pem"
	renew := func() {
		t.Helper()
		c, k := selfSigned(t, "auto.test")
		if err := a.pool.WriteFile(ctx, s.ID, srcCert, []byte(c), "644", ""); err != nil {
			t.Fatal(err)
		}
		if err := a.pool.WriteFile(ctx, s.ID, srcKey, []byte(k), "600", ""); err != nil {
			t.Fatal(err)
		}
	}
	renew()
	cv, err := a.PullCert(PullCertRequest{ServerID: s.ID, CertPath: srcCert, KeyPath: srcKey})
	if err != nil {
		t.Fatal(err)
	}
	// 没有来源的证书不能开启自动同步
	c0, k0 := selfSigned(t, "manual.test")
	manual, err := a.ImportCert(ImportCertRequest{CertPEM: c0, KeyPEM: k0})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetCertAutoSync(manual.ID, true); err == nil {
		t.Fatal("auto sync requires a source server")
	}
	if _, err := a.SyncCertNow(manual.ID); err == nil {
		t.Fatal("sync now requires a source server")
	}
	target, err := a.SaveTarget(store.DeployTarget{CertID: cv.ID, ServerID: s.ID, Preset: "custom", CertPath: base + "/deploy/app.crt", KeyPath: base + "/deploy/app.key",
		Backup: true, TestCommand: "true", ServiceAction: "none"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := a.autoSyncRun(now); len(got) != 0 {
		t.Fatalf("auto sync is off, nothing should run: %+v", got)
	}
	if err := a.SetCertAutoSync(cv.ID, true); err != nil {
		t.Fatal(err)
	}

	// 首次检查：来源没有变化，但部署目标还没有这张证书 → 同步过去
	got := a.autoSyncRun(now)
	if len(got) != 1 || got[0].Changed || got[0].Deployed != 1 || got[0].Failed != 0 || !got[0].OK {
		t.Fatalf("first run: %+v", got)
	}
	if again := a.autoSyncRun(now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("cert checked less than 6h ago must be skipped: %+v", again)
	}

	// 来源续期：自动拉取新证书并同步
	renew()
	got = a.autoSyncRun(now.Add(7 * time.Hour))
	if len(got) != 1 || !got[0].Changed || got[0].Deployed != 1 || !got[0].OK || !strings.Contains(got[0].Message, "已续期") {
		t.Fatalf("renewal: %+v", got)
	}
	view, err := a.GetCert(cv.ID)
	if err != nil || view.Fingerprint == cv.Fingerprint || len(view.History) != 1 || !view.Targets[0].Synced || !view.AutoSync || !view.AutoSyncOK || view.AutoSyncAt == 0 {
		t.Fatalf("cert after renewal: %+v %v", view, err)
	}
	deployed, err := a.pool.ReadFile(ctx, s.ID, target.CertPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := a.PreviewCert(deployed, "", ""); err != nil || p.Fingerprint != view.Fingerprint {
		t.Fatalf("deployed file should be the renewed cert: %v", err)
	}

	// 部署失败（配置检测不通过）会回滚，并且同一版本不会反复重试
	target.TestCommand = "false"
	if _, err := a.SaveTarget(target); err != nil {
		t.Fatal(err)
	}
	renew()
	got = a.autoSyncRun(now.Add(14 * time.Hour))
	if len(got) != 1 || !got[0].Changed || got[0].Failed != 1 || got[0].OK {
		t.Fatalf("failed deploy: %+v", got)
	}
	got = a.autoSyncRun(now.Add(21 * time.Hour))
	if len(got) != 1 || got[0].Changed || got[0].Deployed+got[0].Failed != 0 || got[0].OK || !strings.Contains(got[0].Message, "此前同步失败") {
		t.Fatalf("stuck target must not be retried: %+v", got)
	}
	still, err := a.pool.ReadFile(ctx, s.ID, target.CertPath, 0)
	if err != nil || still != deployed {
		t.Fatalf("failed deploy should roll back to the previous cert: %v", err)
	}

	// 手动「立即检查」走相同流程；来源不可达时报告错误
	target.TestCommand = "true"
	if _, err := a.SaveTarget(target); err != nil {
		t.Fatal(err)
	}
	renew()
	res, err := a.SyncCertNow(cv.ID)
	if err != nil || !res.Changed || res.Deployed != 1 || !res.OK {
		t.Fatalf("sync now: %+v %v", res, err)
	}
	if _, err := a.pool.Exec(ctx, s.ID, "rm -f "+srcCert, sshx.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err = a.SyncCertNow(cv.ID)
	if err != nil || res.OK || !strings.Contains(res.Message, "检查来源服务器失败") {
		t.Fatalf("unreachable source: %+v %v", res, err)
	}
	if view, _ := a.GetCert(cv.ID); view.AutoSyncOK || !strings.Contains(view.AutoSyncMessage, "检查来源服务器失败") {
		t.Fatalf("failure should be recorded on the cert: %+v", view.AutoSyncMessage)
	}
}

func TestAutoInspect(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	if _, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if a.autoInspectTick(now) != nil {
		t.Fatal("auto inspection is off by default")
	}
	if st, err := a.SetInspectInterval(3); err != nil || st.General.InspectInterval != 10 {
		t.Fatalf("interval should be clamped to 10 minutes: %+v %v", st.General, err)
	}
	// 还没有报告时立即巡检；之后未到间隔不重复
	r := a.autoInspectTick(now)
	if r == nil || len(r.Servers) != 1 || a.GetLastInspection() == nil {
		t.Fatalf("first auto inspection: %+v", r)
	}
	if a.autoInspectTick(now.Add(5*time.Minute)) != nil {
		t.Fatal("must wait for the interval")
	}
	if a.autoInspectTick(now.Add(11*time.Minute)) == nil {
		t.Fatal("should inspect again after the interval")
	}
	// 手动巡检进行中时跳过
	a.inspectMu.Lock()
	skipped := a.autoInspectTick(now.Add(time.Hour))
	a.inspectMu.Unlock()
	if skipped != nil {
		t.Fatal("auto inspection must not overlap a manual one")
	}
	if st, _ := a.SetInspectInterval(0); st.General.InspectInterval != 0 || a.autoInspectTick(now.Add(48*time.Hour)) != nil {
		t.Fatal("interval 0 turns auto inspection off")
	}

	// 只提醒新出现的严重问题：数值变化不算新问题
	prev := &ops.Report{Findings: []ops.Finding{{Severity: "critical", ServerID: "s", Category: "磁盘", Title: "磁盘 / 使用率 95%"}}}
	cur := &ops.Report{Findings: []ops.Finding{
		{Severity: "critical", ServerID: "s", Category: "磁盘", Title: "磁盘 / 使用率 97%"},
		{Severity: "critical", ServerID: "s", ServerName: "web", Category: "内存", Title: "内存使用率 93%"},
		{Severity: "warning", ServerID: "s", Category: "负载", Title: "系统负载偏高"},
	}}
	if got := newCritical(prev, cur); len(got) != 1 || got[0] != "[web] 内存使用率 93%" {
		t.Fatalf("new critical findings: %v", got)
	}
	if got := newCritical(nil, cur); len(got) != 2 {
		t.Fatalf("without a previous report every critical finding is new: %v", got)
	}
}
