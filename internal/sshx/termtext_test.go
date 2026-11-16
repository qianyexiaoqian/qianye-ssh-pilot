package sshx

import "testing"

func TestCleanTerminalText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"colors", "\x1b[01;32mroot@web\x1b[0m:\x1b[01;34m~\x1b[0m# ls\r\n\x1b[0m\x1b[01;34mnginx\x1b[0m  app.log\r\n", "root@web:~# ls\nnginx  app.log"},
		{"carriage return overwrites the line", "downloading 10%\rdownloading 100%\r\ndone\r\n", "downloading 100%\ndone"},
		{"shorter rewrite with erase to end of line", "progress 100 percent\rdone\x1b[K\r\n", "done"},
		{"backspace editing", "ech\b\b\bls -la\r\n", "ls -la"},
		{"backspace then erase", "lss\b\x1b[K\r\n", "ls"},
		{"window title sequence", "\x1b]0;root@web: ~\x07root@web:~# ", "root@web:~#"},
		{"bracketed paste and charset", "\x1b[?2004h\x1b(Bprompt$ \x1b[?2004l", "prompt$"},
		{"cursor to column", "abcdef\x1b[3GXY\r\n", "abXYef"},
		{"tabs and bells", "a\tb\x07\r\n", "a       b"},
		{"chinese", "\x1b[31m错误：\x1b[0m磁盘已满\r\n", "错误：磁盘已满"},
		{"truncated multibyte start", "\x98\x99ok\n", "ok"},
		{"trailing escape", "ok\x1b", "ok"},
	}
	for _, c := range cases {
		if got := CleanTerminalText(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := TailLines("a\n\n\n\nb\nc\n\n", 2); got != "b\nc" {
		t.Errorf("TailLines: %q", got)
	}
	if got := TailLines("a\n\n\nb", 0); got != "a\n\nb" {
		t.Errorf("TailLines without limit: %q", got)
	}
}
