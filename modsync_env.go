package main

// modsync_env.go — ModSync 的"加载器 + 目录"唯一收敛点，逐句对应
// `fabric/.../modsync/ModSyncEnv.java`（29 行）。
//
// Java 侧只有四个静态量：
//   LOADER      = "fabric"                      （ModSyncEnv.java:13）
//   GAME_VERSION= VoxLinkConstants.GAME_VERSION  （ModSyncEnv.java:15，取本端 MC 版本）
//   getModsDir()= gameDir/mods                   （ModSyncEnv.java:21-23）
//   getCacheFile()= gameDir/voxlink/modsync_cache.json（ModSyncEnv.java:26-28）
//
// 桌面应用没有"当前游戏实例"这一运行期事实，只能由用户声明：这里把它做成
// 一份可选的实例清单（每个实例 = 名字 + 加载器 + MC 版本 + mods 目录），
// 活动实例即本端的 (loader, gameVersion, modsDir) 三元组 —— 与 Java 一样，
// 全部 modsync 代码只通过本文件取这四个量，跨加载器传播时只改这里。
// 没有引入任何"实例框架"：解析顺序、字段、缓存文件位置都在这里收敛。
//
// 持久化：不改动 settings.go（其他并行任务的所有权），设置另存一份
// `<配置目录>/VoxLinkApp/modsync.json`（与 settings.json 同目录，见 settings.go:24-36）。
// 需要把它并进 appSettings 的话见交付报告的 hunk。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// modSyncDefaultLoader = ModSyncEnv.java:13 的 LOADER 常量。
const modSyncDefaultLoader = "fabric"

// modSyncProtocolVersion = ModSyncManifestService.java:413 写进清单骨架的协议串。
const modSyncProtocolVersion = "modSync.v1"

// disableDefaultInstanceDetect 测试用：置 true 关闭默认实例自动探测。
// 生产恒为 false（没人写它）。存在的理由：探测会读真实 %APPDATA% 与各盘符，
// 跑在装了 MC 的机器上时，"未配置"这个状态本来无法复现 ——
// 相关用例会从"验证逻辑"退化成"碰运气看这台机器有没有装游戏"。
var disableDefaultInstanceDetect bool

// scope 取值：ModSyncManifestService.java:44-45。
const (
	modSyncScopeRequired = "required"
	modSyncScopeAll      = "all"
)

// modSyncInstance 一个可选的 MC 实例（mods 目录 + 加载器 + MC 版本）。
type modSyncInstance struct {
	Name        string `json:"name"`
	Loader      string `json:"loader"`
	GameVersion string `json:"gameVersion"`
	ModsDir     string `json:"modsDir"`
}

// modSyncSettings ModSync 侧的设置（对应 VoxLinkConfig 的两个开关 + 实例选择）：
// joinRequiredModsCheck 默认 true（VoxLinkConfig.java:48,97），
// hostModSyncPublish 默认 true（VoxLinkConfig.java:50,98）。
type modSyncSettings struct {
	Instances          []modSyncInstance `json:"instances"`
	ActiveInstance     string            `json:"activeInstance"`
	JoinRequiredMods   bool              `json:"joinRequiredModsCheck"`
	HostModSyncPublish bool              `json:"hostModSyncPublish"`
}

func defaultModSyncSettings() modSyncSettings {
	return modSyncSettings{
		Instances:          nil,
		ActiveInstance:     "",
		JoinRequiredMods:   true,
		HostModSyncPublish: true,
	}
}

// modSyncEnv 本端解析出的四元组（ModSyncEnv.java 的 1:1 形态）。
type modSyncEnv struct {
	Loader      string
	GameVersion string
	ModsDir     string
	CacheFile   string
}

// configured=false 表示用户还没声明任何实例：此时无法扫描/diff。
func (e modSyncEnv) configured() bool { return e.ModsDir != "" }

// modSyncState ModSync 全局状态单例：App 引用（供日志/事件/HTTP 出口）、
// 设置、房主侧单线程执行器、清单内存缓存。
// Java 侧这些都是 static 字段（ModSyncManifestService.java:33-38,105-106 等），
// 应用同一进程同一时刻只有一个房间会话，单例成立。
type modSyncState struct {
	mu  sync.Mutex
	app *App
	// dir 设置/缓存文件的落盘目录，跟着绑定它的 App.settingsPath 走。
	// 之前这里恒用 defaultSettingsPath()，于是任何一条 Go 测试只要调一次
	// SaveModSyncSettings，就会把测试实例写进玩家真实的 %APPDATA%/VoxLinkApp/modsync.json
	// （本轮就是这么翻车的）。绑定 App 时记下目录，派生文件全部落在同一处。
	dir      string
	settings modSyncSettings
	loaded   bool

	hostQueue   *modSyncTaskQueue
	hostOnce    sync.Once
	guestQueue  *modSyncTaskQueue
	guestOnce   sync.Once
	envOverride *modSyncEnv // 测试注入

	cachedStateHash string
	cachedRoot      *modSyncCacheRoot
	cacheMu         sync.Mutex
}

