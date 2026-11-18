param(
  [Parameter(Mandatory = $true)][int]$ProcId,   # 验收构建的进程号
  [string]$Shot = ""                            # 把窗口（连同边框）截成 PNG 保存到这个路径
)
# 检查验收构建（QianYeSSHPilot-qa.exe）窗口的边框：窗口、客户区和可见范围各在哪里，
# 以及窗口四周各个位置的鼠标命中结果（系统据此决定显示哪种缩放指针、拖动时怎样缩放）。
# 只读取信息，不移动鼠标、不改变窗口。坐标都是物理像素。
Add-Type -ReferencedAssemblies System.Drawing @"
using System; using System.Runtime.InteropServices; using System.Collections.Generic; using System.Drawing; using System.Drawing.Imaging;
public class FW {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetWindow(IntPtr h, uint cmd);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr h, ref POINT p);
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
  [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr h);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr hdc, uint flags);
  [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(IntPtr h, int attr, out RECT r, int size);
  public struct RECT { public int Left, Top, Right, Bottom; }
  public struct POINT { public int X, Y; }
  public static List<IntPtr> Top(uint pid) {
    var list = new List<IntPtr>();
    EnumWindows((h, l) => { uint p; GetWindowThreadProcessId(h, out p); if (p == pid && IsWindowVisible(h) && GetWindow(h, 4) == IntPtr.Zero) list.Add(h); return true; }, IntPtr.Zero);
    return list;
  }
  public static int Hit(IntPtr h, int x, int y) {
    return (int)SendMessage(h, 0x0084, IntPtr.Zero, (IntPtr)(((y & 0xFFFF) << 16) | (x & 0xFFFF)));
  }
  public static void Capture(IntPtr h, string path) {
    RECT r; GetWindowRect(h, out r);
    using (var bmp = new Bitmap(r.Right - r.Left, r.Bottom - r.Top, PixelFormat.Format32bppArgb)) {
      using (var g = Graphics.FromImage(bmp)) { IntPtr dc = g.GetHdc(); PrintWindow(h, dc, 2); g.ReleaseHdc(dc); }
      bmp.Save(path, ImageFormat.Png);
    }
  }
}
"@
[void][FW]::SetProcessDPIAware()
$p = Get-Process QianYeSSHPilot-qa -ErrorAction SilentlyContinue | Where-Object { $_.Id -eq $ProcId }
if (-not $p) { "pid $ProcId is not a test process"; exit 1 }
$names = @{ 0 = 'nowhere'; 1 = 'client'; 2 = 'caption'; 10 = 'left'; 11 = 'right'; 12 = 'top'; 13 = 'topleft'; 14 = 'topright'; 15 = 'bottom'; 16 = 'bottomleft'; 17 = 'bottomright'; 18 = 'border' }
foreach ($h in [FW]::Top([uint32]$ProcId)) {
  $w = New-Object FW+RECT; [void][FW]::GetWindowRect($h, [ref]$w)
  $c = New-Object FW+RECT; [void][FW]::GetClientRect($h, [ref]$c)
  $o = New-Object FW+POINT; [void][FW]::ClientToScreen($h, [ref]$o)
  $v = New-Object FW+RECT; [void][FW]::DwmGetWindowAttribute($h, 9, [ref]$v, 16)
  "scale=$([FW]::GetDpiForWindow($h) / 96.0) maximised=$([FW]::IsZoomed($h))"
  "window  $($w.Left),$($w.Top) - $($w.Right),$($w.Bottom)  ($($w.Right-$w.Left) x $($w.Bottom-$w.Top))"
  "client  $($o.X),$($o.Y) - $($o.X+$c.Right),$($o.Y+$c.Bottom)  ($($c.Right) x $($c.Bottom))"
  "visible $($v.Left),$($v.Top) - $($v.Right),$($v.Bottom)  ($($v.Right-$v.Left) x $($v.Bottom-$v.Top))"
  "borders left=$($o.X-$w.Left) top=$($o.Y-$w.Top) right=$($w.Right-$o.X-$c.Right) bottom=$($w.Bottom-$o.Y-$c.Bottom)"
  $mx = [int](($w.Left + $w.Right) / 2); $my = [int](($w.Top + $w.Bottom) / 2)
  $pts = [ordered]@{
    'left edge'     = @(($w.Left + 3), $my);    'right edge'   = @(($w.Right - 4), $my)
    'bottom edge'   = @($mx, ($w.Bottom - 4));  'top edge'     = @($mx, ($w.Top + 2))
    'bottom-left'   = @(($w.Left + 3), ($w.Bottom - 4)); 'bottom-right' = @(($w.Right - 4), ($w.Bottom - 4))
    'top-left'      = @(($w.Left + 3), ($w.Top + 2));    'top-right'    = @(($w.Right - 4), ($w.Top + 2))
    'inside left'   = @(($o.X + 3), $my);       'inside right' = @(($o.X + $c.Right - 4), $my)
    'inside bottom' = @($mx, ($o.Y + $c.Bottom - 6)); 'centre'   = @($mx, $my)
  }
  foreach ($k in $pts.Keys) {
    $code = [FW]::Hit($h, $pts[$k][0], $pts[$k][1])
    "hit {0,-13} ({1},{2}) = {3}" -f $k, $pts[$k][0], $pts[$k][1], $names[$code]
  }
  if ($Shot) { [FW]::Capture($h, $Shot); "saved $Shot" }
}
