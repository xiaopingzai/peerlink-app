package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ============================================================
// 自动更新：探测本派生仓库 GitHub Release 最新版本
// 流程：检查 → 提示 → 下载 → 替换二进制 → 重启
// ============================================================

const (
	// 发布仓（本派生应用端独立仓）。tag 与资产名以该仓 Release 为准。
	updateGHOwner = "xiaopingzai"
	updateGHRepo  = "peerlink-app"

	// 官网更新清单（INI 式 update.txt）。优先探测该源，GitHub Release 作为回退。
	updateManifestURL = "https://xpz.fwh.is/peerlink-website/update.txt"

	// 探测窗口
	updateTotalTimeout    = 8 * time.Second
	updateDownloadTimeout = 30 * time.Minute
)

// updateInfo 检查结果，透传给前端。
type updateInfo struct {
	Available   bool   `json:"available"`
	Version     string `json:"version"`
	Current     string `json:"current"`
	Source      string `json:"source"` // github / website
	Channel     string `json:"channel,omitempty"`
	AssetName   string `json:"assetName"`
	DownloadURL string `json:"downloadUrl"`
	FallbackURL string `json:"fallbackUrl,omitempty"`
	AssetSize   int64  `json:"-"`
	Notes       string `json:"notes"`
	CheckedAt   int64  `json:"checkedAt"`
	Error       string `json:"error,omitempty"`
}

// releaseAsset 双源归一后的资产。
type releaseAsset struct {
	Name string
	URL  string
	Size int64
}

// releaseView 双源归一后的 release。
type releaseView struct {
	Tag     string
	Notes   string
	Channel string
	Assets  []releaseAsset
	Source  string
}

type updateState struct {
	mu          sync.Mutex
	info        updateInfo
	checking    bool
	downloading bool
	progress    int // 0-100
}

var sharedUpdate = &updateState{}

// ---------- 版本比较 ----------

// parseSemver "1.0.2-2" -> [1,0,2]（容忍前导 v 与非数字段，核心三段参与比较）
func parseSemver(s string) []int {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	out := make([]int, 0, 3)
	for _, p := range strings.Split(s, ".") {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out[:3]
}

// parseSuffix 取 "-N" 迭代序号：无后缀记 0，"1.0.2-2" -> 2。
// 本项目用 -N 表示同一核心号下的补丁迭代，序号越大越新。
func parseSuffix(s string) int {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	i := strings.Index(s, "-")
	if i < 0 {
		return 0
	}
	n := 0
	for _, c := range s[i+1:] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// versionNewer 远端是否比本地新：先比核心三段，核心相同再比 -N 迭代序号。
func versionNewer(remote, local string) bool {
	r, l := parseSemver(remote), parseSemver(local)
	for i := 0; i < 3; i++ {
		if r[i] > l[i] {
			return true
		}
		if r[i] < l[i] {
			return false
		}
	}
	return parseSuffix(remote) > parseSuffix(local)
}

// ---------- 双源探测 ----------

type probeResult struct {
	view *releaseView
	err  error
}

func ghProbe(ctx context.Context) (*releaseView, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", updateGHOwner, updateGHRepo)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", httpUserAgent)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github status %d", resp.StatusCode)
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
			Size        int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	v := &releaseView{Tag: raw.TagName, Notes: raw.Body, Source: "github"}
	for _, a := range raw.Assets {
		v.Assets = append(v.Assets, releaseAsset{Name: a.Name, URL: a.DownloadURL, Size: a.Size})
	}
	return v, nil
}

var updateHTTP = &http.Client{Timeout: updateTotalTimeout}

// manifestPlatforms 清单键与 GOOS-GOARCH 一一对应。
var manifestPlatforms = []string{
	"windows-amd64", "windows-386",
	"linux-amd64", "linux-arm64",
	"darwin-amd64", "darwin-arm64",
}

// parseManifest 解析官网 INI 式 update.txt 为归一后的 releaseView。
func parseManifest(text string) (*releaseView, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	version := strings.TrimSpace(fields["version"])
	if version == "" {
		return nil, fmt.Errorf("manifest missing version")
	}
	channel := strings.TrimSpace(fields["channel"])
	if channel == "" {
		channel = "stable"
	}
	view := &releaseView{Tag: version, Notes: fields["notes"], Channel: channel, Source: "website"}
	for _, key := range manifestPlatforms {
		u := strings.TrimSpace(fields[key])
		if u == "" {
			continue
		}
		name := "PeerLink-UI-" + key
		if strings.HasPrefix(key, "windows-") {
			name += ".exe"
		}
		view.Assets = append(view.Assets, releaseAsset{Name: name, URL: u})
	}
	return view, nil
}

// webProbe 拉取官网更新清单并解析。
func webProbe(ctx context.Context) (*releaseView, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, updateManifestURL, nil)
	req.Header.Set("User-Agent", httpUserAgent)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("manifest status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseManifest(string(body))
}

// pickLatest 在双源都成功时取版本号更新的那个；同版本保留先入列的（官网清单优先）。
// 不能只认官网清单：清单未及时更新时会盖住 GitHub 上更新的 Release。
func pickLatest(views []*releaseView) *releaseView {
	if len(views) == 0 {
		return nil
	}
	best := views[0]
	for _, v := range views[1:] {
		if versionNewer(v.Tag, best.Tag) {
			best = v
		}
	}
	return best
}

// probeViews 双源并发探测：官网清单与 GitHub Release 各自拉取，返回所有成功的视图；
// 仅一个成功就只返回那个；都失败返回最后一个错误。版本取舍交给 pickLatest。
func probeViews(ctx context.Context) ([]*releaseView, error) {
	ch := make(chan probeResult, 2)
	go func() { v, e := webProbe(ctx); ch <- probeResult{v, e} }()
	go func() { v, e := ghProbe(ctx); ch <- probeResult{v, e} }()

	var views []*releaseView
	var lastErr error
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil {
			lastErr = r.err
			continue
		}
		views = append(views, r.view)
	}
	return views, lastErr
}

