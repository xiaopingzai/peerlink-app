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
		// 拖放必须走 Wails 原生通道：WebView2 里 dataTransfer 只有 File 对象、拿不到绝对路径，
		// 这是"拖进来没反应"的根因。DisableWebViewDrop 顺带堵掉"拖到别处当成打开文件"的空窗口。
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Windows: &windows.Options{
			Theme: windows.SystemDefault,
		},
	})
	if err != nil {
		log.Fatalf("wails run failed: %v", err)
	}
}
