package main

// sync_mod_align_20261102_test.go — 2026-11-02 "mod 端对齐应用端"这一轮的回归用例。
//
// 对齐面（每项对应 mod 端 core 的一处改动）：
//   1. TURN 节点选优 = rtt × (1+负载比) × 粘性 0.8，且不可达恒沉底
//   2. 探测限界 PROBE_MAX_NODES=8 + 粘性保送
//   3. relay/list 带 full=1（全量下发），并解析 sessions/maxSessions
//   4. v3 全局候选过滤：caps 必须含 relayMuxV1、NAT 不得 sym/strict/unknown、一批 ≤3
//   5. 自动中继 TURN 判死 60s 冷却（到期可再试 TURN）
//
// 这些都是"改了没人叫"就悄悄退化的地方（选优退化成纯 RTT、冷却退化成会话级死刑），
// 所以逐条钉死。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- 1 & 2：TURN 选优与探测限界 ----

func TestTurnProbeScoreLoadAndSticky(t *testing.T) {
	turnStickyNodeMu.Lock()
	turnStickyNodeID = ""
	turnStickyNodeMu.Unlock()

	base := turnNodeInfo{ID: "a", MaxSessions: 100, Sessions: 0}
	loaded := turnNodeInfo{ID: "b", MaxSessions: 100, Sessions: 50}

	// 同 RTT：负载低的评分更低（更优）
	low := turnProbeScore(turnProbeResult{node: base, rttMs: 100})
	high := turnProbeScore(turnProbeResult{node: loaded, rttMs: 100})
	if !(low < high) {
		t.Fatalf("负载项失效: 空载=%v 半载=%v", low, high)
	}

	// 粘性折扣：同一节点被记住后评分打 8 折
	turnRememberGoodNode("a")
	sticky := turnProbeScore(turnProbeResult{node: base, rttMs: 100})
	if sticky >= low {
		t.Fatalf("粘性折扣未生效: 原=%v 粘性=%v", low, sticky)
	}

	// 不可达恒沉底（即便负载为 0 且是粘性节点）
	turnRememberGoodNode("dead")
	dead := turnProbeScore(turnProbeResult{node: turnNodeInfo{ID: "dead", MaxSessions: 100}, rttMs: -1})
	if dead <= high {
		t.Fatalf("不可达节点未沉底: dead=%v loaded=%v", dead, high)
	}

	turnStickyNodeMu.Lock()
	turnStickyNodeID = ""
	turnStickyNodeMu.Unlock()
}

func TestTurnProbeNodesLimitsAndKeepsSticky(t *testing.T) {
	// 12 个节点，只有第 11 个是粘性且高负载 → 必须仍被探到（保送），其余只取负载最低 8 个
	nodes := make([]turnNodeInfo, 0, 12)
	for i := 0; i < 12; i++ {
		nodes = append(nodes, turnNodeInfo{
			ID: "n" + string(rune('a'+i)), Host: "127.0.0.1", Port: 1,
			MaxSessions: 100, Sessions: i, // 越靠后负载越高
		})
	}
	stickyID := nodes[11].ID
	turnStickyNodeMu.Lock()
	turnStickyNodeID = stickyID
	turnStickyNodeMu.Unlock()
	defer func() {
		turnStickyNodeMu.Lock()
		turnStickyNodeID = ""
		turnStickyNodeMu.Unlock()
	}()

	// 不真探测（端口 1 必然超时，结果是 rtt=-1），只看"探了多少个"
	probed := turnProbeNodes(nodes)
	if len(probed) != turnProbeMaxNodes+1 {
		t.Fatalf("探测数量应为 %d（前 8 + 粘性保送），实际 %d", turnProbeMaxNodes+1, len(probed))
	}
	found := false
	for _, p := range probed {
		if p.node.ID == stickyID {
			found = true
		}
	}
	if !found {
		t.Fatal("粘性节点未被保送进探测名单")
	}
}

// ---- 3：relay/list 带 full=1 且解析 sessions/maxSessions ----

func TestTurnFetchNodeListSendsFullAndParsesLoad(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"nodes": []map[string]any{
					{"id": "turn01", "host": "1.2.3.4", "port": 37000, "stdTurnPort": 3478, "sessions": 7, "maxSessions": 40},
				},
			},
		})
	}))
	defer srv.Close()

	// 清缓存，避免命中上一用例
	turnNodeCacheMu.Lock()
	turnNodeCache = nil
	turnNodeCacheBase = ""
	turnNodeCacheMu.Unlock()

	a := NewApp()
	nodes, err := turnFetchNodeListBase(a, srv.URL)
	if err != nil {
		t.Fatalf("拉列表失败: %v", err)
	}
	if !strings.Contains(gotQuery, "full=1") {
		t.Fatalf("relay/list 必须带 full=1（全量下发由客户端选优），实际 query=%q", gotQuery)
	}
	if len(nodes) != 1 || nodes[0].Sessions != 7 || nodes[0].MaxSessions != 40 {
		t.Fatalf("负载字段未解析: %+v", nodes)
	}
}

