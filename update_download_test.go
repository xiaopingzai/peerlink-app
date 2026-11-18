package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDeriveMirrorURL(t *testing.T) {
	gh := "https://github.com/xiaopingzai/peerlink-app/releases/download/1.0.2-5/PeerLink-UI-darwin-arm64"
	want := updateMirrorPrefix + gh

	if got := deriveMirrorURL(gh); got != want {
		t.Errorf("GitHub 链接派生镜像 = %q, want %q", got, want)
	}
	// 已是镜像前缀：不重复派生。
	if got := deriveMirrorURL(want); got != "" {
		t.Errorf("已是镜像应返回空, got %q", got)
	}
	// 非 GitHub 链接不派生。
	other := "https://xpz.fwh.is/peerlink-website/update.txt"
	if got := deriveMirrorURL(other); got != "" {
		t.Errorf("非 GitHub 链接应返回空, got %q", got)
	}
	if got := deriveMirrorURL(""); got != "" {
		t.Errorf("空串应返回空, got %q", got)
	}
}

func TestBuildDownloadURLsOrderAndDedup(t *testing.T) {
	gh1 := "https://github.com/xiaopingzai/peerlink-app/releases/download/1.0.2-5/PeerLink-UI-darwin-arm64"
	gh2 := "https://github.com/xiaopingzai/peerlink-app/releases/download/1.0.2-5/PeerLink-UI-windows-amd64.exe"
	m1 := updateMirrorPrefix + gh1
	m2 := updateMirrorPrefix + gh2

	// 单源 GitHub：补出镜像候选，镜像在前、原链兜底。
	got := buildDownloadURLs(gh1, "")
	want := []string{m1, gh1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("单源候选 = %v, want %v", got, want)
	}

	// 双源：镜像组在前，原链组在后。
	got = buildDownloadURLs(gh1, gh2)
	want = []string{m1, m2, gh1, gh2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("双源候选 = %v, want %v", got, want)
	}

	// 主/回退相同：去重，不出现重复候选。
	got = buildDownloadURLs(gh1, gh1)
	want = []string{m1, gh1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("重复源应去重 = %v, want %v", got, want)
	}

	// 第一候选必须是镜像（镜像优先）。
	if len(got) == 0 || !strings.HasPrefix(got[0], updateMirrorPrefix) {
		t.Errorf("首个候选应为镜像前缀, got %v", got)
	}
}

func TestBuildDownloadURLsKeepsNonGitHubMirror(t *testing.T) {
	// 官网清单自带的镜像直链（已带前缀）不应被二次派生，也不应排在 GitHub 原链之后。
	m := updateMirrorPrefix + "https://github.com/xiaopingzai/peerlink-app/releases/download/1.0.2-5/PeerLink-UI-linux-amd64"
	got := buildDownloadURLs(m, "")
	if len(got) != 1 || got[0] != m {
		t.Errorf("已有镜像直链应原样保留且不重复派生, got %v", got)
	}
}