var modSyncSvc = &modSyncState{}

// modSyncCurrentApp 供 modsync_log.go 取事件出口；未绑定回落 log.Printf。
func modSyncCurrentApp() *App {
	modSyncSvc.mu.Lock()
	defer modSyncSvc.mu.Unlock()
	return modSyncSvc.app
}

// modSyncBind 由 *App 的绑定方法调用（这些方法定义在本包内，不改动 app.go）。
func (a *App) modSync() *modSyncState {
	modSyncSvc.mu.Lock()
	modSyncSvc.app = a
	if a.settingsPath != "" {
		modSyncSvc.dir = filepath.Dir(a.settingsPath)
	}
	modSyncSvc.mu.Unlock()
	modSyncSvc.ensureSettingsLoaded()
	return modSyncSvc
}

func (s *modSyncState) currentApp() *App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app
}

// ---- 设置持久化 ----

// modSyncSettingsPath = settings.json 同目录的 modsync.json。
// 保留无参版本给"没有 App 绑定"的场合兜底；有 App 时必须走 settingsFile()。
func modSyncSettingsPath() string {
	return filepath.Join(modSyncBaseDirOf(""), "modsync.json")
}

// modSyncBaseDirOf 返回派生文件该落在哪个目录：给了目录就用它（测试/绑定 App 的目录），
// 否则退回用户配置目录。
func modSyncBaseDirOf(dir string) string {
	if dir != "" {
		return dir
	}
	return filepath.Dir(defaultSettingsPath())
}

// settingsFile 读本状态绑定的 App 目录下的 modsync.json（调用方须持 s.mu）。
func (s *modSyncState) settingsFile() string {
	return filepath.Join(modSyncBaseDirOf(s.dir), "modsync.json")
}

func (s *modSyncState) ensureSettingsLoaded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	s.loaded = true
	s.settings = s.readSettingsLocked()
}

