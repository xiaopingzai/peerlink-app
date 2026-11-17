package main

import (
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
