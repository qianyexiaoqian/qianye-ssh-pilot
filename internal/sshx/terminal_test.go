package sshx_test

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"sshpilot/internal/sshx"
	"sshpilot/internal/testsshd"
)

func TestTerminalKeepsEarlyOutputAndStreams(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "t", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})

	var mu sync.Mutex
	var out strings.Builder
	exited := make(chan string, 1)
	var termID string
	mgr := sshx.NewTerminalManager(pool, func(name string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(name, "term:data:"):
			b, _ := base64.StdEncoding.DecodeString(data[0].(string))
			out.Write(b)
		case strings.HasPrefix(name, "term:exit:"):
			exited <- data[0].(string)
		}
	})
	id, err := mgr.Open(context.Background(), "s1", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	termID = id
	// 模拟前端稍后才完成订阅：此前的输出必须被缓存
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	early := out.Len()
	mu.Unlock()
	if early != 0 {
		t.Fatal("output must be held until the frontend is ready")
	}
	mgr.Ready(termID)
	if err := mgr.Resize(termID, 120, 40); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Write(termID, "echo 终端-ok\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := out.String()
		mu.Unlock()
		if strings.Contains(s, "WELCOME TO TESTSSHD") && strings.Contains(s, "终端-ok") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	got := out.String()
	mu.Unlock()
	if !strings.Contains(got, "WELCOME TO TESTSSHD") || !strings.Contains(got, "终端-ok") {
		t.Fatalf("terminal output: %q", got)
	}
	if len(mgr.List()) != 1 {
		t.Fatal("terminal should be listed")
	}
	_ = mgr.Write(termID, "exit\n")
	select {
	case reason := <-exited:
		if reason == "" {
			t.Fatal("empty exit reason")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit event")
	}
	if err := mgr.Write(termID, "x"); err == nil {
		t.Fatal("writing to a closed terminal should fail")
	}
}
