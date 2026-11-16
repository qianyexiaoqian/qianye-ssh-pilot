package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sshpilot/internal/sshx"
)

// Sample 是实时监控的一次采样。速率类指标（CPU、网络）由相邻两次采样的差值计算。
type Sample struct {
	ServerID  string    `json:"serverId"`
	Time      int64     `json:"time"`
	CPU       float64   `json:"cpu"`    // 使用率 %
	IOWait    float64   `json:"ioWait"` // 等待磁盘 IO 的时间占比 %
	CPUs      int       `json:"cpus"`
	Load      []float64 `json:"load"`
	MemTotal  int64     `json:"memTotal"`
	MemUsed   int64     `json:"memUsed"`
	MemPct    float64   `json:"memPct"`
	SwapTotal int64     `json:"swapTotal"`
	SwapUsed  int64     `json:"swapUsed"`
	HasNet    bool      `json:"hasNet"`
	NetRx     float64   `json:"netRx"` // 字节/秒
	NetTx     float64   `json:"netTx"`
	DiskMount string    `json:"diskMount"` // 使用率最高的分区
	DiskPct   float64   `json:"diskPct"`
	DiskUsed  int64     `json:"diskUsed"`
	DiskSize  int64     `json:"diskSize"`
	Error     string    `json:"error"`
	Stopped   bool      `json:"stopped"` // 出现认证失败等无法自动恢复的错误，已停止重试
}

// ErrMonitorUnsupported 表示服务器没有 /proc（非 Linux 系统），无法实时采样。
var ErrMonitorUnsupported = errors.New("这台服务器不支持实时监控（读取不到 /proc 中的 CPU 与内存信息）")

// MonitorScript 生成远端采样循环。整个循环只用 shell 内置命令加 grep / cat / sleep，
// 每次采样的开销可以忽略；输出管道关闭（客户端断开）或达到运行上限后自行退出，不会留下孤儿进程。
// 脚本中不能出现单引号（整体以 sh -c '…' 执行，兼容各种登录 shell）。
func MonitorScript(interval int) string {
	interval = min(max(interval, 1), 60)
	maxTicks := max(7200/interval, 60) // 约 2 小时后退出，由客户端重新发起
	dfEvery := max(30/interval, 1)     // 磁盘用量变化慢，约 30 秒采一次
	script := fmt.Sprintf(strings.Join([]string{
		`export LC_ALL=C`,
		`n=0`,
		`echo "@cpus $(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null)"`,
		`while [ "$n" -lt %d ]`,
		`do echo "@b" || exit 0`,
		`read -r up rest 2>/dev/null </proc/uptime && echo "@t $up"`,
		`read -r line 2>/dev/null </proc/stat && echo "$line"`,
		`read -r line 2>/dev/null </proc/loadavg && echo "@load $line"`,
		`grep -E "^(MemTotal|MemAvailable|MemFree|Buffers|Cached|SwapTotal|SwapFree):" /proc/meminfo 2>/dev/null`,
		`if [ -r /proc/net/dev ]; then echo "@net"; cat /proc/net/dev; fi`,
		// 失效的网络挂载会让 df 一直卡住，有 timeout 命令时限制在 5 秒内
		`if [ $((n %% %d)) -eq 0 ]; then echo "@df"; if command -v timeout >/dev/null 2>&1; then timeout 5 df -P 2>/dev/null; else df -P 2>/dev/null; fi; fi`,
		`echo "@e" || exit 0`,
		// 第一次采样只用于建立基线，1 秒后就给出首个读数
		`if [ "$n" -eq 0 ]; then sleep 1; else sleep %d; fi`,
		`n=$((n+1))`,
		`done`,
	}, "; "), maxTicks, dfEvery, interval)
	return "sh -c " + sshx.ShellQuote(script)
}

type monitorTick struct {
	up                                        float64 // 远端开机时长（秒），用于计算速率
	total, idle, iowait                       int64
	hasCPU                                    bool
	load                                      [3]float64
	memTotal, memAvail, memFree, memBuf, memC int64
	swapTotal, swapFree                       int64
	rx, tx                                    float64
	hasNet                                    bool
}

const (
	secMain = iota
	secNet
	secDF
)

// sampler 把采样循环输出的文本行还原成 Sample。
type sampler struct {
	cpus   int
	sec    int
	cur    monitorTick
	prev   *monitorTick
	prevAt time.Time
	disk   DiskInfo
	disks  []DiskInfo
	dfSeen bool
	bad    int // 连续读不到 CPU 与内存的采样次数
}

// virtualIface 过滤回环与容器 / 虚拟网桥接口，避免流量被重复统计。
func virtualIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "kube", "lxc", "vnet", "tap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// parseDFLine 解析 df -P 的一行。设备名可能包含空格，因此从行尾向前取字段。
func parseDFLine(line string) (DiskInfo, bool) {
	f := strings.Fields(line)
	n := len(f)
	if n < 6 || !strings.HasSuffix(f[n-2], "%") {
		return DiskInfo{}, false
	}
	size, used, avail := atoi64(f[n-5]), atoi64(f[n-4]), atoi64(f[n-3])
	d := DiskInfo{Mount: f[n-1], Device: strings.Join(f[:n-5], " "), Size: size * 1024, Used: used * 1024}
	if size <= 0 {
		return d, false
	}
	if used+avail > 0 {
		d.UsePct = float64(used) * 100 / float64(used+avail)
	}
	return d, true
}

// realDisk 只保留真实的块设备分区以及根分区（容器、ZFS 等环境的根分区设备名不以 /dev/ 开头）。
func realDisk(d DiskInfo) bool {
	if d.Mount == "/" {
		return true
	}
	return strings.HasPrefix(d.Device, "/dev/") && !strings.Contains(d.Device, "loop")
}