func (s *modSyncState) readSettingsLocked() modSyncSettings {
	def := defaultModSyncSettings()
	data, err := os.ReadFile(s.settingsFile())
	if err != nil {
		return def
	}
	var raw struct {
		Instances          []modSyncInstance `json:"instances"`
		ActiveInstance     *string           `json:"activeInstance"`
		JoinRequiredMods   *bool             `json:"joinRequiredModsCheck"`
		HostModSyncPublish *bool             `json:"hostModSyncPublish"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return def
	}
	out := def
	out.Instances = raw.Instances
	if raw.ActiveInstance != nil {
		out.ActiveInstance = *raw.ActiveInstance
	}
	// 缺键时保持默认 true（与 VoxLinkConfig.java:97-98 的 `? getAsBoolean() : true` 同构）
	if raw.JoinRequiredMods != nil {
		out.JoinRequiredMods = *raw.JoinRequiredMods
	}
	if raw.HostModSyncPublish != nil {
		out.HostModSyncPublish = *raw.HostModSyncPublish
	}
	return out
}

func (s *modSyncState) saveSettings(st modSyncSettings) error {
	s.mu.Lock()
	s.settings = st
	s.loaded = true
	s.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	file := s.settingsFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

func (s *modSyncState) snapshotSettings() modSyncSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.settings
	out := modSyncSettings{
		ActiveInstance:     st.ActiveInstance,
		JoinRequiredMods:   st.JoinRequiredMods,
		HostModSyncPublish: st.HostModSyncPublish,
	}
	out.Instances = append([]modSyncInstance(nil), st.Instances...)
	return out
}

// isJoinRequiredModsCheck = VoxLinkConfig.isJoinRequiredModsCheck()（:281-283）
func (s *modSyncState) isJoinRequiredModsCheck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.JoinRequiredMods
}

// isHostModSyncPublish = VoxLinkConfig.isHostModSyncPublish()（:289-291）
func (s *modSyncState) isHostModSyncPublish() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.HostModSyncPublish
}

// ---- 四元组解析（ModSyncEnv.java 的三个静态方法） ----

// env 得到本端 (loader, gameVersion, modsDir, cacheFile)。
// 未配置实例时返回 configured()=false 的空 env。
func (s *modSyncState) env() modSyncEnv {
	s.mu.Lock()
	ov := s.envOverride
	st := s.settings
	dir := s.dir
	s.mu.Unlock()
	if ov != nil {
		e := *ov
		if e.CacheFile == "" {
			e.CacheFile = modSyncCacheFileForDir(dir, e.ModsDir, st.ActiveInstance)
		}
		return e
	}

	ins, ok := s.activeInstance()
	if !ok {
		return modSyncEnv{}
	}
	loader := strings.TrimSpace(ins.Loader)
	if loader == "" {
		loader = modSyncDefaultLoader
	}
	modsDir := strings.TrimSpace(ins.ModsDir)
	return modSyncEnv{
		Loader:      loader,
		GameVersion: strings.TrimSpace(ins.GameVersion),
		ModsDir:     modsDir,
		CacheFile:   modSyncCacheFileForDir(s.dir, modsDir, ins.Name),
	}
}

func (s *modSyncState) activeInstance() (modSyncInstance, bool) {
	st := s.snapshotSettings()
	if len(st.Instances) == 0 {
		// 无显式实例时探测一个"启动器默认实例"（见 modSyncDetectDefaultInstance）
		if ins, ok := modSyncDetectDefaultInstance(); ok {
			return ins, true
		}
		return modSyncInstance{}, false
	}
	for _, in := range st.Instances {
		if in.Name == st.ActiveInstance {
			return in, true
		}
	}
	return st.Instances[0], true
}

// modSyncCacheFileFor 对应 ModSyncEnv.getCacheFile()（:26-28）：
// gameDir/voxlink/modsync_cache.json。mods 目录按启动器惯例是 gameDir/mods，
// 因此 base=="mods" 时取父目录当 gameDir；否则退回应用配置目录（不污染用户目录树）。
func modSyncCacheFileFor(modsDir, instanceName string) string {
	return modSyncCacheFileForDir("", modsDir, instanceName)
}

// modSyncCacheFileForDir 同 modSyncCacheFileFor，但"退回应用配置目录"那一支用传入的
// baseDir（= 绑定 App 的 settingsPath 目录），测试因此不会往玩家目录写缓存。
func modSyncCacheFileForDir(baseDir, modsDir, instanceName string) string {
	if modsDir != "" && strings.EqualFold(filepath.Base(modsDir), "mods") {
		return filepath.Join(filepath.Dir(modsDir), "voxlink", "modsync_cache.json")
	}
	name := "modsync_cache.json"
	if instanceName != "" {
		name = "modsync_cache_" + modSyncSafeName(instanceName) + ".json"
	}
	return filepath.Join(modSyncBaseDirOf(baseDir), name)
}

// modSyncSafeName 只留 [A-Za-z0-9_-]，其余换成 '-'，供缓存文件名使用。
func modSyncSafeName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	out := sb.String()
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// modSyncDetectDefaultInstance 启动器惯例路径（不是 Java 行为，Java 无需探测）：
// 只在本端存在该目录时把它当默认实例，避免"零配置即完全不可用"。
//
// 现代启动器普遍开「版本隔离」：每个整合包在自己的 versions/<名字>/mods 下，
// 而根 .minecraft/mods 常常是空的（实测 D:\pcl\.minecraft\mods 有 0 个 jar，
// 各隔离实例却有 37~144 个）。只认根 mods 等于"配置好了但什么都没同步"。
//
// 因此按这个优先级找：
//  1. versions/ 下**有 jar 的隔离实例**里 jar 最多的那个（最可能是在玩的整合包）
//  2. 根 mods（非隔离的老式布局）
//  3. 都不存在 → 未配置
//
// 加载器与 MC 版本由 modSyncInferFromModsDir 就地推断，玩家仍可在界面上改。
func modSyncDetectDefaultInstance() (modSyncInstance, bool) {
	// 测试注入口：置空即彻底关闭自动探测（让"未配置"成为可复现状态，
	// 否则跑在装了 MC 的机器上，任何"应为未配置"的用例都会变成假失败）。
	if disableDefaultInstanceDetect {
		return modSyncInstance{}, false
	}
	// ① 隔离实例优先
	if ins, ok := modSyncDetectIsolatedInstance(); ok {
		loader, version := modSyncInferFromModsDir(ins.ModsDir)
		if loader == "" {
			loader = modSyncDefaultLoader
		}
		if version != "" {
			ins.GameVersion = version
		}
		ins.Loader = loader
		return ins, true
	}

	// ② 根 mods
	candidates := modSyncDefaultModsDirCandidates()
	for _, dir := range candidates {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			loader, version := modSyncInferFromModsDir(dir)
			if loader == "" {
				loader = modSyncDefaultLoader
			}
			return modSyncInstance{
				Name:        "default",
				Loader:      loader,
				GameVersion: version,
				ModsDir:     dir,
			}, true
		}
	}
	return modSyncInstance{}, false
}

// modSyncDetectIsolatedInstance 在候选 .minecraft 根下找 versions/*/mods 里
// jar 最多的那个隔离实例。没有隔离实例（或全为空）返回 false。
//
// "jar 最多"而不是"版本号最大"：玩家同时装了好几个整合包时，
// 他实际在玩的很可能是最重的那个，而版本号大小跟常用程度无关。
func modSyncDetectIsolatedInstance() (modSyncInstance, bool) {
	var bestDir string
	var bestCount int
	for _, modsDir := range modSyncDefaultModsDirCandidates() {
		gameDir := filepath.Dir(modsDir)
		versionsDir := filepath.Join(gameDir, "versions")
		entries, err := os.ReadDir(versionsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			isoMods := filepath.Join(versionsDir, e.Name(), "mods")
			if st, err := os.Stat(isoMods); err != nil || !st.IsDir() {
				continue
			}
			if n := modSyncCountJars(isoMods); n > bestCount {
				bestCount, bestDir = n, isoMods
			}
		}
	}
	if bestDir == "" {
		return modSyncInstance{}, false
	}
	return modSyncInstance{
		Name:    "default",
		ModsDir: bestDir,
	}, true
}

// modSyncCountJars 数目录里的 .jar 个数（只看文件名，不读内容）。
func modSyncCountJars(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			n++
		}
	}
	return n
}

// modSyncInferFromModsDir 从 mods 目录就地推断 (加载器, MC 版本)，推断不出返回空串。
//
// 依据（按可靠性排序，先命中先返回）：
//  1. 隔离实例的版本 json（同目录 <名字>.json 的 libraries 里有权威坐标）：
//     forge  → net.minecraftforge:fmlloader:1.20.1-47.4.10
//     fabric → net.fabricmc:intermediary:1.20.1
//     这是启动器自己写的，最可信；jar 名只能猜。
//  2. 同级 versions/ 的子目录名 = MC 版本（非隔离布局）。
//  3. mods 里的 jar 文件名带加载器标记 + MC 版本（最后兜底）。
//
// 只读目录名/文件名与小 json，不打开 jar（避免为大整合包付 IO 代价）。
func modSyncInferFromModsDir(modsDir string) (loader, gameVersion string) {
	gameDir := filepath.Dir(modsDir)
	instanceName := filepath.Base(gameDir)

	// versions/ 的位置取决于 mods 处于哪种布局：
	//   a) 版本隔离：<game>/versions/<实例>/mods → versions 在 gameDir 的上一层
	//   b) 根布局  ：<game>/mods               → versions 就在 gameDir 下
	// 判据用「mods 的父目录自身是否位于 versions/ 下」，不能只看目录名。
	versionsDir := filepath.Join(gameDir, "versions")
	if filepath.Base(filepath.Dir(gameDir)) == "versions" {
		versionsDir = filepath.Join(filepath.Dir(filepath.Dir(gameDir)), "versions")
	}

	// ① 版本隔离：实例的版本 json 里有权威坐标（forge/fabric 都把 MC 版本写进 libraries）
	if filepath.Base(filepath.Dir(gameDir)) == "versions" {
		if l, v := modSyncInferFromVersionJSON(filepath.Join(gameDir, instanceName+".json")); v != "" {
			return l, v
		}
	}

	// ② versions/ 目录名
	if ents, err := os.ReadDir(versionsDir); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			if modSyncIsMCVersion(e.Name()) && modSyncVersionBetter(e.Name(), gameVersion) {
				gameVersion = e.Name()
			}
		}
		if loader == "" {
			for _, e := range ents {
				if e.IsDir() {
					if l := modSyncLoaderFromVersionDirName(e.Name()); l != "" {
						loader = l
						break
					}
				}
			}
		}
	}

	// ③ mods 里的 jar 名（最后兜底）
	ents, err := os.ReadDir(modsDir)
	if err != nil {
		return loader, gameVersion
	}
	jarVer := ""
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			continue
		}
		lower := strings.ToLower(e.Name())
		if loader == "" {
			loader = modSyncLoaderFromJarName(lower)
		}
		if gameVersion == "" && jarVer == "" {
			jarVer = modSyncVersionFromJarName(lower)
		}
	}
	if gameVersion == "" {
		gameVersion = jarVer
	}
	return loader, gameVersion
}

// modSyncInferFromVersionJSON 读实例版本 json 的 libraries，取权威 (加载器, MC 版本)。
//
// forge/fabric 都会把自身坐标连同 MC 版本写进 libraries：
//
//	net.minecraftforge:fmlloader:1.20.1-47.4.10   → 1.20.1 + forge
//	net.minecraftforge:neoforge:20.4.237          → 见下方 neoforge 分支
//	net.fabricmc:intermediary:1.20.1              → 1.20.1 + fabric
//
// 文件不存在/解析失败一律返回空串，交给上层继续猜。
func modSyncInferFromVersionJSON(path string) (loader, version string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var doc struct {
		Libraries []struct {
			Name string `json:"name"`
		} `json:"libraries"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return "", ""
	}
	for _, lib := range doc.Libraries {
		lower := strings.ToLower(lib.Name)
		switch {
		case strings.HasPrefix(lower, "net.minecraftforge:fmlloader:"):
			// fmlloader:1.20.1-47.4.10
			if v := modSyncLeadingVersion(lib.Name); v != "" {
				return "forge", v
			}
		case strings.HasPrefix(lower, "net.neoforged:neoforge:"):
			// neoforge:20.4.237 → MC 20.4（NeoForge 21.x 起版本号与 MC 主次版一致）
			if v := modSyncLeadingVersion(lib.Name); v != "" {
				return "neoforge", modSyncNeoAfterToMC(v)
			}
		case strings.HasPrefix(lower, "net.fabricmc:intermediary:"),
			strings.HasPrefix(lower, "net.fabricmc:yarn:"):
			if v := modSyncLeadingVersion(lib.Name); v != "" {
				return "fabric", v
			}
		}
	}
	return "", ""
}

