package main

import (
	"errors"
	"runtime"
	"testing"
)

func TestParseManifest(t *testing.T) {
	body := `# 注释行
version=1.0.2
channel=stable
notes=更新说明

windows-amd64=https://example.com/PeerLink-UI-windows-amd64.exe
windows-386=https://example.com/PeerLink-UI-windows-386.exe
linux-amd64=https://example.com/PeerLink-UI-linux-amd64
darwin-arm64=https://example.com/PeerLink-UI-darwin-arm64
`
	view, err := parseManifest(body)
	if err != nil {
		t.Fatalf("parseManifest 返回错误: %v", err)
	}
	if view.Tag != "1.0.2" {
		t.Errorf("Tag = %q, want 1.0.2", view.Tag)
	}
	if view.Source != "website" {
		t.Errorf("Source = %q, want website", view.Source)
	}
	if view.Notes != "更新说明" {
		t.Errorf("Notes = %q", view.Notes)
	}

	byName := map[string]string{}
	for _, a := range view.Assets {
		byName[a.Name] = a.URL
	}

	want := map[string]string{
		"PeerLink-UI-windows-amd64.exe": "https://example.com/PeerLink-UI-windows-amd64.exe",
		"PeerLink-UI-windows-386.exe":   "https://example.com/PeerLink-UI-windows-386.exe",
		"PeerLink-UI-linux-amd64":       "https://example.com/PeerLink-UI-linux-amd64",
		"PeerLink-UI-darwin-arm64":      "https://example.com/PeerLink-UI-darwin-arm64",
	}
	for name, u := range want {
		if byName[name] != u {
			t.Errorf("资产 %q = %q, want %q", name, byName[name], u)
		}
	}
	// 未提供的平台不应出现
	if _, ok := byName["PeerLink-UI-linux-arm64"]; ok {
		t.Error("linux-arm64 未在清单中，不应生成资产")
	}
}

func TestParseManifestMissingVersion(t *testing.T) {
	_, err := parseManifest("channel=stable\nwindows-amd64=https://example.com/x.exe\n")
	if err == nil {
		t.Fatal("缺少 version 时应返回错误")
	}
}

func TestPickAssetCurrentPlatform(t *testing.T) {
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	assets := []releaseAsset{
		{Name: "PeerLink-UI-windows-amd64.exe", URL: "https://x/win.exe"},
		{Name: "PeerLink-UI-" + suffix + ext, URL: "https://x/current"},
		{Name: "PeerLink-UI-linux-arm64", URL: "https://x/arm"},
	}
	got := pickAsset(assets)
	if got == nil {
		t.Fatal("应选中当前平台资产")
	}
	if got.URL != "https://x/current" {
		t.Errorf("选中资产 URL = %q, want https://x/current", got.URL)
	}
}

func TestPickAssetLegacyPrefix(t *testing.T) {
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	// 旧版回退源仍用 VoxLink-UI- 前缀，两种前缀都应识别。
	assets := []releaseAsset{
		{Name: "VoxLink-UI-" + suffix, URL: "https://x/legacy"},
	}
	got := pickAsset(assets)
	if got == nil {
		t.Fatal("旧前缀 VoxLink-UI- 资产也应被选中")
	}
	if got.URL != "https://x/legacy" {
		t.Errorf("选中资产 URL = %q, want https://x/legacy", got.URL)
	}
}

func TestPickAssetMissing(t *testing.T) {
	assets := []releaseAsset{
		{Name: "PeerLink-UI-windows-amd64.exe", URL: "https://x/win"},
	}
	if got := pickAsset(assets); got != nil {
		t.Errorf("当前平台无资产时应返回 nil, got %+v", got)
	}
}

func TestPickLatestPrefersNewerGitHub(t *testing.T) {
	// 官网清单滞后在 1.0.2-3，GitHub 已是 1.0.2-4：应取 GitHub，不能被旧清单盖住。
	views := []*releaseView{
		{Tag: "1.0.2-3", Source: "website"},
		{Tag: "1.0.2-4", Source: "github"},
	}
	got := pickLatest(views)
	if got == nil || got.Source != "github" || got.Tag != "1.0.2-4" {
		t.Errorf("应取更新的 GitHub 源, got %+v", got)
	}
}

