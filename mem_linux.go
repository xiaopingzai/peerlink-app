//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

// systemTotalMemory 读取 /proc/meminfo 的 MemTotal（整机物理内存，字节）。
// 探测失败返回 0，调用方按"未知内存"走安全默认档位。
func systemTotalMemory() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
