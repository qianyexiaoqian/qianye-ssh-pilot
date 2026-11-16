package agent

import (
	"regexp"
	"strings"
)

var (
	mdTableRule = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	mdFence     = regexp.MustCompile("^\\s*```")
	mdHeading   = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	mdBullet    = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	mdEmphasis  = regexp.MustCompile(`\*\*|__|~~`)
)

// plainText 去掉 Markdown 的排版符号（标题、加粗、表格线、代码围栏），只留下文字。
// 对话要点要拿去做检索和展示，带着一堆 | 和 ``` 既占字数又干扰匹配。
func plainText(md string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if mdFence.MatchString(line) || mdTableRule.MatchString(line) {
			continue
		}
		line = mdHeading.ReplaceAllString(line, "")
		line = mdBullet.ReplaceAllString(line, "")
		line = mdEmphasis.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, "`", "")
		if strings.Contains(line, "|") {
			// 表格的一行：单元格之间用顿号隔开
			var cells []string
			for _, c := range strings.Split(line, "|") {
				if c = strings.TrimSpace(c); c != "" {
					cells = append(cells, c)
				}
			}
			line = strings.Join(cells, "、")
		}
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}
