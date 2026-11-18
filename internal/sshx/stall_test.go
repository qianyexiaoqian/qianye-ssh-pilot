package sshx_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/sshx"
	"sshpilot/internal/testsshd"
)

// shortWaits 把等服务器回应的各个期限缩短，测试结束时恢复。
func shortWaits(t *testing.T) {
	t.Helper()
	reply, every, alive, grace := sshx.ReplyTimeout, sshx.KeepaliveEvery, sshx.KeepaliveTimeout, sshx.StopGrace
	sshx.ReplyTimeout, sshx.KeepaliveEvery, sshx.KeepaliveTimeout, sshx.StopGrace = 500*time.Millisecond, time.Hour, 400*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() {
		sshx.ReplyTimeout, sshx.KeepaliveEvery, sshx.KeepaliveTimeout, sshx.StopGrace = reply, every, alive, grace
	})
}

// within 执行 fn，超过期限还没有返回就让测试失败（“卡住了”正是要防的那种情况）。
func within(t *testing.T, what string, limit time.Duration, fn func()) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s: still blocked after %s", what, limit)
	}
	return time.Since(start)
}

// 经跳板机连接的服务器：跳板机到目标那一段不通了（不断开，只是没有任何回应）。
// 本机到跳板机的连接一直是好的，所以系统不会报错；以前这时打开会话、执行命令、超时和取消都会一直挂着。
func TestStalledHopDoesNotHang(t *testing.T) {
	shortWaits(t)
	jump := testsshd.Start(t)
	target := testsshd.Start(t)
	j := &sshx.Endpoint{ServerID: "j", Name: "jump", Host: jump.Host, Port: jump.Port, User: jump.User, Password: jump.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{
		"t": {ServerID: "t", Name: "target", Host: target.Host, Port: target.Port, User: target.User, Password: target.Password, Jump: j},
	})
	pool.Timeout = time.Second
	ctx := context.Background()
	echo := func(word string) (string, error) {
		res, err := pool.Exec(ctx, "t", "echo "+word, sshx.ExecOptions{Timeout: 20 * time.Second})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(res.Stdout), nil
	}
	if out, err := echo("one"); err != nil || out != "one" {
		t.Fatalf("exec via jump: %q %v", out, err)
	}
	first, err := pool.Get(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}

	// 中转的那一段不通了：命令在期限内带着说得清楚的错误返回，而不是一直等
	jump.Stall(true)
	var execErr error
	took := within(t, "exec on a stalled connection", 6*time.Second, func() { _, execErr = echo("two") })
	if execErr == nil {
		t.Fatal("exec on a stalled connection should fail")
	}
	t.Logf("stalled exec failed after %s: %v", took.Round(10*time.Millisecond), execErr)
	if !first.Closed() {
		t.Error("the unresponsive connection should have been closed")
	}

	// 恢复之后自动重新连接
	jump.Stall(false)
	var out string
	within(t, "exec after recovery", 6*time.Second, func() { out, err = echo("three") })
	if err != nil || out != "three" {
		t.Fatalf("exec after recovery: %q %v", out, err)
	}

	// 命令执行到一半时那一段断了：超时照样生效
	second, _ := pool.Get(ctx, "t")
	var res *sshx.ExecResult
	took = within(t, "timeout on a stalled connection", 6*time.Second, func() {
		go func() {
			time.Sleep(300 * time.Millisecond)
			jump.Stall(true)
		}()
		res, err = pool.Exec(ctx, "t", "echo started; sleep 30", sshx.ExecOptions{Timeout: 900 * time.Millisecond})
	})
	if err == nil || !strings.Contains(err.Error(), "执行超时") || res == nil || !strings.Contains(res.Stdout, "started") {
		t.Fatalf("timeout on a stalled connection: %+v %v", res, err)
	}
	if !second.Closed() {
		t.Error("a connection that does not answer the kill request should be closed")
	}
	if took > 3*time.Second {
		t.Errorf("timeout took %s", took)
	}

	// 取消（界面上的「停止」）也立刻生效：先恢复、连上、执行长命令，再让那一段断掉，然后取消
	jump.Stall(false)
	if out, err = echo("four"); err != nil || out != "four" {
		t.Fatalf("exec before cancel test: %q %v", out, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	took = within(t, "cancel on a stalled connection", 6*time.Second, func() {
		go func() {
			time.Sleep(300 * time.Millisecond)
			jump.Stall(true)
			time.Sleep(200 * time.Millisecond)
			cancel()
		}()
		_, err = pool.Exec(cctx, "t", "sleep 30", sshx.ExecOptions{Timeout: 20 * time.Second})
	})
	if err == nil || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("cancel on a stalled connection: %v", err)
	}
	if took > 3*time.Second {
		t.Errorf("cancel took %s", took)
	}

	// 连接断着的时候取消一个还没开始的操作
	cctx, cancel = context.WithCancel(ctx)
	within(t, "cancel while connecting", 6*time.Second, func() {
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel()
		}()
		_, err = pool.Exec(cctx, "t", "echo five", sshx.ExecOptions{})
	})
	if err == nil {
		t.Fatal("exec with a cancelled context on a stalled connection should fail")
	}

	// 文件传输通道同样不会挂住
	jump.Stall(false)
	if _, err = pool.SFTP(ctx, "t"); err != nil {
		t.Fatalf("sftp via jump: %v", err)
	}
	third, _ := pool.Get(ctx, "t")
	pool.Invalidate("t")
	if !third.Closed() {
		t.Error("invalidate should close the connection")
	}
	if _, err = pool.Get(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	jump.Stall(true)
	within(t, "sftp on a stalled connection", 6*time.Second, func() { _, err = pool.SFTP(ctx, "t") })
	if !errors.Is(err, sshx.ErrNoReply) {
		t.Fatalf("sftp on a stalled connection: %v", err)
	}
}

// 空闲的连接定时探测：中转的那一段不通时，等不到回应就把连接关掉，而不是一直以为它还好着。
func TestKeepaliveClosesUnresponsiveConnection(t *testing.T) {
	shortWaits(t)
	sshx.KeepaliveEvery = 200 * time.Millisecond
	jump := testsshd.Start(t)
	target := testsshd.Start(t)
	j := &sshx.Endpoint{ServerID: "j", Name: "jump", Host: jump.Host, Port: jump.Port, User: jump.User, Password: jump.Password}
	ep := &sshx.Endpoint{ServerID: "t", Name: "target", Host: target.Host, Port: target.Port, User: target.User, Password: target.Password, Jump: j}
	cl, err := sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	// 正常的连接：探测有回应，不会被关掉
	time.Sleep(700 * time.Millisecond)
	if cl.Closed() || !cl.Ping(time.Second) {
		t.Fatal("a healthy connection must stay open")
	}
	jump.Stall(true)
	if cl.Ping(300 * time.Millisecond) {
		t.Error("ping on a stalled connection should fail")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !cl.Closed() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !cl.Closed() {
		t.Fatal("the keepalive should have closed the unresponsive connection")
	}
	// 关掉之后，卡在这条连接上的操作立刻返回
	within(t, "session on a closed connection", 2*time.Second, func() { _, err = cl.OpenSession(context.Background()) })
	if err == nil {
		t.Fatal("opening a session on a closed connection should fail")
	}
}
