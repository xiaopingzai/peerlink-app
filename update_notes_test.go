package main

import (
	"strings"
	"testing"
)

func TestCleanReleaseNotesStripsMarkdown(t *testing.T) {
	md := `## 1.0.2-5 更新内容

### 更新器

- **双源取新**：官网清单与 GitHub Release 两个来源都采集后取较新版本（同版本号保留官网源）；修复旧 ` + "`update.txt`" + ` 盖住 Release 的问题。
- **下载回退**：主源失败回退另一来源；瞬时错误重试一次。

---

[官网说明](https://example.com) 与 ![图标](img.png)
`

	got := cleanReleaseNotes(md)

	// 标记不应裸露
	for _, bad := range []string{"##", "###", "**", "`", "---", "](", "!["} {
		if strings.Contains(got, bad) {
			t.Errorf("清洗后仍含 Markdown 标记 %q:\n%s", bad, got)
		}
	}
	// 文字内容应保留
	for _, want := range []string{"1.0.2-5 更新内容", "更新器", "双源取新：", "下载回退：", "官网说明", "图标"} {
		if !strings.Contains(got, want) {
			t.Errorf("清洗后缺少文字 %q:\n%s", want, got)
		}
	}
	// 列表项应规整为 · 前缀
	if !strings.Contains(got, "· 双源取新：") {
		t.Errorf("列表项未转为 · 前缀:\n%s", got)
	}
}

func TestCleanReleaseNotesPlainTextIdempotent(t *testing.T) {
	// 官网清单 notes 本就是纯文本，清洗不应损坏中文与标点。
	plain := "轻量级 P2P 联机模组，无需端口映射、无需专用服务器。"
	if got := cleanReleaseNotes(plain); got != plain {
		t.Errorf("纯文本被改动:\n got=%q\nwant=%q", got, plain)
	}
}

func TestCleanReleaseNotesEmpty(t *testing.T) {
	if got := cleanReleaseNotes(""); got != "" {
		t.Errorf("空串应返回空, got=%q", got)
	}
	if got := cleanReleaseNotes("   \n\t "); got != "" {
		t.Errorf("空白串应返回空, got=%q", got)
	}
}

func TestCleanReleaseNotesKeepsWildcard(t *testing.T) {
	// PeerLink-UI-* 末尾的 * 是平台通配符，不是强调标记，必须保留。
	md := "两个版本：`PeerLink-UI-*`（图形界面）、`PeerLink-CLI-*`（命令行）。"
	got := cleanReleaseNotes(md)
	if !strings.Contains(got, "PeerLink-UI-*") {
		t.Errorf("平台通配符 * 被误删:\n%s", got)
	}
	if !strings.Contains(got, "PeerLink-CLI-*") {
		t.Errorf("CLI 通配符 * 被误删:\n%s", got)
	}
}

func TestCleanReleaseNotesCollapsesBlankLines(t *testing.T) {
	md := "标题\n\n\n\n正文一行"
	got := cleanReleaseNotes(md)
	if strings.Count(got, "\n\n\n") > 0 {
		t.Errorf("连续空行未压缩:\n%s", got)
	}
}
