package main

import (
	"os"
	"path/filepath"
)

// ============================================================
// App 私有目录归集：设置 / 日志 / 更新缓存 / 临时缓存统一放在
// 用户配置目录下的 PeerLink/ 文件夹，与主程序所在位置解耦。
// 旧品牌目录 VoxLinkApp/ 仅用于首次升级时兜底读取设置，不回写。
// ============================================================

// privateRootDir App 私有数据根目录。
// 优先 os.UserConfigDir()（Windows %APPDATA%、Linux ~/.config、
// macOS ~/Library/Application Support）；取不到时回退用户主目录。
func privateRootDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "PeerLink")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "PeerLink")
	}
	return "PeerLink"
}

// privateSettingsPath 持久化设置文件路径。
func privateSettingsPath() string {
	return filepath.Join(privateRootDir(), "settings.json")
}

// privateLogsDir 运行日志目录。
func privateLogsDir() string {
	return filepath.Join(privateRootDir(), "logs")
}

// privateUpdatesDir 更新包缓存目录：下载的新版本二进制先落这里，
// 校验通过后复制为当前程序的 .new 副本，再由替换脚本覆盖旧程序。
func privateUpdatesDir() string {
	return filepath.Join(privateRootDir(), "updates")
}

// legacySettingsPath 旧品牌目录下的设置文件，仅用于首次升级兜底读取。
// 解析不到可写配置目录时返回空串（调用方按无旧设置处理）。
func legacySettingsPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "VoxLinkApp", "settings.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "VoxLinkApp", "settings.json")
	}
	return ""
}
