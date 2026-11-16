package sshx_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"sshpilot/internal/sshx"
	"sshpilot/internal/testsshd"
)

func newPool(t *testing.T, eps map[string]*sshx.Endpoint) *sshx.Pool {
	t.Helper()
	learned := map[string]string{}
	p := sshx.NewPool(func(id string) (*sshx.Endpoint, error) {
		ep, ok := eps[id]
		if !ok {
			return nil, errors.New("no endpoint")
		}
		cp := *ep
		cp.HostKey = learned[id]
		return &cp, nil
	}, func(id, key, fp string) { learned[id] = key })
	t.Cleanup(p.Close)
	return p
}

func TestPasswordExecAndScript(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})
	ctx := context.Background()

	res, err := pool.Exec(ctx, "s1", "echo hello; echo oops >&2; exit 3", sshx.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" || strings.TrimSpace(res.Stderr) != "oops" || res.ExitCode != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}

	// 脚本通过 stdin 传入，参数作为环境变量
	var streamed strings.Builder
	res, err = pool.RunScript(ctx, "s1", sshx.Script{Content: "echo \"domain=$DOMAIN\"\necho 中文输出", Env: map[string]string{"DOMAIN": "example.com"}},
		sshx.ExecOptions{OnOutput: func(_ string, b []byte) { streamed.Write(b) }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "domain=example.com") || !strings.Contains(res.Stdout, "中文输出") {
		t.Fatalf("script output: %q", res.Stdout)
	}
	if !strings.Contains(streamed.String(), "domain=example.com") {
		t.Fatalf("streamed output missing: %q", streamed.String())
	}

	if _, err := pool.RunScript(ctx, "s1", sshx.Script{Content: "true", Env: map[string]string{"BAD-NAME": "x"}}, sshx.ExecOptions{}); err == nil {
		t.Fatal("invalid env name should be rejected")
	}

	// 超时
	start := time.Now()
	_, err = pool.Exec(ctx, "s1", "sleep 5", sshx.ExecOptions{Timeout: 700 * time.Millisecond})
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("expected timeout, got %v after %s", err, time.Since(start))
	}
}

func TestFilesAndAuthorizedKeys(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})
	ctx := context.Background()

	path := "/tmp/sshpilot-test-" + time.Now().Format("150405.000") + "/conf/app.conf"
	if err := pool.WriteFile(ctx, "s1", path, []byte("listen 443;\n中文\n"), "640", ""); err != nil {
		t.Fatal(err)
	}
	if err := pool.WriteFile(ctx, "s1", path, []byte("listen 8443;\n"), "640", ".bak-test"); err != nil {
		t.Fatal(err)
	}
	got, err := pool.ReadFile(ctx, "s1", path, 0)
	if err != nil || got != "listen 8443;\n" {
		t.Fatalf("read back %q, %v", got, err)
	}
	bak, err := pool.ReadFile(ctx, "s1", path+".bak-test", 0)
	if err != nil || !strings.Contains(bak, "中文") {
		t.Fatalf("backup %q, %v", bak, err)
	}
	if _, err := pool.ReadFile(ctx, "s1", path+".missing", 0); err == nil {
		t.Fatal("missing file should error")
	}

	_, info, err := sshx.GenerateKey("ed25519", 0, "unit@test", "")
	if err != nil {
		t.Fatal(err)
	}
	added, err := pool.AddAuthorizedKey(ctx, "s1", info.PublicKey)
	if err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	added, err = pool.AddAuthorizedKey(ctx, "s1", info.PublicKey)
	if err != nil || added {
		t.Fatalf("second add should be a no-op: %v %v", added, err)
	}
	keys, err := pool.ListAuthorizedKeys(ctx, "s1")
	if err != nil || len(keys) != 1 || keys[0].Fingerprint != info.Fingerprint || keys[0].Comment != "unit@test" {
		t.Fatalf("list: %+v %v", keys, err)
	}
	removed, err := pool.RemoveAuthorizedKey(ctx, "s1", keys[0].Blob)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	keys, _ = pool.ListAuthorizedKeys(ctx, "s1")
	if len(keys) != 0 {
		t.Fatalf("key still present: %+v", keys)
	}
}