// assetPlatformSuffix 当前平台/架构，与清单键一一对应。
func assetPlatformSuffix() string { return runtime.GOOS + "-" + runtime.GOARCH }

func assetExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// matchPlatformAsset 资产是否属于当前平台/架构（新旧两种前缀都认）。
func matchPlatformAsset(a releaseAsset) bool {
	suffix := assetPlatformSuffix()
	ext := assetExt()
	for _, prefix := range []string{"PeerLink-UI-", "VoxLink-UI-"} {
		want := prefix + suffix + ext
		if a.Name == want || strings.HasPrefix(a.Name, prefix+suffix) {
			return true
		}
	}
	return false
}

// pickAsset 选当前平台/架构对应的 UI 版资产。
// 新产物命名 PeerLink-UI-*；旧版/回退源仍可能是 VoxLink-UI-*，两种前缀都认。
func pickAsset(assets []releaseAsset) *releaseAsset {
	for i := range assets {
		if matchPlatformAsset(assets[i]) {
			return &assets[i]
		}
	}
	return nil
}

// pickFallbackAsset 在其余成功源里找同平台资产作为下载回退；URL 与主资产相同则跳过。
func pickFallbackAsset(views []*releaseView, primary *releaseAsset) *releaseAsset {
	for _, v := range views {
		for i := range v.Assets {
			a := v.Assets[i]
			if primary != nil && a.URL == primary.URL {
				continue
			}
			if matchPlatformAsset(a) {
				return &v.Assets[i]
			}
		}
	}
	return nil
}

// ---------- App 侧入口 ----------

// autoUpdateEnabled 自动更新检测开关（默认开）
func (a *App) autoUpdateEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings.AutoUpdate == nil {
		return true
	}
	return *a.settings.AutoUpdate
}

// CheckUpdate 手动/开机检查：返回 updateInfo 信封。
func (a *App) CheckUpdate() string {
	a.update.mu.Lock()
	if a.update.checking {
		a.update.mu.Unlock()
		return wrapOK(a.update.info)
	}
	a.update.checking = true
	a.update.mu.Unlock()

	defer func() {
		a.update.mu.Lock()
		a.update.checking = false
		a.update.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), updateTotalTimeout)
	views, err := probeViews(ctx)
	cancel()
	info := updateInfo{Current: appVersion, CheckedAt: time.Now().Unix()}
	// 只要有一个源成功就继续：另一源失败属可接受降级；两源都失败才报错。
	if len(views) == 0 {
		info.Error = err.Error()
		a.update.mu.Lock()
		a.update.info = info
		a.update.mu.Unlock()
		return wrapOK(info)
	}
	best := pickLatest(views)
	info.Version = best.Tag
	info.Notes = best.Notes
	info.Source = best.Source
	info.Channel = best.Channel
	info.Available = versionNewer(best.Tag, appVersion)
	if asset := pickAsset(best.Assets); asset != nil {
		info.AssetName = asset.Name
		info.DownloadURL = asset.URL
		info.AssetSize = asset.Size
		if fb := pickFallbackAsset(views, asset); fb != nil {
			info.FallbackURL = fb.URL
		}
	} else if info.Available {
		info.Error = "发布包缺少当前平台资产(" + runtime.GOOS + "-" + runtime.GOARCH + ")"
	}
	a.update.mu.Lock()
	a.update.info = info
	a.update.mu.Unlock()
	return wrapOK(info)
}

