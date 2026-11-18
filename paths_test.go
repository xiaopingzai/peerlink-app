package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivatePathsLayout(t *testing.T) {
	if got := privateSettingsPath(); filepath.Base(got) != "settings.json" {
		t.Errorf("设置文件名 = %q, want settings.json", got)
	}
	if dir := privateUpdatesDir(); filepath.Base(dir) != "updates" {
		t.Errorf("更新缓存目录 = %q, want updates", dir)
	}
	if dir := privateLogsDir(); filepath.Base(dir) != "logs" {
		t.Errorf("日志目录 = %q, want logs", dir)
	}
	if !filepath.IsAbs(privateRootDir()) {
		t.Errorf("私有根应为绝对路径, got %q", privateRootDir())
	}
	// 新私有根走 PeerLink；旧品牌目录 VoxLinkApp 只作兜底读取。
	if rel, _ := filepath.Rel(filepath.Dir(privateRootDir()), privateRootDir()); rel != "PeerLink" {
		t.Errorf("私有根末级 = %q, want PeerLink", rel)
	}
}

func TestCleanUpdateCache(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "old.exe"), []byte("x"), 0o644)
	sub := filepath.Join(dir, "nested")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "a.bin"), []byte("y"), 0o644)

	cleanUpdateCache(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("清理后缓存目录应为空, got %d 项", len(entries))
	}
}

func TestLegacySettingsFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 配置目录变量不同，跳过 XDG 注入用例")
	}
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)

	legacy := legacySettingsPath()
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyCfg := appSettings{ClientTag: "legacy-tag", Language: "zh_cn"}
	data, _ := json.Marshal(legacyCfg)
	if err := os.WriteFile(legacy, data, 0o644); err != nil {
		t.Fatal(err)
	}

	a := &App{settings: defaultSettings()}
	// 新私有目录下还没有设置文件：应兜底读取旧品牌目录。
	a.settingsPath = filepath.Join(cfg, "PeerLink", "settings.json")
	a.loadSettings()

	if a.settings.ClientTag != "legacy-tag" {
		t.Errorf("应兜底读取旧设置 ClientTag=%q", a.settings.ClientTag)
	}
	if a.settings.Language != "zh_cn" {
		t.Errorf("应兜底读取旧设置 Language=%q", a.settings.Language)
	}
}

func TestSettingsPreferNewOverLegacy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 配置目录变量不同，跳过 XDG 注入用例")
	}
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)

	// 旧目录放一套值。
	legacy := legacySettingsPath()
	_ = os.MkdirAll(filepath.Dir(legacy), 0o755)
	legacyData, _ := json.Marshal(appSettings{ClientTag: "legacy", Language: "en_us"})
	_ = os.WriteFile(legacy, legacyData, 0o644)

	// 新目录放另一套值：新文件存在时不得被旧文件覆盖。
	newPath := filepath.Join(cfg, "PeerLink", "settings.json")
	_ = os.MkdirAll(filepath.Dir(newPath), 0o755)
	newData, _ := json.Marshal(appSettings{ClientTag: "new", Language: "zh_cn"})
	_ = os.WriteFile(newPath, newData, 0o644)

	a := &App{settings: defaultSettings()}
	a.settingsPath = newPath
	a.loadSettings()

	if a.settings.ClientTag != "new" {
		t.Errorf("新设置存在时应优先新值 ClientTag=%q", a.settings.ClientTag)
	}
	if a.settings.Language != "zh_cn" {
		t.Errorf("新设置存在时应优先新值 Language=%q", a.settings.Language)
	}
}
