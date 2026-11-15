package main

import "testing"

func TestParseManifest(t *testing.T) {
	body := `# 注释行
version=1.0.2
channel=stable
notes=更新说明

windows-amd64=https://example.com/VoxLink-UI-windows-amd64.exe
windows-386=https://example.com/VoxLink-UI-windows-386.exe
linux-amd64=https://example.com/VoxLink-UI-linux-amd64
darwin-arm64=https://example.com/VoxLink-UI-darwin-arm64
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
		"VoxLink-UI-windows-amd64.exe": "https://example.com/VoxLink-UI-windows-amd64.exe",
		"VoxLink-UI-windows-386.exe":   "https://example.com/VoxLink-UI-windows-386.exe",
		"VoxLink-UI-linux-amd64":       "https://example.com/VoxLink-UI-linux-amd64",
		"VoxLink-UI-darwin-arm64":      "https://example.com/VoxLink-UI-darwin-arm64",
	}
	for name, u := range want {
		if byName[name] != u {
			t.Errorf("资产 %q = %q, want %q", name, byName[name], u)
		}
	}
	// 未提供的平台不应出现
	if _, ok := byName["VoxLink-UI-linux-arm64"]; ok {
		t.Error("linux-arm64 未在清单中，不应生成资产")
	}
}

func TestParseManifestMissingVersion(t *testing.T) {
	_, err := parseManifest("channel=stable\nwindows-amd64=https://example.com/x.exe\n")
	if err == nil {
		t.Fatal("缺少 version 时应返回错误")
	}
}