func TestKeyAuthHostKeyAndMismatch(t *testing.T) {
	srv := testsshd.Start(t)
	for _, kind := range []string{"ed25519", "rsa", "ecdsa"} {
		pem, info, err := sshx.GenerateKey(kind, 2048, "k", "pass phrase")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Encrypted {
			t.Fatalf("%s key should be encrypted", kind)
		}
		if _, err := sshx.ParseSigner(pem, ""); err == nil {
			t.Fatalf("%s: missing passphrase should fail", kind)
		}
		signer, err := sshx.ParseSigner(pem, "pass phrase")
		if err != nil {
			t.Fatal(err)
		}
		srv.Authorize(signer.PublicKey())
		var learned string
		ep := &sshx.Endpoint{ServerID: "k", Name: "k", Host: srv.Host, Port: srv.Port, User: srv.User, PrivateKey: pem, Passphrase: "pass phrase"}
		cl, err := sshx.Dial(context.Background(), ep, func(_, key, _ string) { learned = key }, 5*time.Second)
		if err != nil {
			t.Fatalf("%s key auth: %v", kind, err)
		}
		cl.Close()
		if learned == "" {
			t.Fatal("host key not learned")
		}
		// 记录的主机密钥匹配时可以连接
		ep.HostKey = learned
		cl, err = sshx.Dial(context.Background(), ep, nil, 5*time.Second)
		if err != nil {
			t.Fatalf("known host key: %v", err)
		}
		cl.Close()
	}
	// 主机密钥不一致时拒绝连接
	_, other, _ := sshx.GenerateKey("ed25519", 0, "", "")
	ep := &sshx.Endpoint{Name: "m", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password, HostKey: other.PublicKey}
	_, err := sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	var mm *sshx.HostKeyMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("expected host key mismatch, got %v", err)
	}
	// 错误密码给出中文提示
	ep = &sshx.Endpoint{Name: "w", Host: srv.Host, Port: srv.Port, User: srv.User, Password: "wrong"}
	_, err = sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "认证失败") {
		t.Fatalf("expected auth failure, got %v", err)
	}
	if ssh.FingerprintSHA256(srv.HostKey) == "" {
		t.Fatal("no host key")
	}
}

func TestJumpHost(t *testing.T) {
	jump := testsshd.Start(t)
	target := testsshd.Start(t)
	j := &sshx.Endpoint{ServerID: "j", Name: "jump", Host: jump.Host, Port: jump.Port, User: jump.User, Password: jump.Password}
	ep := &sshx.Endpoint{ServerID: "t", Name: "target", Host: target.Host, Port: target.Port, User: target.User, Password: target.Password, Jump: j}
	cl, err := sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("dial via jump: %v", err)
	}
	defer cl.Close()
	s, err := cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Output("echo via-jump")
	if err != nil || strings.TrimSpace(string(out)) != "via-jump" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestPublicKeyParsing(t *testing.T) {
	_, info, _ := sshx.GenerateKey("ed25519", 0, "alice@laptop", "")
	parsed, err := sshx.ParsePublicKey(info.PublicKey)
	if err != nil || parsed.Fingerprint != info.Fingerprint || parsed.Comment != "alice@laptop" || parsed.Type != "ED25519" {
		t.Fatalf("%+v %v", parsed, err)
	}
	if sshx.KeyBlob(info.PublicKey) == "" || sshx.KeyBlob("garbage") != "" {
		t.Fatal("KeyBlob")
	}
	if _, err := sshx.ParseSigner("PuTTY-User-Key-File-3: ssh-ed25519", ""); err == nil || !strings.Contains(err.Error(), "PuTTY") {
		t.Fatalf("expected PuTTY hint, got %v", err)
	}
}

func TestStream(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})

	// 命令自行结束：逐行收到输出，返回 nil
	var lines []string
	if err := pool.Stream(context.Background(), "s1", "echo one; echo two", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, ",") != "one,two" {
		t.Fatalf("lines: %q", lines)
	}
	// 非零退出码带上 stderr
	err := pool.Stream(context.Background(), "s1", "echo boom >&2; exit 4", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "4") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("exit error: %v", err)
	}
	// 长时间运行的命令：持续收到输出，取消后及时返回
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		done <- pool.Stream(ctx, "s1", "i=0; while :; do echo tick$i; i=$((i+1)); sleep 0.2; done", func(l string) {
			select {
			case got <- l:
			default:
			}
		})
	}()
	for _, want := range []string{"tick0", "tick1"} {
		select {
		case l := <-got:
			if l != want {
				t.Fatalf("got %q want %q", l, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no streamed output")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel should return context.Canceled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not stop after cancel")
	}
	// 取消后连接仍然可用
	if res, err := pool.Exec(context.Background(), "s1", "echo alive", sshx.ExecOptions{}); err != nil || strings.TrimSpace(res.Stdout) != "alive" {
		t.Fatalf("pool after cancel: %+v %v", res, err)
	}
}