// GetUpdateState 前端轮询下载进度用。
func (a *App) GetUpdateState() string {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	return wrapOK(map[string]any{
		"info":        a.update.info,
		"downloading": a.update.downloading,
		"progress":    a.update.progress,
		"version":     appVersion,
	})
}

// StartUpdateDownload 下载并替换二进制，随后重启。
// 成功返回后会发起 restart，因此前端应当先落一个"正在更新"的状态再调用。
func (a *App) StartUpdateDownload() string {
	a.update.mu.Lock()
	if a.update.downloading {
		a.update.mu.Unlock()
		return wrapErr("BUSY", "已有更新正在下载")
	}
	info := a.update.info
	if !info.Available || info.DownloadURL == "" {
		a.update.mu.Unlock()
		return wrapErr("NO_UPDATE", "当前没有可用的更新")
	}
	a.update.downloading = true
	a.update.progress = 0
	a.update.mu.Unlock()

	go a.downloadAndApply(info)

	return wrapOK(map[string]any{"started": true, "version": info.Version})
}

func (a *App) setUpdateProgress(p int) {
	a.update.mu.Lock()
	a.update.progress = p
	a.update.mu.Unlock()
	a.emit("update:progress", map[string]any{"progress": p})
}

func (a *App) failUpdate(code string) {
	a.update.mu.Lock()
	a.update.downloading = false
	a.update.info.Error = code
	a.update.mu.Unlock()
	a.emit("update:failed", map[string]any{"error": code})
}

// cleanUpdateCache 清空更新缓存目录内的残留包（不删目录本身）。
// 更新包只应在待应用期间留存，上一轮失败/中断留下的损坏包必须清掉。
func cleanUpdateCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// downloadAndApply 下载 → 校验大小 → 同目录落 .new → 启动替换脚本 → 退出进程。
// 更新包先落在私有 updates/ 缓存目录，校验通过后复制为当前程序的 .new 副本。
func (a *App) downloadAndApply(info updateInfo) {
	updDir := privateUpdatesDir()
	if err := os.MkdirAll(updDir, 0o755); err != nil {
		a.failUpdate("TEMP_DIR: " + err.Error())
		return
	}
	// 清掉上一轮残留的更新包，避免多版本堆积或损坏包被误用。
	cleanUpdateCache(updDir)

	dst := filepath.Join(updDir, info.AssetName)
	urls := []string{info.DownloadURL}
	if info.FallbackURL != "" && info.FallbackURL != info.DownloadURL {
		urls = append(urls, info.FallbackURL)
	}
	if err := downloadWithFallback(urls, dst, info.AssetSize, func(p int) { a.setUpdateProgress(p) }); err != nil {
		_ = os.Remove(dst)
		a.failUpdate("DOWNLOAD: " + err.Error())
		return
	}

	cur, err := os.Executable()
	if err != nil {
		_ = os.Remove(dst)
		a.failUpdate("EXE_PATH: " + err.Error())
		return
	}
	newPath := cur + ".new"
	if err := copyFile(dst, newPath); err != nil {
		_ = os.Remove(dst)
		a.failUpdate("STAGE: " + err.Error())
		return
	}
	// 新二进制已落到 .new 副本，缓存目录里的原件可清理；替换脚本负责 .new → cur。
	_ = os.Remove(dst)

	a.setUpdateProgress(100)
	if err := scheduleReplaceAndRestart(cur, newPath); err != nil {
		a.failUpdate("SCHEDULE: " + err.Error())
		return
	}
	a.emit("update:ready", map[string]any{"version": info.Version})
	// 给前端 800ms 落状态后退出
	time.Sleep(800 * time.Millisecond)
	a.mu.Lock()
	a.update.downloading = false
	a.mu.Unlock()
	if a.quit != nil {
		a.quit()
	}
	os.Exit(0)
}

// httpStatusError 非 2xx 响应；4xx 不重试，5xx/429 与网络错误按可重试处理。
type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("status %d", e.status) }

func isRetryableDownload(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.status == 429 || se.status >= 500
	}
	return true
}

// downloadWithFallback 依次尝试主/回退 URL；每个 URL 内部对瞬时错误重试一次。
func downloadWithFallback(urls []string, dst string, expectedSize int64, onProgress func(int)) error {
	var lastErr error
	for _, u := range urls {
		if u == "" {
			continue
		}
		err := downloadFileWithRetry(u, dst, expectedSize, onProgress)
		if err == nil {
			return nil
		}
		lastErr = err
		os.Remove(dst)
	}
	return lastErr
}

