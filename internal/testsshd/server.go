// Package testsshd 提供一个仅用于测试的最小 SSH 服务器：exec 请求交给本机 bash 执行。
package testsshd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Addr     string
	Host     string
	Port     int
	User     string
	Password string
	Home     string
	HostKey  ssh.PublicKey

	ln         net.Listener
	mu         sync.Mutex
	authorized map[string]bool
	routes     map[string]string
	stalled    bool
	bash       string
}

// Stall 让经这台服务器中转（跳板机）的连接不再转发数据，但也不断开：
// 模拟“跳板机到目标的那一段网络不通了”——本机到跳板机的连接一直是好的，却等不到目标的任何回应。
func (s *Server) Stall(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stalled = on
}

// relay 转发数据；处于 Stall 状态时读到的数据直接丢掉。
func (s *Server) relay(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		s.mu.Lock()
		drop := s.stalled
		s.mu.Unlock()
		if n > 0 && !drop {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// Route 让经过这台服务器中转（跳板机）到 from（"主机:端口"）的连接实际连到 to：
// 用来模拟“只有经过跳板机才能到达”的服务器。
func (s *Server) Route(from, to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = map[string]string{}
	}
	s.routes[from] = to
}

// FindBash 返回可用的 bash 路径（Windows 上使用 Git Bash）。
func FindBash() string {
	if p, err := exec.LookPath("bash"); err == nil && !strings.Contains(strings.ToLower(p), `system32`) {
		return p
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Program Files (x86)\Git\bin\bash.exe`} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// Start 启动服务器；没有 bash 时跳过测试。
func Start(t *testing.T) *Server {
	t.Helper()
	bash := FindBash()
	if bash == "" {
		t.Skip("找不到 bash，跳过 SSH 集成测试")
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{User: "tester", Password: "secret-pass", Home: t.TempDir(), HostKey: signer.PublicKey(), authorized: map[string]bool{}, bash: bash}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.User && string(pw) == s.Password {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() != s.User {
				return nil, errors.New("unknown user")
			}
			s.mu.Lock()
			ok := s.authorized[string(key.Marshal())]
			s.mu.Unlock()
			if ok || s.inAuthorizedKeys(key) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	s.Addr = ln.Addr().String()
	s.Host = "127.0.0.1"
	s.Port = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c, cfg)
		}
	}()
	return s
}

// inAuthorizedKeys 与真实 sshd 一样读取 ~/.ssh/authorized_keys。
func (s *Server) inAuthorizedKeys(key ssh.PublicKey) bool {
	b, err := os.ReadFile(filepath.Join(s.Home, ".ssh", "authorized_keys"))
	if err != nil {
		return false
	}
	for len(b) > 0 {
		pk, _, _, rest, err := ssh.ParseAuthorizedKey(b)
		if err != nil {
			return false
		}
		if string(pk.Marshal()) == string(key.Marshal()) {
			return true
		}
		b = rest
	}
	return false
}

// Authorize 允许某个公钥登录。
func (s *Server) Authorize(key ssh.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorized[string(key.Marshal())] = true
}

func (s *Server) handle(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go s.session(ch, creqs)
		case "direct-tcpip":
			// 供跳板机测试使用的端口转发
			var p struct {
				Host       string
				Port       uint32
				OriginHost string
				OriginPort uint32
			}
			if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
				continue
			}
			addr := net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
			s.mu.Lock()
			if to, ok := s.routes[addr]; ok {
				addr = to
			}
			s.mu.Unlock()
			target, err := net.Dial("tcp", addr)
			if err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, creqs, err := nc.Accept()
			if err != nil {
				target.Close()
				continue
			}
			go ssh.DiscardRequests(creqs)
			go func() {
				s.relay(ch, target)
				ch.Close()
			}()
			go func() {
				s.relay(target, ch)
				target.Close()
			}()
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	env := append(os.Environ(), "HOME="+filepath.ToSlash(s.Home), "LC_ALL=C")
	var cmd *exec.Cmd
	done := make(chan struct{})
	kill := func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	for {
		select {
		case <-done:
			return
		case req, ok := <-reqs:
			if !ok {
				// 客户端关闭了通道（例如超时取消）：结束仍在运行的进程
				kill()
				return
			}
			switch req.Type {
			case "env":
				var kv struct{ K, V string }
				_ = ssh.Unmarshal(req.Payload, &kv)
				env = append(env, kv.K+"="+kv.V)
				_ = req.Reply(true, nil)
			case "signal":
				kill()
			case "pty-req", "window-change":
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			case "subsystem":
				// SFTP：直接读写本机文件系统，工作目录是测试用的主目录
				var p struct{ Name string }
				if err := ssh.Unmarshal(req.Payload, &p); err != nil || p.Name != "sftp" {
					_ = req.Reply(false, nil)
					continue
				}
				srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(filepath.ToSlash(s.Home)))
				if err != nil {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				go func() {
					_ = srv.Serve()
					s.exit(ch, 0)
					close(done)
				}()
			case "exec", "shell":
				var p struct{ Cmd string }
				if req.Type == "exec" {
					if err := ssh.Unmarshal(req.Payload, &p); err != nil {
						_ = req.Reply(false, nil)
						continue
					}
				}
				if cmd != nil {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				if req.Type == "shell" {
					// 模拟登录提示：在客户端订阅输出之前就已经产生的数据
					_, _ = ch.Write([]byte("WELCOME TO TESTSSHD\n"))
					cmd = exec.Command(s.bash)
				} else {
					cmd = exec.Command(s.bash, "-c", p.Cmd)
				}
				cmd.Env = env
				cmd.Dir = s.Home
				stdin, _ := cmd.StdinPipe()
				cmd.Stdout = ch
				cmd.Stderr = ch.Stderr()
				if err := cmd.Start(); err != nil {
					s.exit(ch, 127)
					return
				}
				shell := req.Type == "shell"
				go func() {
					if shell {
						// 模拟 PTY 的 ICRNL：终端回车键发送 \r，交给 bash 前转换为 \n
						_, _ = io.Copy(stdin, crToLF{ch})
					} else {
						_, _ = io.Copy(stdin, ch)
					}
					stdin.Close()
				}()
				go func(c *exec.Cmd) {
					code := 0
					if err := c.Wait(); err != nil {
						var ee *exec.ExitError
						if errors.As(err, &ee) {
							code = ee.ExitCode()
						} else {
							code = 1
						}
					}
					s.exit(ch, code)
					close(done)
				}(cmd)
			default:
				_ = req.Reply(false, nil)
			}
		}
	}
}

type crToLF struct{ r io.Reader }

func (c crToLF) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for i := 0; i < n; i++ {
		if p[i] == '\r' {
			p[i] = '\n'
		}
	}
	return n, err
}

func (s *Server) exit(ch ssh.Channel, code int) {
	status := make([]byte, 4)
	binary.BigEndian.PutUint32(status, uint32(code))
	_, _ = ch.SendRequest("exit-status", false, status)
}
