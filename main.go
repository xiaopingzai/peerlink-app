//go:build desktop

package main

// main.go — UI 版入口（go build -tags desktop,production）。
// 前端资源只在这里 embed：CLI 版不编译本文件，因此产物里不含 HTML/JS 与 webview。

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:       "VoxLink",
		Width:       1100,
		Height:      720,
		MinWidth:    860,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind:        []interface{}{app},
		// legacy 旧栈（Wails v2.6）没有 DragAndDrop 选项：模组目录只能用选择对话框，
		// 不支持把文件拖进窗口。主线 Win10+ 版本保留原生拖放。
		Windows: &windows.Options{
			Theme: windows.SystemDefault,
		},
	})
	if err != nil {
		log.Fatalf("wails run failed: %v", err)
	}
}