func downloadFileWithRetry(url, dst string, expectedSize int64, onProgress func(int)) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = downloadFile(url, dst, expectedSize, onProgress)
		if err == nil {
			return nil
		}
		if !isRetryableDownload(err) {
			return err
		}
		os.Remove(dst)
	}
	return err
}

func downloadFile(url, dst string, expectedSize int64, onProgress func(int)) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", httpUserAgent)
	client := &http.Client{Timeout: updateDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &httpStatusError{status: resp.StatusCode}
	}
	total := resp.ContentLength
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 256<<10)
	var written int64
	last := 0
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if total > 0 {
				p := int(written * 100 / total)
				if p != last {
					last = p
					onProgress(p)
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return validateDownloadSize(written, total, expectedSize)
}

// validateDownloadSize 校验落盘字节数：空包拒绝；Content-Length/已知大小不符则拒绝。
func validateDownloadSize(written, total, expectedSize int64) error {
	if written == 0 {
		return fmt.Errorf("下载内容为空")
	}
	if total > 0 && written != total {
		return fmt.Errorf("下载不完整 %d/%d", written, total)
	}
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("大小校验不一致 %d/%d", written, expectedSize)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// scheduleReplaceAndRestart 生成替换脚本并立即启动：等主进程退出后替换再拉起新版本。
func scheduleReplaceAndRestart(cur, newPath string) error {
	if runtime.GOOS == "windows" {
		bat := filepath.Join(filepath.Dir(cur), "peerlink-update.cmd")
		content := fmt.Sprintf(`@echo off
chcp 65001 >nul
:wait
timeout /t 1 /nobreak >nul
tasklist /FI "PID eq %d" 2>nul | find "%d" >nul && goto wait
move /y "%s" "%s"
start "" "%s"
del "%%~f0"
`, os.Getpid(), os.Getpid(), cur, newPath, cur)
		if err := os.WriteFile(bat, []byte(content), 0o644); err != nil {
			return err
		}
		cmd := exec.Command("cmd", "/c", "start", "/min", bat)
		cmd.Stdin = nil
		return cmd.Start()
	}
	// 非 Windows：就地替换依赖权限，失败时提示手动重装
	sh := filepath.Join(filepath.Dir(cur), "peerlink-update.sh")
	content := fmt.Sprintf(`#!/bin/sh
while kill -0 %d 2>/dev/null; do sleep 1; done
mv -f "%s" "%s"
chmod +x "%s"
"%s" &
rm -f "$0"
`, os.Getpid(), newPath, cur, cur, cur)
	if err := os.WriteFile(sh, []byte(content), 0o755); err != nil {
		return err
	}
	return exec.Command("/bin/sh", sh).Start()
}

// shouldPromptUpdate 开机是否弹更新提示：开关开 且 上次是"稍后更新"或从未提示过。
func (a *App) shouldPromptUpdate() bool {
	if !a.autoUpdateEnabled() {
		return false
	}
	a.mu.Lock()
	v := a.settings.UpdatePromptAt
	a.mu.Unlock()
	return v == "" || v == "later"
}

// RememberUpdateChoice 记录玩家选择："later" 下次启动再提醒，"skip" 本版本不再提醒。
func (a *App) RememberUpdateChoice(choice, version string) string {
	value := ""
	switch choice {
	case "later":
		value = "later"
	case "skip":
		value = "skip:" + strings.TrimSpace(version)
	default:
		return wrapErr("INVALID_PARAMS", "未知的选项")
	}
	a.mu.Lock()
	a.settings.UpdatePromptAt = value
	a.mu.Unlock()
	if err := a.saveSettings(); err != nil {
		return wrapErr("IO", err.Error())
	}
	return wrapOK(map[string]any{"ok": true})
}

// AutoUpdateOnBoot 开机自动检查（由前端在启动后调用一次）：
// 开关开 且 上一轮玩家选了"稍后更新"，才真正请求；否则返回 available=false。
func (a *App) AutoUpdateOnBoot() string {
	if !a.shouldPromptUpdate() {
		return wrapOK(map[string]any{"prompt": false, "version": appVersion})
	}
	a.mu.Lock()
	skipFor := strings.TrimPrefix(a.settings.UpdatePromptAt, "skip:")
	a.mu.Unlock()
	a.CheckUpdate()
	info := a.update.info
	// 玩家"本版本不再提醒"：即使有更新也不弹
	if skipFor != "" && skipFor == info.Version {
		return wrapOK(map[string]any{"prompt": false, "version": appVersion, "info": info})
	}
	return wrapOK(map[string]any{"prompt": info.Available && info.DownloadURL != "", "version": appVersion, "info": info})
}

// 兼容：让 strconv 参与（避免未使用告警，后续版本号解析可扩展）
var _ = strconv.Itoa