func TestPickLatestKeepsNewerWebsite(t *testing.T) {
	views := []*releaseView{
		{Tag: "1.0.2-4", Source: "website"},
		{Tag: "1.0.2-3", Source: "github"},
	}
	got := pickLatest(views)
	if got == nil || got.Source != "website" || got.Tag != "1.0.2-4" {
		t.Errorf("官网更新时应保留官网源, got %+v", got)
	}
}

func TestPickLatestSingleSource(t *testing.T) {
	views := []*releaseView{{Tag: "1.0.2-4", Source: "github"}}
	got := pickLatest(views)
	if got == nil || got.Tag != "1.0.2-4" {
		t.Errorf("单源应原样返回, got %+v", got)
	}
}

func TestPickLatestEmpty(t *testing.T) {
	if got := pickLatest(nil); got != nil {
		t.Errorf("无源时应返回 nil, got %+v", got)
	}
}

func TestParseManifestChannel(t *testing.T) {
	body := "version=1.0.2-4\nchannel=beta\nwindows-amd64=https://example.com/x.exe\n"
	view, err := parseManifest(body)
	if err != nil {
		t.Fatalf("parseManifest 返回错误: %v", err)
	}
	if view.Channel != "beta" {
		t.Errorf("Channel = %q, want beta", view.Channel)
	}

	// 缺省 channel 回落 stable。
	view2, err := parseManifest("version=1.0.2-4\nwindows-amd64=https://example.com/x.exe\n")
	if err != nil {
		t.Fatalf("parseManifest 返回错误: %v", err)
	}
	if view2.Channel != "stable" {
		t.Errorf("缺省 Channel = %q, want stable", view2.Channel)
	}
}

func TestPickFallbackAssetAcrossSources(t *testing.T) {
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	name := "PeerLink-UI-" + suffix + ext
	views := []*releaseView{
		{Source: "website", Assets: []releaseAsset{{Name: name, URL: "https://mirror/x"}}},
		{Source: "github", Assets: []releaseAsset{{Name: name, URL: "https://github/x"}}},
	}
	primary := &views[0].Assets[0]
	fb := pickFallbackAsset(views, primary)
	if fb == nil {
		t.Fatal("另一源同平台资产应作为回退")
	}
	if fb.URL != "https://github/x" {
		t.Errorf("回退 URL = %q, want https://github/x", fb.URL)
	}

	// 两源 URL 相同（镜像与原链一致）时不应重复给回退。
	same := []*releaseView{
		{Source: "website", Assets: []releaseAsset{{Name: name, URL: "https://same/x"}}},
		{Source: "github", Assets: []releaseAsset{{Name: name, URL: "https://same/x"}}},
	}
	if got := pickFallbackAsset(same, &same[0].Assets[0]); got != nil {
		t.Errorf("URL 相同时应无回退, got %+v", got)
	}
}

func TestValidateDownloadSize(t *testing.T) {
	cases := []struct {
		name        string
		written     int64
		total       int64
		expected    int64
		wantErr     bool
	}{
		{"完整且长度已知", 100, 100, 0, false},
		{"空包拒绝", 0, 100, 0, true},
		{"Content-Length 不符", 99, 100, 0, true},
		{"长度未知但与已知大小一致", 100, 0, 100, false},
		{"长度未知但与已知大小不符", 90, 0, 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateDownloadSize(c.written, c.total, c.expected)
			if (err != nil) != c.wantErr {
				t.Errorf("validateDownloadSize(%d,%d,%d) err=%v, wantErr=%v", c.written, c.total, c.expected, err, c.wantErr)
			}
		})
	}
}

func TestIsRetryableDownload(t *testing.T) {
	if isRetryableDownload(&httpStatusError{status: 404}) {
		t.Error("4xx 不应重试")
	}
	if !isRetryableDownload(&httpStatusError{status: 500}) {
		t.Error("5xx 应重试")
	}
	if !isRetryableDownload(&httpStatusError{status: 429}) {
		t.Error("429 应重试")
	}
	if !isRetryableDownload(errors.New("connection reset")) {
		t.Error("网络错误应重试")
	}
}
