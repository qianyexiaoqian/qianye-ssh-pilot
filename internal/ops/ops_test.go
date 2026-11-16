package ops

import (
	"strings"
	"testing"
	"time"

	"sshpilot/internal/store"
)

const sample = `hostname=web-01
os=Ubuntu 22.04.4 LTS
kernel=5.15.0-105-generic
arch=x86_64
cpus=4
cpu_model=Intel(R) Xeon(R) Platinum
uptime=4000000
load=6.40 7.10 5.00
mem_MemTotal=8000000
mem_MemAvailable=400000
mem_SwapTotal=0
mem_SwapFree=0
cpu1=cpu  1000 0 500 8000 500 0 0 0 0 0
cpu2=cpu  1600 0 700 8100 600 0 0 0 0 0
disk=/|/dev/vda1|41152736|39000000|2152736
disk=/data|/dev/vdb1|103081248|10000000|93081248
ips=10.0.0.5 172.17.0.1
failed=nginx.service
failed=backup.timer
procs=312
users=2
proc=1234|58.0|12.5|java
`

func TestParseInfoAndAnalyze(t *testing.T) {
	in := ParseInfo(sample)
	if in.Hostname != "web-01" || in.CPUs != 4 || in.Load[1] != 7.10 || len(in.Disks) != 2 || len(in.FailedUnits) != 2 || len(in.TopProcs) != 1 {
		t.Fatalf("parse: %+v", in)
	}
	// 两次采样：总时间差 1000，空闲差 200 → 80%
	if in.CPUUsage < 79.9 || in.CPUUsage > 80.1 {
		t.Fatalf("cpu usage %.2f", in.CPUUsage)
	}
	if in.Disks[0].UsePct < 94 || in.MemUsedPct() < 94 {
		t.Fatalf("disk %.1f mem %.1f", in.Disks[0].UsePct, in.MemUsedPct())
	}

	r := &Report{Servers: []ServerReport{{ServerID: "s1", ServerName: "web-01", Online: true, Info: in}, {ServerID: "s2", ServerName: "db-01", Error: "连接超时"}}}
	now := time.Now()
	certs := []store.Certificate{{ID: "c1", Name: "example.com", Fingerprint: "NEW", NotAfter: now.Add(5*24*time.Hour + 12*time.Hour).UnixMilli()}}
	targets := []store.DeployTarget{{ID: "t1", CertID: "c1", ServerID: "s1", LastFingerprint: "OLD", LastStatus: "success"}}
	Analyze(r, certs, targets, []store.Server{{ID: "s1", Name: "web-01", AuthType: "password"}}, 30)
	text := ""
	for _, f := range r.Findings {
		text += f.Severity + ":" + f.Title + "\n"
	}
	for _, want := range []string{"critical:无法连接", "磁盘 / 使用率", "critical:内存使用率", "系统负载偏高", "2 个服务处于失败状态", "critical:证书 example.com 将在 5 天后过期", "有新版本未同步", "使用密码登录"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing finding %q in:\n%s", want, text)
		}
	}
	if r.Findings[0].Severity != "critical" || r.Score >= 60 || r.Critical == 0 {
		t.Fatalf("ordering/score: score=%d first=%+v", r.Score, r.Findings[0])
	}
	if p := ReportPrompt(r); !strings.Contains(p, "web-01") || !strings.Contains(p, "健康分") {
		t.Fatalf("prompt: %s", p)
	}
}

// 一台真实 Linux 服务器上采样循环的输出（两次采样，间隔 2 秒）。
const monitorOutput = `@cpus 4
@b
@t 1000.00
cpu  10000 0 5000 80000 2000 0 500 0 0 0
@load 0.52 0.40 0.31 2/345 9876
MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    6000000 kB
Buffers:          200000 kB
Cached:          3000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
@net
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999999   1000    0    0    0     0          0         0  9999999   1000    0    0    0     0       0          0
  eth0: 1000000   5000    0    0    0     0          0         0   500000   4000    0    0    0     0       0          0
docker0:  777777    100    0    0    0     0          0         0   777777    100    0    0    0     0       0          0
@df
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/vda1         41152736 20000000  21152736      49% /
tmpfs              4000000        0   4000000       0% /dev/shm
/dev/loop0           65536    65536         0     100% /snap/core20/1
/dev/vdb1        103081248 90000000  13081248      88% /data
@e
@b
@t 1002.00
cpu  10300 0 5100 80350 2050 0 500 0 0 0
@load 0.60 0.42 0.32 1/345 9877
MemTotal:        8000000 kB
MemFree:          400000 kB
MemAvailable:    5600000 kB
Buffers:          200000 kB
Cached:          3000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
@net
    lo: 9999999   1000    0    0    0     0          0         0  9999999   1000    0    0    0     0       0          0
  eth0: 3048576   5000    0    0    0     0          0         0   602400   4000    0    0    0     0       0          0
@e
`

