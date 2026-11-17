//go:build !linux

package main

// systemTotalMemory 在非 Linux 平台暂返回 0（探测不到整机总内存）。
// 调用方据此把 auto 档回落 balanced，不会误判成低内存而激进回收。
// TODO(win/darwin)：Windows 用 GlobalMemoryStatusEx、macOS 用 sysctl hw.memsize 补全。
func systemTotalMemory() uint64 { return 0 }
