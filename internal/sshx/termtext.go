package sshx

import (
	"strings"
	"unicode/utf8"
)

// CleanTerminalText 把终端的原始输出整理成接近屏幕显示效果的纯文本：
// 去掉颜色、光标移动等控制序列，按回车（\r）和退格（\b）改写当前行。
// 这不是完整的终端模拟，全屏程序（vim、top）的输出整理后没有意义，命令行里的命令和结果是准确的。
func CleanTerminalText(raw string) string {
	var lines []string
	var line []rune
	col := 0
	put := func(r rune) {
		if col < len(line) {
			line[col] = r
		} else {
			for len(line) < col {
				line = append(line, ' ')
			}
			line = append(line, r)
		}
		col++
	}
	flush := func() {
		lines = append(lines, strings.TrimRight(string(line), " "))
		line, col = line[:0], 0
	}
	// 跳过残缺的开头（缓冲区从多字节字符中间截断时）
	for len(raw) > 0 && !utf8.RuneStart(raw[0]) {
		raw = raw[1:]
	}
	rs := []rune(raw)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == 0x1b:
			if i+1 >= len(rs) {
				break
			}
			switch rs[i+1] {
			case '[': // CSI：参数字节之后以 0x40–0x7E 结束
				j := i + 2
				for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
					j++
				}
				if j < len(rs) {
					switch rs[j] {
					case 'K': // 清除到行尾
						if col < len(line) {
							line = line[:col]
						}
					case 'G': // 光标移动到指定列（默认第 1 列）
						col = csiNumber(rs[i+2:j], 1) - 1
						col = max(col, 0)
					case 'D': // 光标左移
						col = max(col-csiNumber(rs[i+2:j], 1), 0)
					case 'C': // 光标右移
						col += csiNumber(rs[i+2:j], 1)
					}
				}
				i = j
			case ']', 'P', '^', '_': // OSC / DCS 等：以 BEL 或 ESC \ 结束
				j := i + 2
				for j < len(rs) && rs[j] != 0x07 && !(rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\') {
					j++
				}
				if j < len(rs) && rs[j] == 0x1b {
					j++
				}
				i = j
			case '(', ')', '#', '%': // 字符集选择等三字节序列
				i += 2
			default:
				i++
			}
		case r == '\n':
			flush()
		case r == '\r':
			col = 0
		case r == '\b':
			if col > 0 {
				col--
			}
		case r == '\t':
			for n := 8 - col%8; n > 0; n-- {
				put(' ')
			}
		case r < 0x20 || r == 0x7f:
			// 其余控制字符（响铃等）忽略
		default:
			put(r)
		}
	}
	if len(line) > 0 {
		flush()
	}
	return strings.Join(lines, "\n")
}

func csiNumber(params []rune, def int) int {
	n, seen := 0, false
	for _, r := range params {
		if r < '0' || r > '9' {
			break
		}
		n, seen = n*10+int(r-'0'), true
	}
	if !seen || n == 0 {
		return def
	}
	return n
}

// TailLines 取文本的最后 n 行（忽略结尾的空行，连续空行压缩成一行）。
func TailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "\n")
}