func (s *sampler) feed(line string) (Sample, bool) {
	line = strings.TrimSpace(line)
	switch {
	case line == "":
	case strings.HasPrefix(line, "@cpus"):
		s.cpus = int(atoi64(strings.TrimPrefix(line, "@cpus")))
	case line == "@b":
		s.cur, s.sec, s.dfSeen = monitorTick{}, secMain, false
	case strings.HasPrefix(line, "@t "):
		s.cur.up = atof(line[3:])
	case strings.HasPrefix(line, "@load "):
		for i, f := range strings.Fields(line[6:]) {
			if i < 3 {
				s.cur.load[i] = atof(f)
			}
		}
	case line == "@net":
		s.sec, s.cur.hasNet = secNet, true
	case line == "@df":
		s.sec, s.dfSeen, s.disks = secDF, true, s.disks[:0]
	case line == "@e":
		return s.finish()
	default:
		switch s.sec {
		case secMain:
			s.mainLine(line)
		case secNet:
			s.netLine(line)
		case secDF:
			if d, ok := parseDFLine(line); ok && realDisk(d) {
				s.disks = append(s.disks, d)
			}
		}
	}
	return Sample{}, false
}

func (s *sampler) mainLine(line string) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	if f[0] == "cpu" {
		// user nice system idle iowait irq softirq steal
		var total int64
		for i, v := range f[1:] {
			n := atoi64(v)
			if i < 8 {
				total += n
			}
			switch i {
			case 3:
				s.cur.idle = n
			case 4:
				s.cur.iowait = n
			}
		}
		s.cur.total, s.cur.hasCPU = total, len(f) >= 5
		return
	}
	v := atoi64(f[1]) * 1024
	switch f[0] {
	case "MemTotal:":
		s.cur.memTotal = v
	case "MemAvailable:":
		s.cur.memAvail = v
	case "MemFree:":
		s.cur.memFree = v
	case "Buffers:":
		s.cur.memBuf = v
	case "Cached:":
		s.cur.memC = v
	case "SwapTotal:":
		s.cur.swapTotal = v
	case "SwapFree:":
		s.cur.swapFree = v
	}
}

func (s *sampler) netLine(line string) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return // 表头
	}
	f := strings.Fields(line[idx+1:])
	if len(f) < 9 || virtualIface(strings.TrimSpace(line[:idx])) {
		return
	}
	s.cur.rx += atof(f[0])
	s.cur.tx += atof(f[8])
}

func clampPct(v float64) float64 { return min(max(v, 0), 100) }

func (s *sampler) finish() (Sample, bool) {
	cur := s.cur
	if !cur.hasCPU && cur.memTotal == 0 {
		s.bad++
		return Sample{}, false
	}
	s.bad = 0
	if s.dfSeen {
		s.disk = DiskInfo{}
		for _, d := range s.disks {
			if d.UsePct >= s.disk.UsePct {
				s.disk = d
			}
		}
	}
	now := time.Now()
	prev, prevAt := s.prev, s.prevAt
	s.prev, s.prevAt = &cur, now
	if prev == nil {
		return Sample{}, false // 第一次采样只建立基线
	}
	sm := Sample{Time: now.UnixMilli(), CPUs: s.cpus, Load: cur.load[:], MemTotal: cur.memTotal, SwapTotal: cur.swapTotal,
		DiskMount: s.disk.Mount, DiskPct: s.disk.UsePct, DiskUsed: s.disk.Used, DiskSize: s.disk.Size}
	if dt := cur.total - prev.total; dt > 0 {
		sm.CPU = clampPct(float64(dt-(cur.idle-prev.idle)-(cur.iowait-prev.iowait)) * 100 / float64(dt))
		sm.IOWait = clampPct(float64(cur.iowait-prev.iowait) * 100 / float64(dt))
	}
	if cur.memTotal > 0 {
		avail := cur.memAvail
		if avail == 0 {
			// 3.14 以前的内核没有 MemAvailable，用空闲 + 缓冲 + 缓存估算
			avail = min(cur.memFree+cur.memBuf+cur.memC, cur.memTotal)
		}
		sm.MemUsed = cur.memTotal - avail
		sm.MemPct = clampPct(float64(sm.MemUsed) * 100 / float64(cur.memTotal))
	}
	if cur.swapTotal > 0 {
		sm.SwapUsed = cur.swapTotal - cur.swapFree
	}
	elapsed := cur.up - prev.up
	if elapsed <= 0 {
		elapsed = now.Sub(prevAt).Seconds()
	}
	if cur.hasNet && prev.hasNet && elapsed > 0 {
		sm.HasNet = true
		sm.NetRx = max((cur.rx-prev.rx)/elapsed, 0)
		sm.NetTx = max((cur.tx-prev.tx)/elapsed, 0)
	}
	return sm, true
}

// Watch 在服务器上运行采样循环，每得到一次采样就调用 onSample，直到 ctx 取消。
// 采样循环达到运行上限后正常退出时返回 nil，调用方可以重新发起。
func Watch(ctx context.Context, pool *sshx.Pool, serverID string, interval int, onSample func(Sample)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &sampler{}
	unsupported := false
	err := pool.Stream(ctx, serverID, MonitorScript(interval), func(line string) {
		if sm, ok := s.feed(line); ok {
			sm.ServerID = serverID
			onSample(sm)
		} else if s.bad >= 2 {
			unsupported = true
			cancel()
		}
	})
	if unsupported {
		return ErrMonitorUnsupported
	}
	return err
}
