package main

// modsync_infer_20261103_test.go — 从磁盘自动推断 (加载器, MC 版本) 的回归用例。
//
// 背景：默认实例此前硬编码 loader="fabric"、gameVersion=""，等于零配置玩家
// 必须手填两件事才能用 ModSync。而这两个值本来就在磁盘上：
//   - MC 版本 ← 同级 versions/ 的目录名
//   - 加载器   ← mods 里 jar 的文件名
// 这里用真实目录结构 + 真实 jar 命名验证推断规则，含容易踩的坑。

import (
	"os"
	"path/filepath"
	"testing"
)

// mkInstance 造一个 <root>/<ver-dir>/versions/<v> + mods/<jar> 的最小现场。
func mkInstance(t *testing.T, versions []string, jars []string) string {
	t.Helper()
	root := t.TempDir()
	modsDir := filepath.Join(root, ".minecraft", "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		t.Fatalf("mkdir mods: %v", err)
	}
	vdir := filepath.Join(root, ".minecraft", "versions")
	for _, v := range versions {
		if err := os.MkdirAll(filepath.Join(vdir, v), 0o755); err != nil {
			t.Fatalf("mkdir version: %v", err)
		}
	}
	for _, j := range jars {
		if err := os.WriteFile(filepath.Join(modsDir, j), []byte("x"), 0o644); err != nil {
			t.Fatalf("write jar: %v", err)
		}
	}
	return modsDir
}

// TestInferVersionFromVersionsDir versions/ 目录名是最可靠的版本来源。
func TestInferVersionFromVersionsDir(t *testing.T) {
	cases := []struct {
		name     string
		versions []string
		jars     []string
		wantV    string
		wantL    string
	}{
		{
			name:     "单版本 Fabric 实例",
			versions: []string{"1.20.1"},
			jars:     []string{"fabric-api-0.92.2+1.20.1.jar", "sodium-0.5.8.jar"},
			wantV:    "1.20.1",
			wantL:    "fabric",
		},
		{
			name:     "多版本取最新（数值序，不是字典序）",
			versions: []string{"1.9", "1.20.1", "26.2"},
			jars:     []string{"fabric-api-0.158.0+26.2.jar"},
			wantV:    "26.2",
			wantL:    "fabric",
		},
		{
			name:     "Forge 实例",
			versions: []string{"1.20.1-forge-47.2.0"},
			jars:     []string{"journeymap-1.20.1-5.9.7-forge.jar"},
			wantV:    "1.20.1",
			wantL:    "forge",
		},
		{
			name:     "NeoForge 优先于 forge 前缀",
			versions: []string{"1.21.1"},
			jars:     []string{"neoforge-21.1.72-universal.jar"},
			wantV:    "1.21.1",
			wantL:    "neoforge",
		},
		{
			name:     "无 versions 目录，回落到 jar 名里的版本",
			versions: nil,
			jars:     []string{"fabric-api-0.158.0+26.2.jar", "spark-1.10.173-fabric.jar"},
			wantV:    "26.2",
			wantL:    "fabric",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			modsDir := mkInstance(t, c.versions, c.jars)
			loader, ver := modSyncInferFromModsDir(modsDir)
			if ver != c.wantV {
				t.Errorf("版本=%q 期望 %q", ver, c.wantV)
			}
			if loader != c.wantL {
				t.Errorf("加载器=%q 期望 %q", loader, c.wantL)
			}
		})
	}
}

// TestVersionBetterIsNumeric 版本比较必须按数值，不能按字典序。
// 字典序会把 "9" 排在 "26" 前（"9" > "26" 逐字符比），导致选错版本。
func TestVersionBetterIsNumeric(t *testing.T) {
	cases := []struct{ a, b string; want bool }{
		{"26.2", "1.20.1", true},
		{"1.21", "1.9", true},   // 字典序会判错
		{"1.20.1", "1.20", true},
		{"1.20", "1.20.1", false},
		{"9", "26", false},
	}
	for _, c := range cases {
		if got := modSyncVersionBetter(c.a, c.b); got != c.want {
			t.Errorf("versionBetter(%q,%q)=%v 期望 %v", c.a, c.b, got, c.want)
		}
	}
}

// TestLoaderFromJarName neoforge 必须先于 forge 判定，否则 neoforge jar 会被判成 forge。
func TestLoaderFromJarName(t *testing.T) {
	cases := map[string]string{
		"fabric-api-0.92.2+1.20.1.jar":       "fabric",
		"neoforge-21.1.72-universal.jar":     "neoforge",
		"journeymap-1.20.1-5.9.7-forge.jar":  "forge",
		"some-neoforge-fabric-mix.jar":       "neoforge", // neoforge 优先
		"sodium-0.5.8.jar":                   "",          // 无标记
	}
	for name, want := range cases {
		if got := modSyncLoaderFromJarName(name); got != want {
			t.Errorf("%q → %q 期望 %q", name, got, want)
		}
	}
}

// TestVersionFromJarName jar 名里抠版本的各种形态。
func TestVersionFromJarName(t *testing.T) {
	cases := map[string]string{
		"fabric-api-0.158.0+26.2.jar":         "26.2",
		"mod-1.20.1-1.2.3.jar":                "1.20.1",
		"something-mc1.21.4-v2.jar":           "1.21.4",
		"tabtps-fabric-mc26.2-1.4.1.jar":      "26.2",
		"no-version-here.jar":                 "",
	}
	for name, want := range cases {
		if got := modSyncVersionFromJarName(name); got != want {
			t.Errorf("%q → %q 期望 %q", name, got, want)
		}
	}
}