// modSyncLeadingVersion 取坐标最后一段的前导版本号："a:b:1.20.1-47.4.10" → "1.20.1"。
func modSyncLeadingVersion(coordinate string) string {
	parts := strings.Split(coordinate, ":")
	if len(parts) < 3 {
		return ""
	}
	seg := parts[2]
	// 截到第一个非数字/点的字符
	end := 0
	for end < len(seg) {
		r := seg[end]
		if (r >= '0' && r <= '9') || r == '.' {
			end++
			continue
		}
		break
	}
	v := strings.Trim(seg[:end], ".")
	if modSyncIsMCVersion(v) {
		return v
	}
	return ""
}

// modSyncNeoAfterToMC NeoForge 的 MC 版本段还原。
// NeoForge 21.x 起版本号形如 21.1.72，其中 21.1 就是 MC 1.21.1 的省略写法；
// 20.x 时代是 20.4.237 → MC 1.20.4。这里统一补成 MC 的 "1.x.y" 形态。
func modSyncNeoAfterToMC(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	major, minor := parts[0], parts[1]
	// 21.1 → 1.21.1 ；20.4 → 1.20.4
	if len(major) == 2 && major[0] == '2' {
		return "1." + major + "." + minor
	}
	return v
}

// modSyncLoaderFromJarName 从 jar 文件名识别加载器；识别不出返回空串。
func modSyncLoaderFromJarName(name string) string {
	switch {
	case strings.Contains(name, "neoforge"):
		return "neoforge"
	case strings.Contains(name, "fabric"):
		return "fabric"
	case strings.Contains(name, "forge"):
		// 注意顺序：neoforge 已在前面拦下，这里落到真 forge
		return "forge"
	}
	return ""
}

