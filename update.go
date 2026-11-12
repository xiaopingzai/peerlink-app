package main

import (
	"context"
	"encoding/json"
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

	// 探测窗口
	updateHeadTimeout   = 6 * time.Second
	updateTotalTimeout  = 8 * time.Second
	updateDownloadTimeout = 30 * time.Minute
)

// updateInfo 检查结果，透传给前端。
type updateInfo struct {
	Available   bool   `json:"available"`
	Version     string `json:"version"`
	Current     string `json:"current"`
	Source      string `json:"source"`       // github
	AssetName   string `json:"assetName"`
	DownloadURL string `json:"downloadUrl"`
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
	Tag      string
	Notes    string
	Assets   []releaseAsset
	Source   string
}

type updateState struct {
	mu       sync.Mutex
	info     updateInfo
	checking bool
	downloading bool
	progress int // 0-100
}

var sharedUpdate = &updateState{}

// ---------- 版本比较 ----------

// parseSemver "1.0.2" -> [1,0,2]（容忍前导 v 与非数字段）
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

// versionNewer 远端是否比本地新
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
	return false
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

// raceLatest 探测 GitHub 发布仓的最新 Release。
func raceLatest() (*releaseView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateTotalTimeout)
	defer cancel()

	ch := make(chan probeResult, 1)
	go func() { v, e := ghProbe(ctx); ch <- probeResult{v, e} }()

	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.view, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("发布源超时")
	}
}

// pickAsset 选当前平台/架构对应的 UI 版资产。
func pickAsset(assets []releaseAsset) *releaseAsset {
	want := "VoxLink-UI-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for i := range assets {
		if assets[i].Name == want {
			return &assets[i]
		}
	}
	// 放宽：同平台同架构的 UI 包（容忍不带 .exe）
	for i := range assets {
		n := assets[i].Name
		if strings.HasPrefix(n, "VoxLink-UI-"+runtime.GOOS+"-"+runtime.GOARCH) {
			return &assets[i]
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

	view, err := raceLatest()
	info := updateInfo{Current: appVersion, CheckedAt: time.Now().Unix()}
	if err != nil {
		info.Error = err.Error()
		a.update.mu.Lock()
		a.update.info = info
		a.update.mu.Unlock()
		return wrapOK(info)
	}
	info.Version = view.Tag
	info.Notes = view.Notes
	info.Source = view.Source
	info.Available = versionNewer(view.Tag, appVersion)
	if asset := pickAsset(view.Assets); asset != nil {
		info.AssetName = asset.Name
		info.DownloadURL = asset.URL
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

// downloadAndApply 下载 → 校验大小 → 同目录落 .new → 启动替换脚本 → 退出进程。
func (a *App) downloadAndApply(info updateInfo) {
	dir, err := os.MkdirTemp("", "voxlink-upd-*")
	if err != nil {
		a.failUpdate("TEMP_DIR: " + err.Error())
		return
	}
	defer os.RemoveAll(dir)

	dst := filepath.Join(dir, info.AssetName)
	if err := downloadFile(info.DownloadURL, dst, func(p int) { a.setUpdateProgress(p) }); err != nil {
		a.failUpdate("DOWNLOAD: " + err.Error())
		return
	}

	cur, err := os.Executable()
	if err != nil {
		a.failUpdate("EXE_PATH: " + err.Error())
		return
	}
	newPath := cur + ".new"
	if err := copyFile(dst, newPath); err != nil {
		a.failUpdate("STAGE: " + err.Error())
		return
	}

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

func downloadFile(url, dst string, onProgress func(int)) error {
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
		return fmt.Errorf("status %d", resp.StatusCode)
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
	if total > 0 && written != total {
		return fmt.Errorf("下载不完整 %d/%d", written, total)
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
		bat := filepath.Join(filepath.Dir(cur), "voxlink-update.cmd")
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
	sh := filepath.Join(filepath.Dir(cur), "voxlink-update.sh")
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