// ---- 4：v3 全局候选过滤 ----

func TestFetchGlobalRelayCandidatesFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"candidates": []map[string]any{
					// 合格：cone + relayMuxV1
					{"clientId": "ok-1", "natType": "port_restricted_cone", "mappedIp": "1.1.1.1", "mappedPort": 1001, "caps": "relay,relayMuxV1"},
					// 无 relayMuxV1 → 丢
					{"clientId": "no-mux", "natType": "port_restricted_cone", "mappedIp": "1.1.1.2", "mappedPort": 1002, "caps": "relay"},
					// 对称 NAT → 丢（双保险，服务端已滤）
					{"clientId": "sym", "natType": "symmetric", "mappedIp": "1.1.1.3", "mappedPort": 1003, "caps": "relay,relayMuxV1"},
					// 缺映射 → 丢
					{"clientId": "no-addr", "natType": "port_restricted_cone", "mappedIp": "", "mappedPort": 0, "caps": "relay,relayMuxV1"},
					// 合格 2/3/4
					{"clientId": "ok-2", "natType": "port_restricted_cone", "mappedIp": "1.1.1.4", "mappedPort": 1004, "caps": "relayMuxV1"},
					{"clientId": "ok-3", "natType": "full_cone", "mappedIp": "1.1.1.5", "mappedPort": 1005, "caps": "relay,relayMuxV1"},
					{"clientId": "ok-4", "natType": "port_restricted_cone", "mappedIp": "1.1.1.6", "mappedPort": 1006, "caps": "relay,relayMuxV1"},
				},
			},
		})
	}))
	defer srv.Close()

	a := NewApp()
	a.settings.ServerURL = srv.URL
	e := a.engine
	e.app = a

	got := e.fetchGlobalRelayCandidates("AB12CD")
	if len(got) != globalRelayBatchN {
		t.Fatalf("应截到 %d 个合格候选，实际 %d: %+v", globalRelayBatchN, len(got), got)
	}
	for _, c := range got {
		if c.ClientID == "no-mux" || c.ClientID == "sym" || c.ClientID == "no-addr" {
			t.Fatalf("不合格候选混入: %+v", c)
		}
	}
}

// ---- 5：TURN 判死 60s 冷却 ----

func TestTurnUnhealthyCooldown(t *testing.T) {
	a := NewApp()
	e := a.engine

	e.mu.Lock()
	e.autoRelayTurnUnhealthy = true
	e.autoRelayTurnUnhealthyAtMs = time.Now().UnixMilli()
	active := e.turnUnhealthyActiveLocked()
	e.mu.Unlock()
	if !active {
		t.Fatal("刚判死时应当生效（冷却窗内）")
	}

	// 冷却窗过期 → 允许再试 TURN（不再会话级死刑）
	e.mu.Lock()
	e.autoRelayTurnUnhealthyAtMs = time.Now().Add(-turnUnhealthyCooldown - time.Second).UnixMilli()
	expired := e.turnUnhealthyActiveLocked()
	e.mu.Unlock()
	if expired {
		t.Fatal("冷却窗过期后 TURN 应视为可再试，实际仍判死")
	}
}

// ---- 6：NAT 分级里的 "strict" 子串陷阱（本轮 P0）----

// TestNatClassFromRoomTypeStrictSubstringTrap port_restricted_cone / restricted_cone
// 字面量里嵌着 "strict"（po-rt_re-strict-ed_cone）。任何 Contains("strict") 的写法
// 都会把最常见的锥型判成对称 NAT —— 线上 12 次 relay_request 里 9 次
// "No available Cone relay node" 正是这个假阳性造成的饿死。
func TestNatClassFromRoomTypeStrictSubstringTrap(t *testing.T) {
	cases := []struct {
		in   string
		want natClass
	}{
		{"full_cone", natCone},
		{"restricted_cone", natCone},
		{"port_restricted_cone", natCone},
		{"cone", natCone},
		{"open", natCone},     // 早期 API 遗留值：锥型
		{"moderate", natCone}, // 早期 API 遗留值：受限锥
		{"symmetric", natHardSym},
		{"symmetric_easy_inc", natEasySym},
		{"symmetric_easy_dec", natEasySym},
		{"strict", natHardSym}, // 早期 API 遗留值：严格 NAT
		{"unknown", natUnknown},
		{"", natUnknown},
	}
	for _, c := range cases {
		if got := natClassFromRoomType(c.in); got != c.want {
			t.Errorf("natClassFromRoomType(%q)=%v 期望 %v（strict 子串陷阱）", c.in, got, c.want)
		}
	}
}