// modSyncLoaderFromVersionDirName 版本目录名里的加载器标记（1.20.1-forge-47.2.0）。
func modSyncLoaderFromVersionDirName(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "neoforge") {
		return "neoforge"
	}
	if strings.Contains(lower, "fabric") {
		return "fabric"
	}
	if strings.Contains(lower, "forge") {
		return "forge"
	}
	return ""
}

// modSyncIsMCVersion 目录名是否像一个 MC 版本号（1.20.1 / 26.2 / 1.21）。
func modSyncIsMCVersion(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// modSyncVersionBetter a 是否比 b 更新（按数值逐段比，不是字典序：
// 字典序会把 "9" 排在 "26" 前面，"1.9" 排在 "1.20" 前面）。
func modSyncVersionBetter(a, b string) bool {
	if b == "" {
		return true
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var ai, bi int
		fmt.Sscanf(as[i], "%d", &ai)
		fmt.Sscanf(bs[i], "%d", &bi)
		if ai != bi {
			return ai > bi
		}
	}
	return len(as) > len(bs)
}

// modSyncVersionFromJarName 从 jar 名里抠 MC 版本。
// 常见形态：fabric-api-0.158.0+26.2.jar、mod-1.20.1-1.2.3.jar、*-mc1.20.1-*.jar。
func modSyncVersionFromJarName(name string) string {
	name = strings.TrimSuffix(name, ".jar")
	// + 后面那种最可靠（Fabric 生态的约定）
	if i := strings.LastIndex(name, "+"); i >= 0 {
		if v := name[i+1:]; modSyncIsMCVersion(v) {
			return v
		}
	}
	// "mc" 前缀
	if i := strings.Index(name, "mc"); i >= 0 && i+2 < len(name) {
		tail := name[i+2:]
		for j := 0; j < len(tail); j++ {
			if tail[j] == '-' || tail[j] == '_' {
				if v := tail[:j]; modSyncIsMCVersion(v) {
					return v
				}
				break
			}
		}
		if v := tail; modSyncIsMCVersion(v) {
			return v
		}
	}
	// 普通 "-1.20.1-" 段
	segs := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for _, seg := range segs {
		if !modSyncIsMCVersion(seg) || !strings.Contains(seg, ".") {
			continue
		}
		// 排除加载器自身版本号：neoforge-21.1.72-universal 的 21.1.72 是 NeoForge 版本，
		// 不是 MC 版本（它的 MC 版本要从实例 json 的 libraries 取，见 modSyncInferFromVersionJSON）。
		// 判据：相同前缀的 jar（neoforge-* / forge-* / fabric-*）紧跟的第一段即加载器版本。
		if modSyncLooksLikeLoaderVersion(name, seg) {
			continue
		}
		return seg
	}
	return ""
}

// modSyncLooksLikeLoaderVersion seg 是否是文件名里紧跟加载器名的那种版本号。
// 例：neoforge-21.1.72-universal → 21.1.72 紧跟 "neoforge-"，判为加载器版本。
func modSyncLooksLikeLoaderVersion(name, seg string) bool {
	idx := strings.Index(name, seg)
	if idx < 0 {
		return false
	}
	head := strings.ToLower(name[:idx])
	for _, l := range []string{"neoforge-", "neoforge_", "forge-", "forge_", "fabric-", "fabric_", "fabric-api-"} {
		if strings.HasSuffix(head, l) {
			return true
		}
	}
	return false
}


// modSyncDefaultModsDirCandidates 候选 mods 目录，**按"最可能是当前在玩的"排序**。
//
// 此前只探 %APPDATA%\.minecraft\mods 一处，实测这台机器上真实存在的两个 MC 目录
// （D:\pcl\.minecraft、D:\Download\.minecraft）全都探不到 → ModSync 形同未配置。
// 玩家用 PCL/HMCL/BakaXL 这类启动器时，.minecraft 往往就落在启动器自己的目录下。
//
// 这里扩成一组：官方启动器默认位优先，其次启动器惯例目录。
// 调用方取"第一个存在且像样的"，不存在就继续往下走（顺序即优先级）。
func modSyncDefaultModsDirCandidates() []string {
	var out []string
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		if appdata, err := os.UserConfigDir(); err == nil && appdata != "" {
			// 官方启动器
			out = append(out, filepath.Join(appdata, ".minecraft", "mods"))
			// 常见第三方启动器：把 .minecraft 放在自己的数据目录下
			for _, launcher := range []string{"PCL", "HMCL", "BakaXL", "MultiMC", "PrismLauncher"} {
				out = append(out, filepath.Join(appdata, launcher, ".minecraft", "mods"))
				out = append(out, filepath.Join(appdata, launcher, "minecraft", "mods"))
			}
		}
	case "darwin":
		if home != "" {
			out = append(out, filepath.Join(home, "Library", "Application Support", "minecraft", "mods"))
			out = append(out, filepath.Join(home, ".minecraft", "mods"))
		}
	default:
		if home != "" {
			out = append(out, filepath.Join(home, ".minecraft", "mods"))
		}
	}

	// 通用兜底：家目录下的 .minecraft（跨平台都有玩家这么放）
	if home != "" {
		hmc := filepath.Join(home, ".minecraft", "mods")
		dup := false
		for _, p := range out {
			if p == hmc {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, hmc)
		}
	}

	// 跨盘符浅扫：玩家把游戏装在 D:/E: 是常态（实测 D:\pcl\.minecraft、
	// D:\Download\.minecraft 就是这种），只探 APPDATA 永远找不到。
	// 只扫「盘符根目录下的一层目录」再拼 .minecraft/mods，不做递归：
	// 深度固定、候选数与盘上顶层目录数同阶，启动代价可忽略。
	out = append(out, modSyncScanOtherDrives()...)
	return out
}

