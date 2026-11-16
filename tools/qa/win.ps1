param(
  [int]$ProcId = 0,        # 要操作的进程；0 表示只列出测试实例的窗口
  [int]$X = -32000, [int]$Y = -32000, [int]$Width = 0, [int]$Height = 0,
  [switch]$Close           # 向窗口发送关闭消息（相当于点右上角的 X）
)
# 只操作验收构建（QianYeSSHPilot-qa.exe）的窗口，而且必须指定进程号；不会碰正常使用中的 QianYeSSHPilot.exe。
# 坐标是物理像素，宽高是逻辑像素（会按窗口的缩放比例换算）。
Add-Type @"
using System; using System.Text; using System.Runtime.InteropServices; using System.Collections.Generic;
public class TW {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetWindow(IntPtr h, uint cmd);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int w, int ht, uint flags);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
  public struct RECT { public int Left, Top, Right, Bottom; }
  public static List<IntPtr> Top(uint pid) {
    var list = new List<IntPtr>();
    EnumWindows((h, l) => { uint p; GetWindowThreadProcessId(h, out p); if (p == pid && IsWindowVisible(h) && GetWindow(h, 4) == IntPtr.Zero) list.Add(h); return true; }, IntPtr.Zero);
    return list;
  }
}
"@
[void][TW]::SetProcessDPIAware()
function Describe($p) {
  foreach ($h in [TW]::Top([uint32]$p.Id)) {
    $sb = New-Object System.Text.StringBuilder 256
    [void][TW]::GetWindowText($h, $sb, 256)
    $r = New-Object TW+RECT
    [void][TW]::GetWindowRect($h, [ref]$r)
    $scale = [TW]::GetDpiForWindow($h) / 96.0
    "pid=$($p.Id) title='$($sb.ToString())' pos=$($r.Left),$($r.Top) size=$([int](($r.Right-$r.Left)/$scale))x$([int](($r.Bottom-$r.Top)/$scale)) scale=$scale"
  }
}
$procs = Get-Process QianYeSSHPilot-qa -ErrorAction SilentlyContinue
if ($ProcId -eq 0) { foreach ($p in $procs) { Describe $p }; if (-not $procs) { "no test windows" }; exit 0 }
$p = $procs | Where-Object { $_.Id -eq $ProcId }
if (-not $p) { "pid $ProcId is not a test process"; exit 1 }
foreach ($h in [TW]::Top([uint32]$ProcId)) {
  if ($Close) { [void][TW]::PostMessage($h, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero); "close sent"; continue }
  $r = New-Object TW+RECT
  [void][TW]::GetWindowRect($h, [ref]$r)
  $scale = [TW]::GetDpiForWindow($h) / 96.0
  $nx = if ($X -eq -32000) { $r.Left } else { $X }
  $ny = if ($Y -eq -32000) { $r.Top } else { $Y }
  $nw = if ($Width -gt 0) { [int]($Width * $scale) } else { $r.Right - $r.Left }
  $nh = if ($Height -gt 0) { [int]($Height * $scale) } else { $r.Bottom - $r.Top }
  # SWP_NOZORDER | SWP_NOACTIVATE：不抢焦点，不改变窗口层级
  [void][TW]::SetWindowPos($h, [IntPtr]::Zero, $nx, $ny, $nw, $nh, 0x0014)
}
Start-Sleep -Milliseconds 300
Describe $p
