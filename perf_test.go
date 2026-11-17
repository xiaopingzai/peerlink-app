package main

import (
	"strings"
	"testing"
)

func TestResolvePerfTier(t *testing.T) {
	const oneGiB = 1024 * 1024 * 1024
	cases := []struct {
		name     string
		saved    string
		totalMem uint64
		want     string
	}{
		{"auto 未知内存回落 balanced", perfTierAuto, 0, perfTierBalanced},
		{"auto 512MB 低内存落 light", perfTierAuto, 512 * 1024 * 1024, perfTierLight},
		{"auto 恰好 1GiB 不算低内存", perfTierAuto, oneGiB, perfTierBalanced},
		{"auto 4GB 落 balanced", perfTierAuto, 4 * oneGiB, perfTierBalanced},
		{"显式 light 不被大内存覆盖", perfTierLight, 4 * oneGiB, perfTierLight},
		{"显式 full 不被小内存降级", perfTierFull, 512 * 1024 * 1024, perfTierFull},
		{"野值按 auto 处理", "turbo", 0, perfTierBalanced},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvePerfTier(c.saved, c.totalMem); got != c.want {
				t.Errorf("resolvePerfTier(%q,%d)=%q want %q", c.saved, c.totalMem, got, c.want)
			}
		})
	}
}

func TestGOGCForTier(t *testing.T) {
	want := map[string]int{
		perfTierLight:    50,
		perfTierBalanced: 100,
		perfTierFull:     150,
		perfTierAuto:     100,
	}
	for tier, gc := range want {
		if got := gogcForTier(tier); got != gc {
			t.Errorf("gogcForTier(%q)=%d want %d", tier, got, gc)
		}
	}
}

func TestSaveSettingsRejectsBadPerfTier(t *testing.T) {
	a := NewApp()
	a.settingsPath = t.TempDir() + "/settings.json"
	out := a.SaveSettings(`{"perfTier":"turbo"}`)
	if !strings.Contains(out, "INVALID_PARAMS") {
		t.Fatalf("野值档位应当场报错：%s", out)
	}
	if a.perfTier() != perfTierAuto {
		t.Errorf("报错那次不应改写档位，got %q", a.perfTier())
	}
}

func TestPerfTierPersists(t *testing.T) {
	dir := t.TempDir()
	a := NewApp()
	a.settingsPath = dir + "/settings.json"
	if out := a.SaveSettings(`{"perfTier":"light"}`); !strings.Contains(out, `"perfTier":"light"`) {
		t.Fatalf("保存应回显 light：%s", out)
	}

	b := NewApp()
	b.settingsPath = dir + "/settings.json"
	b.loadSettings()
	if b.perfTier() != perfTierLight {
		t.Errorf("重载后档位 = %q, want light", b.perfTier())
	}
}

func TestPerfSnapshot(t *testing.T) {
	a := NewApp()
	a.settingsPath = t.TempDir() + "/settings.json"
	info := a.perfInfo()
	for _, key := range []string{"perfTier", "resolvedTier", "perfTiers", "totalMemory", "heapInUse", "goroutines"} {
		if _, ok := info[key]; !ok {
			t.Errorf("perfInfo 缺字段 %s", key)
		}
	}
	if tiers, ok := info["perfTiers"].([]string); !ok || len(tiers) != 4 {
		t.Errorf("perfTiers 应为四档，got %+v", info["perfTiers"])
	}
}

// Linux 环境下 /proc/meminfo 应能读出正的整机内存；其他平台返回 0（未知）。
func TestSystemTotalMemory(t *testing.T) {
	total := systemTotalMemory()
	if total < 0 {
		t.Errorf("总内存不能为负: %d", total)
	}
}