// modSyncScanOtherDrives 在其他盘符的根目录下找 "<top>/.minecraft/mods"。
// 仅 Windows；浅扫一层，遇到就停（同盘只取第一个命中，避免整合包目录爆量）。
func modSyncScanOtherDrives() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	appdata, err := os.UserConfigDir()
	if err != nil || appdata == "" {
		return nil
	}
	systemDrive := strings.ToUpper(filepath.VolumeName(appdata)) // 通常是 "C:"
	if systemDrive == "" {
		return nil
	}
	var out []string
	for _, letter := range []string{"D", "E", "F", "G"} {
		drive := letter + ":"
		if drive == systemDrive {
			continue
		}
		entries, err := os.ReadDir(drive + string(filepath.Separator))
		if err != nil {
			continue // 盘不存在 / 无权限
		}
		found := false
		for _, e := range entries {
			if !e.IsDir() || found {
				continue
			}
			modsDir := filepath.Join(drive+string(filepath.Separator), e.Name(), ".minecraft", "mods")
			if st, err := os.Stat(modsDir); err == nil && st.IsDir() {
				out = append(out, modsDir)
				found = true
			}
		}
	}
	return out
}

// ---- 房主侧单线程执行器（ModSyncManifestService.java:33-37） ----

// Java：Executors.newSingleThreadExecutor(线程名 "VoxLink-ModSync-Host", daemon)，
// 任务队列无界（LinkedBlockingQueue）。Go 侧用 mutex + 切片队列 + 单 worker goroutine
// 复刻同一语义：串行执行、不丢任务、不阻塞提交方。
type modSyncTaskQueue struct {
	mu     sync.Mutex
	cond   chan struct{}
	tasks  []func()
	closed bool
}