// TestDetectDefaultInstanceFillsBoth 端到端：默认实例探测必须带回版本与加载器，
// 而不是像以前那样留空 —— 那正是"零配置玩家必须手填"的根源。
func TestDetectDefaultInstanceFillsBoth(t *testing.T) {
	modsDir := mkInstance(t, []string{"1.20.1"}, []string{"fabric-api-0.92.2+1.20.1.jar"})
	loader, ver := modSyncInferFromModsDir(modsDir)
	if ver == "" || loader == "" {
		t.Fatalf("推断为空: loader=%q version=%q", loader, ver)
	}
}

// mkIsolated 造一个「版本隔离」现场：versions/<名字>/{mods,<名字>.json}
// 版本 json 里带 forge/fabric 的权威坐标 —— 这是启动器自己写的，比猜 jar 名可靠。
func mkIsolated(t *testing.T, root, name string, libCoords []string, jars []string) string {
	t.Helper()
	instDir := filepath.Join(root, ".minecraft", "versions", name)
	modsDir := filepath.Join(instDir, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	libs := ""
	for i, c := range libCoords {
		if i > 0 { libs += "," }
		libs += `{"name":"` + c + `"}`
	}
	doc := `{"id":"` + name + `","libraries":[` + libs + `]}`
	if err := os.WriteFile(filepath.Join(instDir, name+".json"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	for _, j := range jars {
		_ = os.WriteFile(filepath.Join(modsDir, j), []byte("x"), 0o644)
	}
	return modsDir
}

// TestInferFromVersionJSON 版本 json 里的 forge/fabric 坐标给出权威 (加载器, MC 版本)。
func TestInferFromVersionJSON(t *testing.T) {
	cases := []struct {
		name   string
		coords []string
		wantL  string
		wantV  string
	}{
		{
			name:   "Forge fmlloader 带 MC 版本",
			coords: []string{"net.minecraftforge:fmlloader:1.20.1-47.4.10"},
			wantL:  "forge", wantV: "1.20.1",
		},
		{
			name:   "Fabric intermediary",
			coords: []string{"net.fabricmc:intermediary:1.20.1"},
			wantL:  "fabric", wantV: "1.20.1",
		},
		{
			name:   "NeoForge 21.x 省略写法还原成 1.21.1",
			coords: []string{"net.neoforged:neoforge:21.1.72"},
			wantL:  "neoforge", wantV: "1.21.1",
		},
		{
			name:   "NeoForge 20.x",
			coords: []string{"net.neoforged:neoforge:20.4.237"},
			wantL:  "neoforge", wantV: "1.20.4",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			modsDir := mkIsolated(t, root, "实例", c.coords, []string{"sodium.jar"})
			loader, ver := modSyncInferFromModsDir(modsDir)
			if loader != c.wantL || ver != c.wantV {
				t.Errorf("得到 (%q,%q) 期望 (%q,%q)", loader, ver, c.wantL, c.wantV)
			}
		})
	}
}

// TestIsolatedBeatsRootMods 版本隔离优先于根 mods：
// 实测 D:\pcl\.minecraft\mods 有 0 个 jar，各隔离实例却有几十上百个。
// 选根 mods 等于「配置好了却什么都不同步」。
func TestIsolatedBeatsRootMods(t *testing.T) {
	root := t.TempDir()
	// 根 mods 存在但没有 jar
	if err := os.MkdirAll(filepath.Join(root, ".minecraft", "mods"), 0o755); err != nil {
		t.Fatalf("mkdir root mods: %v", err)
	}
	// 隔离实例有 jar
	isoMods := mkIsolated(t, root, "整合包", []string{"net.minecraftforge:fmlloader:1.20.1-47.4.10"},
		[]string{"a.jar", "b.jar", "c.jar"})

	loader, ver := modSyncInferFromModsDir(isoMods)
	if loader == "" || ver == "" {
		t.Fatalf("隔离实例推断失败: %q %q", loader, ver)
	}
	if n := modSyncCountJars(isoMods); n != 3 {
		t.Errorf("jar 计数=%d 期望 3", n)
	}
}

// TestVersionJSONBeatsJarGuess 版本 json 优先于 jar 名猜测：
// 真实整合包里常有 "0World2Create-Universal-1.19-1.20.X-1.0.0.jar" 这种
// 名字带多个版本号的 jar，靠它猜会猜成 1.19（实际是 1.20.1）。
func TestVersionJSONBeatsJarGuess(t *testing.T) {
	root := t.TempDir()
	modsDir := mkIsolated(t, root, "Zombie",
		[]string{"net.minecraftforge:fmlloader:1.20.1-47.4.10"},
		[]string{"0World2Create-Universal-1.19-1.20.X-1.0.0.jar"})
	loader, ver := modSyncInferFromModsDir(modsDir)
	if ver != "1.20.1" {
		t.Errorf("版本=%q 期望 1.20.1（不能被 jar 名里的 1.19 带偏）", ver)
	}
	if loader != "forge" {
		t.Errorf("加载器=%q 期望 forge", loader)
	}
}
