package ops

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sshpilot/internal/sshx"
)

type DiskInfo struct {
	Mount  string  `json:"mount"`
	Device string  `json:"device"`
	Size   int64   `json:"size"`
	Used   int64   `json:"used"`
	UsePct float64 `json:"usePct"`
}

type ProcInfo struct {
	PID     int     `json:"pid"`
	Command string  `json:"command"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
}

type ServerInfo struct {
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Kernel       string     `json:"kernel"`
	Arch         string     `json:"arch"`
	CPUModel     string     `json:"cpuModel"`
	CPUs         int        `json:"cpus"`
	CPUUsage     float64    `json:"cpuUsage"`
	Uptime       int64      `json:"uptime"`
	Load         []float64  `json:"load"`
	MemTotal     int64      `json:"memTotal"`
	MemAvailable int64      `json:"memAvailable"`
	SwapTotal    int64      `json:"swapTotal"`
	SwapFree     int64      `json:"swapFree"`
	Disks        []DiskInfo `json:"disks"`
	IPs          []string   `json:"ips"`
	FailedUnits  []string   `json:"failedUnits"`
	Procs        int        `json:"procs"`
	Users        int        `json:"users"`
	TopProcs     []ProcInfo `json:"topProcs"`
	CollectedAt  int64      `json:"collectedAt"`
}

func (s *ServerInfo) MemUsedPct() float64 {
	if s.MemTotal <= 0 {
		return 0
	}
	return float64(s.MemTotal-s.MemAvailable) * 100 / float64(s.MemTotal)
}

func (s *ServerInfo) SwapUsedPct() float64 {
	if s.SwapTotal <= 0 {
		return 0
	}
	return float64(s.SwapTotal-s.SwapFree) * 100 / float64(s.SwapTotal)
}

const collectScript = `export LC_ALL=C
echo "hostname=$(hostname 2>/dev/null)"
if [ -r /etc/os-release ]; then . /etc/os-release; echo "os=${PRETTY_NAME:-$NAME}"; else echo "os=$(uname -s)"; fi
echo "kernel=$(uname -r)"
echo "arch=$(uname -m)"
echo "cpus=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo)"
echo "cpu_model=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ *//')"
echo "uptime=$(cut -d. -f1 /proc/uptime 2>/dev/null)"
echo "load=$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null)"
awk '/^(MemTotal|MemAvailable|MemFree|Buffers|Cached|SwapTotal|SwapFree):/ {sub(":", "", $1); print "mem_" $1 "=" $2}' /proc/meminfo 2>/dev/null
echo "cpu1=$(head -n1 /proc/stat)"
sleep 0.5
echo "cpu2=$(head -n1 /proc/stat)"
df -P 2>/dev/null | awk 'NR>1 && NF>=6 && (($1 ~ /^\/dev\// && $1 !~ /loop/) || $NF == "/") {print "disk=" $NF "|" $1 "|" $(NF-4) "|" $(NF-3) "|" $(NF-2)}'
echo "ips=$(hostname -I 2>/dev/null || ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | tr '\n' ' ')"
if command -v systemctl >/dev/null 2>&1; then
  systemctl --failed --no-legend --plain 2>/dev/null | awk '{ if ($1 == "●" || $1 == "*") print "failed=" $2; else print "failed=" $1 }'
fi
echo "procs=$(ls -d /proc/[0-9]* 2>/dev/null | wc -l)"
echo "users=$(who 2>/dev/null | wc -l)"
ps -eo pid,pcpu,pmem,comm --sort=-pcpu 2>/dev/null | awk 'NR>1 && NR<=6 {print "proc=" $1 "|" $2 "|" $3 "|" $4}'
true`

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func cpuTimes(line string) (total, idle int64) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0
	}
	for i, v := range f[1:] {
		n := atoi64(v)
		if i < 8 { // user nice system idle iowait irq softirq steal
			total += n
		}
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return total, idle
}

// CollectInfo 在服务器上收集系统信息（不需要 root）。
func CollectInfo(ctx context.Context, pool *sshx.Pool, serverID string) (*ServerInfo, error) {
	res, err := pool.RunScript(ctx, serverID, sshx.Script{Content: collectScript, Interpreter: "sh"}, sshx.ExecOptions{Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	return ParseInfo(res.Stdout), nil
}

func ParseInfo(out string) *ServerInfo {
	info := &ServerInfo{CollectedAt: time.Now().UnixMilli(), Load: []float64{0, 0, 0}, Disks: []DiskInfo{}, IPs: []string{}, FailedUnits: []string{}, TopProcs: []ProcInfo{}}
	var t1, i1, t2, i2 int64
	var memFree, buffers, cached int64
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		switch k {
		case "hostname":
			info.Hostname = v
		case "os":
			info.OS = v
		case "kernel":
			info.Kernel = v
		case "arch":
			info.Arch = v
		case "cpus":
			info.CPUs = int(atoi64(v))
		case "cpu_model":
			info.CPUModel = v
		case "uptime":
			info.Uptime = atoi64(v)
		case "load":
			for i, f := range strings.Fields(v) {
				if i < 3 {
					info.Load[i] = atof(f)
				}
			}
		case "mem_MemTotal":
			info.MemTotal = atoi64(v) * 1024
		case "mem_MemAvailable":
			info.MemAvailable = atoi64(v) * 1024
		case "mem_MemFree":
			memFree = atoi64(v) * 1024
		case "mem_Buffers":
			buffers = atoi64(v) * 1024
		case "mem_Cached":
			cached = atoi64(v) * 1024
		case "mem_SwapTotal":
			info.SwapTotal = atoi64(v) * 1024
		case "mem_SwapFree":
			info.SwapFree = atoi64(v) * 1024
		case "cpu1":
			t1, i1 = cpuTimes(v)
		case "cpu2":
			t2, i2 = cpuTimes(v)
		case "disk":
			p := strings.Split(v, "|")
			if len(p) == 5 {
				used, avail := atoi64(p[3]), atoi64(p[4])
				d := DiskInfo{Mount: p[0], Device: p[1], Size: atoi64(p[2]) * 1024, Used: used * 1024}
				if used+avail > 0 {
					d.UsePct = float64(used) * 100 / float64(used+avail)
				}
				info.Disks = append(info.Disks, d)
			}
		case "ips":
			info.IPs = strings.Fields(v)
		case "failed":
			if v != "" {
				info.FailedUnits = append(info.FailedUnits, v)
			}
		case "procs":
			info.Procs = int(atoi64(v))
		case "users":
			info.Users = int(atoi64(v))
		case "proc":
			p := strings.SplitN(v, "|", 4)
			if len(p) == 4 {
				info.TopProcs = append(info.TopProcs, ProcInfo{PID: int(atoi64(p[0])), CPU: atof(p[1]), Mem: atof(p[2]), Command: p[3]})
			}
		}
	}
	if dt := t2 - t1; dt > 0 {
		info.CPUUsage = float64(dt-(i2-i1)) * 100 / float64(dt)
	}
	// 3.14 以前的内核没有 MemAvailable，用空闲 + 缓冲 + 缓存估算
	if info.MemAvailable == 0 && info.MemTotal > 0 {
		info.MemAvailable = min(memFree+buffers+cached, info.MemTotal)
	}
	return info
}