func newModSyncTaskQueue() *modSyncTaskQueue {
	q := &modSyncTaskQueue{cond: make(chan struct{}, 1)}
	go q.run()
	return q
}

func (q *modSyncTaskQueue) submit(task func()) {
	if task == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.tasks = append(q.tasks, task)
	q.mu.Unlock()
	// 非阻塞唤醒
	select {
	case q.cond <- struct{}{}:
	default:
	}
}

func (q *modSyncTaskQueue) run() {
	for {
		q.mu.Lock()
		if len(q.tasks) == 0 && q.closed {
			q.mu.Unlock()
			return
		}
		if len(q.tasks) == 0 {
			q.mu.Unlock()
			select {
			case <-q.cond:
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		task := q.tasks[0]
		q.tasks = q.tasks[1:]
		q.mu.Unlock()
		// Java 的每个任务体自带 try/catch(Throwable)（ModSyncManifestService.java:52-58），
		// 单个任务失败不得终止线程。
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					modSyncLogWarn("host manifest task failed: {}", rec)
				}
			}()
			task()
		}()
	}
}

// hostExec 把任务交给房主侧单线程队列（懒启动）。
func (s *modSyncState) hostExec(task func()) {
	s.hostOnce.Do(func() { s.hostQueue = newModSyncTaskQueue() })
	if s.hostQueue != nil {
		s.hostQueue.submit(task)
	}
}

// guestExec 房客侧单线程队列：ModSyncGuestService.java:37-41 的
// "VoxLink-ModSync-Guest" daemon 执行器（同一套串行队列实现）。
func (s *modSyncState) guestExec(task func()) {
	s.guestOnce.Do(func() { s.guestQueue = newModSyncTaskQueue() })
	if s.guestQueue != nil {
		s.guestQueue.submit(task)
	}
}

// ---- 与 App 的接缝（不改动 app.go，方法定义在本包内） ----

// modSyncRoom 取当前"本端当房主"的房间码与 hostToken。
// 对应 ModSyncManifestService.java:81-85（room/code/token 任一为空即非当房主）。
func (a *App) modSyncRoom() (code, token string, ok bool) {
	a.mu.Lock()
	state, isHost := a.state, a.isHost
	code, token = a.code, a.token
	a.mu.Unlock()
	if !isHost || state != "hosting" || code == "" || token == "" {
		return "", "", false
	}
	return code, token, true
}

// modSyncCall 以模组侧的超时预算发一次信令 POST。
//
// 为什么要绕开 apiClient.post：api.go 的通用 client 有 10s 硬超时（api.go:20 httpTimeout），
// 而模组侧 /room/mods/answer 给 20s（ModSyncManifestService.java:94）、
// /room/mods/request 单次给 8s 且服务端长轮询 12s（ModSyncGuestService.java:299、
// server-go/modsmanifest.go:23）。这里复用 api.go 的 buildRequestURL 与信封解析
// （apiClient.finish），但用无整体超时的 a.api.upload 传输 + context 控制逐项预算，
// 从而把超时值保持成与 Java 逐字一致。
func (a *App) modSyncCall(route string, body any, timeout time.Duration) (map[string]any, error) {
	reqURL, err := buildRequestURL(a.baseURL(), route, nil)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &APIError{Code: "INTERNAL", Message: "请求体序列化失败: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, &APIError{Code: "NETWORK", Message: "构造请求失败: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", httpUserAgent)
	req.Header.Set("X-VoxLink-Version", appVersion)

	resp, err := a.api.upload.Do(req)
	if err != nil {
		return nil, &APIError{Code: "NETWORK", Message: "网络请求失败: " + err.Error()}
	}
	raw, err := a.api.finish(resp)
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return map[string]any{}, nil
		}
	}
	return data, nil
}

// modSyncCapabilities 对齐 SignalingClient.java:202-211：
// 上报开关（hostModSyncPublish）关闭时不声明 modSyncV1 ——
// 房客据此与旧版房主一样秒判"无清单"，零等待直通。
func (a *App) modSyncCapabilities() []string {
	s := a.modSync()
	out := append([]string(nil), appCapabilities...)
	if s.isHostModSyncPublish() {
		out = append(out, modSyncCapName)
	}
	return out
}

// modSyncCapName = ProtocolNegotiator.CAP_MOD_SYNC_V1（ProtocolNegotiator.java:24）。
const modSyncCapName = "modSyncV1"

