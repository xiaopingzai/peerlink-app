package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnReadyRequiresStartedTransport(t *testing.T) {
	a, _ := newAutoRelayTestApp(t)
	e := a.engine
	tr, _, _ := livePair(t)
	failed := ""
	e.mu.Lock()
	e.turnTransport = tr
	e.turnPeerID = "host"
	e.turnInFlight = true
	e.relayFailForTest = func(reason string) { failed = reason }
	e.mu.Unlock()

	if !tr.IsConnected() {
		t.Fatal("测试前提：未 Start 的传输在旧判据下应被误判为已连接")
	}
	if e.turnReady() {
		t.Fatal("未 Start 的 v1 transport 不得视为 turn_ready")
	}
	e.turnReadyWatchdog(false, turnReadyMaxRound)
	e.mu.Lock()
	inFlight := e.turnInFlight
	transport := e.turnTransport
	e.mu.Unlock()
	if inFlight || transport != nil {
		t.Fatalf("看门狗未拆除未就绪 TURN: inFlight=%v transport=%v", inFlight, transport != nil)
	}
	if failed != keyAutoTurnFailed {
		t.Fatalf("超时未进入既有失败口: %q", failed)
	}
}

func TestAutoPlayerStartFailureLeavesRelayPhase(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	off := false
	a.settings.RelayEnabled = &off
	e.mu.Lock()
	e.autoRelayTurnAttempted = true
	e.autoRelayPlayerAttempted = false
	e.autoRelayArmedRound = e.retryRound
	e.mu.Unlock()
	sink.events = nil

	e.fireAutoPlayer()
	e.mu.Lock()
	mode := e.autoRelayMode
	e.mu.Unlock()
	if mode {
		t.Fatal("玩家中继启动错误被吞后会一直停留在自动中继模式")
	}
	foundP2P := false
	for _, ev := range connEvents(sink) {
		if ev["phase"] == phaseP2P && ev["status"] == statusTrying {
			foundP2P = true
		}
	}
	if !foundP2P {
		t.Fatalf("启动失败后未回到 P2P 恢复流程: %v", connEvents(sink))
	}
}

func TestGlobalRelayLegInitializesMap(t *testing.T) {
	e := newConnEngine(NewApp())
	e.startXRelayLeg(relayGlobalCandidate{ClientID: "candidate", MappedIP: "invalid", MappedPort: 1})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		n := len(e.relayLegs)
		e.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("非法候选的占位未清理")
}

func TestStandbyDeclinedKeepsActiveTurn(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	tr, _, _ := livePair(t)
	tr.started.Store(true)
	e.mu.Lock()
	e.turnTransport = tr
	e.mu.Unlock()
	sink.events = nil

	e.guestOnRelayDeclined()
	if got := connEvents(sink); len(got) != 0 {
		t.Fatalf("热备拒绝不得改写主 TURN 状态: %v", got)
	}
}

func TestRelayBridgeDownEmitsLinkLostAndRecovery(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	e.mu.Lock()
	e.autoRelayTurnAttempted = true
	e.autoRelayPlayerAttempted = true
	e.rDone = true
	e.mu.Unlock()
	sink.events = nil

	e.relayBridgeDown()
	events := connEvents(sink)
	if len(events) == 0 || events[0]["phase"] != phasePrelay || events[0]["status"] != statusTrying || events[0]["detail"] != "link_lost" {
		t.Fatalf("断桥必须先广播 link_lost: %v", events)
	}
	foundP2P := false
	for _, ev := range events {
		if ev["phase"] == phaseP2P && ev["status"] == statusTrying {
			foundP2P = true
		}
	}
	if !foundP2P {
		t.Fatalf("断桥后未进入恢复流程: %v", events)
	}
}

func TestAutoRelayCooldownReallyRetriesTurn(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	e.mu.Lock()
	e.punchUiStartMs = time.Now().UnixMilli() - 70_000
	e.punchRecvEver = true
	e.autoRelayArmedRound = e.retryRound
	e.autoRelayTurnAttempted = true
	e.autoRelayPlayerAttempted = true
	e.autoRelayTurnUnhealthy = true
	e.autoRelayTurnUnhealthyAtMs = time.Now().UnixMilli() - 61_000
	e.mu.Unlock()
	sink.events = nil

	e.autoRelayTickOnce(e.sess)
	e.mu.Lock()
	unhealthy := e.autoRelayTurnUnhealthy
	e.mu.Unlock()
	if unhealthy {
		t.Fatal("冷却到期后未清除 TURN 不健康状态")
	}
	foundTurn := false
	for _, ev := range connEvents(sink) {
		if ev["phase"] == phaseTurn && ev["status"] == statusTrying {
			foundTurn = true
		}
	}
	if !foundTurn {
		t.Fatalf("冷却到期后没有真正重试 TURN: %v", connEvents(sink))
	}
}

func TestRelayDeclineAfterTurnDeadEntersFailover(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	tr, _, _ := livePair(t)
	tr.started.Store(true)
	e.mu.Lock()
	e.turnTransport = tr
	e.rInFlight = true
	e.autoRelayMode = true
	e.autoRelayTurnAttempted = true
	e.autoRelayPlayerAttempted = true
	e.mu.Unlock()
	sink.events = nil

	e.guestOnRelayDeclined()
	e.mu.Lock()
	standbyIgnored := e.rInFlight
	e.mu.Unlock()
	if standbyIgnored {
		t.Fatal("TURN 死路后的主玩家中继被误判为热备，拒绝回包未进入换道")
	}
	foundP2P := false
	for _, ev := range connEvents(sink) {
		if ev["phase"] == phaseP2P && ev["status"] == statusTrying {
			foundP2P = true
		}
	}
	if !foundP2P {
		t.Fatalf("TURN 死路后拒绝回包未进入打洞恢复: %v", connEvents(sink))
	}
}

func TestLateRelayNotifyIgnored(t *testing.T) {
	a, sink := newAutoRelayTestApp(t)
	e := a.engine
	sink.events = nil
	e.guestOnRelayNotify("host", map[string]any{"connected": true})
	e.mu.Lock()
	hint := e.rConnectedHint
	e.mu.Unlock()
	if hint {
		t.Fatal("请求已结束后，迟到的 connected 不得污染下一次中继")
	}
	e.guestOnRelayNotify("host", map[string]any{"relayIp": "invalid", "relayPort": 1})
	e.guestOnRelayDeclined()
	time.Sleep(20 * time.Millisecond)
	if got := connEvents(sink); len(got) != 0 {
		t.Fatalf("迟到的中继地址不得重新启动流程: %v", got)
	}
}

func TestGlobalRelayFailureSignalsRequester(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("route") {
		case "/relay/candidates":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"candidates": []any{}}})
		case "/signal/send":
			sent = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&sent)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		}
	}))
	t.Cleanup(srv.Close)
	a := NewApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.settings.ServerURL = srv.URL
	a.AttachSink(nullSink{}, nil)
	a.Init()
	a.settings.ServerURL = srv.URL
	room := map[string]any{"code": "AB12CD", "clientType": "app"}
	s := newSession(a, "AB12CD", "host-token", true)
	a.setState("hosting", "AB12CD", "host-token", true, room, s)
	a.engine.bindSession(s, "host-client")
	t.Cleanup(func() { a.engine.unbindSession(s) })

	a.engine.dispatchGlobalRelay(s, "guest-1", "203.0.113.1", 40000)
	if sent["type"] != "relay_declined" || sent["to"] != "guest-1" {
		t.Fatalf("全局候选为空时未通知请求者: %v", sent)
	}
}