func feedAll(s *sampler, text string) []Sample {
	var out []Sample
	for _, line := range strings.Split(text, "\n") {
		if sm, ok := s.feed(line); ok {
			out = append(out, sm)
		}
	}
	return out
}

func TestMonitorSampler(t *testing.T) {
	out := feedAll(&sampler{}, monitorOutput)
	// 第一次采样只建立基线，第二次才产生读数
	if len(out) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(out))
	}
	s := out[0]
	// 总时间差 800：空闲 350、iowait 50 → 使用率 50%，iowait 6.25%
	if s.CPU != 50 || s.IOWait != 6.25 || s.CPUs != 4 {
		t.Fatalf("cpu: %+v", s)
	}
	if s.MemTotal != 8000000*1024 || s.MemUsed != 2400000*1024 || s.MemPct != 30 || s.SwapUsed != 500000*1024 {
		t.Fatalf("mem: %+v", s)
	}
	if s.Load[0] != 0.60 || s.Load[2] != 0.32 {
		t.Fatalf("load: %v", s.Load)
	}
	// 只统计 eth0：2 秒内下行 2048576 字节、上行 102400 字节
	if !s.HasNet || s.NetRx != 1024288 || s.NetTx != 51200 {
		t.Fatalf("net: rx=%v tx=%v", s.NetRx, s.NetTx)
	}
	// 磁盘取使用率最高的真实分区（忽略 tmpfs 与 loop），并沿用到没有 df 输出的采样
	if s.DiskMount != "/data" || s.DiskPct < 87 || s.DiskPct > 88 {
		t.Fatalf("disk: %+v", s)
	}
}

func TestMonitorSamplerEdgeCases(t *testing.T) {
	// 旧内核没有 MemAvailable：用空闲 + 缓冲 + 缓存估算；没有网络信息时不报告速率
	old := "@b\ncpu 100 0 100 800\nMemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 100 kB\nCached: 300 kB\n@e\n" +
		"@b\ncpu 150 0 150 900\nMemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 100 kB\nCached: 300 kB\n@e\n"
	out := feedAll(&sampler{}, old)
	if len(out) != 1 || out[0].MemPct != 50 || out[0].CPU != 50 || out[0].HasNet {
		t.Fatalf("old kernel: %+v", out)
	}
	// 读不到 CPU 和内存（非 Linux）时不产生采样，并累计失败次数
	s := &sampler{}
	if out := feedAll(s, "@b\n@e\n@b\n@e\n"); len(out) != 0 || s.bad != 2 {
		t.Fatalf("unsupported: %+v bad=%d", out, s.bad)
	}
	// 设备名带空格、容器的根分区（设备名不是 /dev/ 开头）
	if d, ok := parseDFLine("C:/Program Files/Git   208642044 127041472  81600572      61% /"); !ok || d.Mount != "/" || d.Device != "C:/Program Files/Git" || !realDisk(d) {
		t.Fatalf("df with spaces: %+v %v", d, ok)
	}
	if d, ok := parseDFLine("overlay 100 60 40 60% /"); !ok || !realDisk(d) || d.UsePct != 60 {
		t.Fatalf("overlay root: %+v %v", d, ok)
	}
	if _, ok := parseDFLine("Filesystem     1024-blocks     Used Available Capacity Mounted on"); ok {
		t.Fatal("header must be skipped")
	}
	if !virtualIface("veth12ab") || !virtualIface("lo") || virtualIface("eth0") || virtualIface("ens3") {
		t.Fatal("virtual interface filter")
	}
	if sc := MonitorScript(3); strings.Count(sc, "'") != 2 || !strings.HasPrefix(sc, "sh -c '") {
		t.Fatalf("script must be a single quoted sh -c command: %s", sc)
	}
}