// modSyncIsVoxLinkCode 对应 RoomCodeRouter.isVoxLinkCode（RoomCodeRouter.java:8,24-26）：
// ^[A-HJ-NP-Z2-9]{6}$（不含 I/O/0/1）。
func modSyncIsVoxLinkCode(code string) bool {
	if code == "" {
		return false
	}
	upper := strings.ToUpper(code)
	if len(upper) != 6 {
		return false
	}
	for _, r := range upper {
		switch {
		case r >= 'A' && r <= 'H':
		case r >= 'J' && r <= 'N':
		case r >= 'P' && r <= 'Z':
		case r >= '2' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// modSyncOsStat 薄封装：便于 diff 侧统一处理"文件不存在=跳过"。
func modSyncOsStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// ---- 绑定方法（设置面） ----

// modSyncSettingsView 前端可见的投影：活动实例 + 两个开关。
type modSyncSettingsView struct {
	Configured         bool              `json:"configured"`
	Instances          []modSyncInstance `json:"instances"`
	ActiveInstance     string            `json:"activeInstance"`
	JoinRequiredMods   bool              `json:"joinRequiredModsCheck"`
	HostModSyncPublish bool              `json:"hostModSyncPublish"`
	ModsDir            string            `json:"modsDir"`
	Loader             string            `json:"loader"`
	MCVersion          string            `json:"mcVersion"`
	CacheFile          string            `json:"cacheFile"`
}

// GetModSyncSettings 读回 ModSync 设置与当前生效的四元组。
func (a *App) GetModSyncSettings() string {
	s := a.modSync()
	env := s.env()
	st := s.snapshotSettings()
	return wrapOK(modSyncSettingsView{
		Configured:         env.configured(),
		Instances:          st.Instances,
		ActiveInstance:     st.ActiveInstance,
		JoinRequiredMods:   st.JoinRequiredMods,
		HostModSyncPublish: st.HostModSyncPublish,
		ModsDir:            env.ModsDir,
		Loader:             env.Loader,
		MCVersion:          env.GameVersion,
		CacheFile:          env.CacheFile,
	})
}

// SaveModSyncSettings 保存实例清单与开关；实例变化会让内存缓存失效
// （缓存键本身是 mods 状态哈希，天然随目录变化而变，这里只是省一次无谓命中）。
func (a *App) SaveModSyncSettings(settingsJSON string) string {
	var req modSyncSettingsView
	if err := parseParams(settingsJSON, &req); err != nil {
		return wrapErr("INVALID_PARAMS", err.Error())
	}
	s := a.modSync()
	instances := make([]modSyncInstance, 0, len(req.Instances))
	for _, in := range req.Instances {
		in.Name = strings.TrimSpace(in.Name)
		in.ModsDir = strings.TrimSpace(in.ModsDir)
		in.Loader = strings.TrimSpace(in.Loader)
		in.GameVersion = strings.TrimSpace(in.GameVersion)
		if in.Name == "" || in.ModsDir == "" {
			continue
		}
		instances = append(instances, in)
	}
	active := strings.TrimSpace(req.ActiveInstance)
	if instances == nil {
		instances = []modSyncInstance{}
	}
	// "不同步模组"（activeInstance 为空）是合法状态：玩家选了就不能被自动改成第一个实例。
	// 只有玩家指定的名字不在清单里时才回退，避免"当前"指向一个已删除的实例。
	if active != "" && !hasInstanceNamed(instances, active) {
		active = ""
	}

	st := modSyncSettings{
		Instances:          instances,
		ActiveInstance:     active,
		JoinRequiredMods:   req.JoinRequiredMods,
		HostModSyncPublish: req.HostModSyncPublish,
	}
	if err := s.saveSettings(st); err != nil {
		return wrapErr("IO", err.Error())
	}
	s.cacheMu.Lock()
	s.cachedStateHash = ""
	s.cachedRoot = nil
	s.cacheMu.Unlock()
	return a.GetModSyncSettings()
}

// InferInstanceFromDir 给"新建实例"弹窗用：玩家选/拖入 mods 目录后，
// 自动推断加载器与 MC 版本填进表单。推断不出时返回空串，界面保持玩家已填的内容。
// 参数 {modsDir}；返回 {loader, gameVersion}。
func (a *App) InferInstanceFromDir(reqJSON string) string {
	var req struct {
		ModsDir string `json:"modsDir"`
	}
	if err := parseParams(reqJSON, &req); err != nil {
		return wrapErr("INVALID_PARAMS", err.Error())
	}
	dir := strings.TrimSpace(req.ModsDir)
	if dir == "" {
		return wrapOK(map[string]any{"loader": "", "gameVersion": ""})
	}
	// 容忍玩家直接拖 .minecraft 根目录：只有 mods 子目录存在时自动下沉一层
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return wrapOK(map[string]any{"loader": "", "gameVersion": ""})
	}
	if _, err := os.Stat(filepath.Join(dir, "mods")); err == nil {
		dir = filepath.Join(dir, "mods")
	}
	loader, version := modSyncInferFromModsDir(dir)
	return wrapOK(map[string]any{
		"loader":      loader,
		"gameVersion": version,
		"modsDir":     dir,
	})
}

// hasInstanceNamed 判断清单里是否存在该名字的实例。
func hasInstanceNamed(instances []modSyncInstance, name string) bool {
	for _, in := range instances {
		if in.Name == name {
			return true
		}
	}
	return false
}
