package main

import (
	"runtime"
	"runtime/debug"
)

// 性能/内存档位：按整机内存自动选，或由玩家在设置里固定。
const (
	perfTierAuto     = "auto"     // 默认：探测整机内存，低于阈值走 light，否则 balanced
	perfTierLight    = "light"    // 低内存激进：压低堆上限、更早回收，内存优先（CPU 略增）
	perfTierBalanced = "balanced" // 均衡：GC 占比 100%（Go 默认）
	perfTierFull     = "full"     // 优先吞吐：放宽堆上限、减少 GC 次数（内存占用更高）
)

// lowMemoryThreshold 整机总内存低于该值（1 GiB）时，auto 档自动落到 light。
const lowMemoryThreshold = 1024 * 1024 * 1024

// perfTiers 设置页可选项（含 auto）。
var perfTiers = []string{perfTierAuto, perfTierLight, perfTierBalanced, perfTierFull}

func validPerfTier(t string) bool {
	for _, v := range perfTiers {
		if v == t {
			return true
		}
	}
	return false
}

// resolvePerfTier 把落盘档位解析为实际档位：显式档位原样保留；auto 按整机内存判定。
// totalMem 为 0 表示平台探测不到总内存，此时 auto 回落 balanced（安全默认，不激进）。
func resolvePerfTier(saved string, totalMem uint64) string {
	if saved != "" && saved != perfTierAuto && validPerfTier(saved) {
		return saved
	}
	if totalMem > 0 && totalMem < lowMemoryThreshold {
		return perfTierLight
	}
	return perfTierBalanced
}

// gogcForTier 各档位对应的 GC 目标堆增长率（debug.SetGCPercent）。
// light 压低阈值换取常驻内存更小；full 放宽阈值换取更少的 GC 次数/CPU 停顿。
func gogcForTier(tier string) int {
	switch tier {
	case perfTierLight:
		return 50
	case perfTierFull:
		return 150
	default:
		return 100
	}
}

// applyPerfTier 按落盘档位调整运行时 GC 参数，返回解析后的实际档位。
func applyPerfTier(saved string) string {
	resolved := resolvePerfTier(saved, systemTotalMemory())
	debug.SetGCPercent(gogcForTier(resolved))
	return resolved
}

// perfInfo 返回设置页展示的性能/内存快照：整机总内存、Go 堆在用量、goroutine 数、
// 落盘档位与解析后档位。totalMem 为 0 表示当前平台探测不到总内存。
func (a *App) perfInfo() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	saved := a.perfTier()
	total := systemTotalMemory()
	return map[string]any{
		"perfTier":     saved,
		"resolvedTier": resolvePerfTier(saved, total),
		"perfTiers":    perfTiers,
		"totalMemory":  total,
		"heapInUse":    ms.HeapInuse,
		"heapAlloc":    ms.HeapAlloc,
		"goroutines":   runtime.NumGoroutine(),
		"gcCount":      ms.NumGC,
	}
}
