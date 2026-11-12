package main

// relay_global.go — 玩家中继 v3：跨房全局大厅候选池（对齐模组 RelayOrchestrator
// dispatchGlobalRelay / startXRelayLeg / handleCrossRoomRelaySetup）。
//
// 为什么必须有这一层（用户 2026-10-31 定谳，说了无数次的口径）：
// 玩家中继的取材必须是**整个 VoxLink 大厅**（全服开了中继、非对称 NAT 的其他房玩家），
// 不是本房间那几个人。v1 曾是全局池但数据面坏（候选者复用自己连往本房 host 的传输转发
// → 跨房注入 + 两条 MC 会话串流），于是被停用收窄成 v2 房内池，欠账至今。
// 线上实证（54 份日志）：12 次 relay_request → 9 次"房内无候选" + 4 次"缺 relayMuxV1"
// → 0 次派发 0 次成功，数据面从未承载真实流量 = 饿死，不是病死。
//
// v3 与 v1 的本质区别（杜绝旧病根）：
//   - 候选者与请求方房主之间**新建一条专用传输腿**（双向打洞 → RUDP + mux，键
//     xrelay-<clientId>），绝不复用任何既有连接。这就是"虚拟加入"：纯传输层，
//     不加房间成员、不碰 MC 会话，所以不会再串流。
//   - 跨房信令走服务端既有的 relay_* 转发分支（SignalAppend：目标房写入 + host-<CODE>
//     归一投递，仅房主可跨房发起），客户端只需把 to 写成候选者 clientId / replyHostId。
//   - sym 房客与候选者之间仍是 v2 那条直连腿（端口预测打洞），语义不变。
//
// 时序（host 视角）：
//   relay_request(sym 房客) → 房内池空 → GET /relay/candidates?excludeRoom=本房
//   → 过滤 caps 含 relayMuxV1 且非对称 NAT → 取 3 个 → 对每个候选：
//      ① 起 xrelay 腿（host ↔ 候选者，mux 化）
//      ② 跨房发 relay_setup{crossRoom:true, hostIp, hostPort, replyHostId}
//      ③ 给 sym 房客发 relay_notify{relayIp, relayPort}（房内信令不变）
//   → 候选者回 relay_accept{forClientId, chan, crossRoom:true} → host 用 xrelay-<id>
//     找到那条腿，把 chan 子通道桥到本地 MC（与 v2 的 relay_accept 处理同款）。
//
// 8 秒无回执即按无候选收场（模组同款预算）；候选中途成桥会清掉这个计时器。

import (
	"fmt"
	"net"
	"strings"
	"time"

	"net/url"
)

const (
	// globalRelayBatchN 一批并行尝试的全局候选上限（模组 dispatchGlobalRelay 的 Math.min(3, size)）。
	globalRelayBatchN = 3
	// globalRelayBatchTimeout 全局批失败预算（模组 schedule(..., 8L, TimeUnit.SECONDS)）。
	globalRelayBatchTimeout = 8 * time.Second
	// xrelayHostLegTimeout host ↔ 候选者专用腿打洞超时（模组 orTimeout(8L, SECONDS)）。
	xrelayHostLegTimeout = 8 * time.Second
	// xrelayPeerLegTimeout 候选者 ↔ sym 房客直连腿打洞超时（模组 15L）。
	xrelayPeerLegTimeout = 15 * time.Second
)

// relayGlobalCandidate 全局候选行（/relay/candidates 响应的 candidates[] 元素，
// 字段对齐 server-go/relaymanager.go relayCandidatesScan 的返回形状）。
type relayGlobalCandidate struct {
	ClientID   string
	NatType    string
	MappedIP   string
	MappedPort int
	Caps       string
}

// fetchGlobalRelayCandidates 拉全服候选（排除本房），按 v3 门槛过滤并按"开了 mux 能力"排序。
// 服务端已滤掉 sym/unknown/无映射/未开中继/过期 peer，这里只补客户端侧的两条：
//   - caps 必须含 relayMuxV1（数据面走 mux 子通道，旧 peer 接了也是白接）；
//   - natType 仍不得含 sym/strict/unknown（服务端已滤，双保险防老服务端）。
func (e *connEngine) fetchGlobalRelayCandidates(excludeRoom string) []relayGlobalCandidate {
	e.mu.Lock()
	base := e.app.baseURL()
	e.mu.Unlock()

	var data struct {
		Candidates []struct {
			ClientID   string `json:"clientId"`
			NatType    string `json:"natType"`
			MappedIP   string `json:"mappedIp"`
			MappedPort int    `json:"mappedPort"`
			Caps       string `json:"caps"`
		} `json:"candidates"`
	}
	q := url.Values{}
	if excludeRoom != "" {
		q.Set("excludeRoom", strings.ToUpper(excludeRoom))
	}
	if err := e.app.api.get(base, "/relay/candidates", q, &data); err != nil {
		e.app.netLog("warn", "全局中继候选拉取失败: "+relaySafeErr(err))
		return nil
	}

	out := make([]relayGlobalCandidate, 0, len(data.Candidates))
	for _, c := range data.Candidates {
		if c.ClientID == "" || c.MappedIP == "" || c.MappedPort <= 0 {
			continue
		}
		// 数据面要求 mux 子通道：候选者必须声明 relayMuxV1
		if !strings.Contains(c.Caps, relayMuxCapability) {
			continue
		}
		// NAT 必须明确是锥型才可当候选。注意不能用 strings.Contains(nt,"strict")：
		// port_restricted_cone / restricted_cone 字面量里嵌着 "strict"，子串匹配会把
		// 最常见的锥型候选全部误杀（线上 9/12 次 "no candidates" 就是这个假阳性）。
		if natClassFromRoomType(strings.ToLower(strings.TrimSpace(c.NatType))) != natCone {
			continue
		}
		out = append(out, relayGlobalCandidate{
			ClientID: c.ClientID, NatType: c.NatType,
			MappedIP: c.MappedIP, MappedPort: c.MappedPort, Caps: c.Caps,
		})
		if len(out) >= globalRelayBatchN {
			break
		}
	}
	return out
}

// dispatchGlobalRelay host 侧 v3 派发：房内池空后转全服候选。
// targetID/targetIP/targetPort 是需中继的房客（sym 侧）。
func (e *connEngine) dispatchGlobalRelay(sess *session, targetID, targetIP string, targetPort int) {
	e.mu.Lock()
	code, token := e.code, e.token
	e.mu.Unlock()
	if sess == nil || code == "" || token == "" {
		return
	}

	cands := e.fetchGlobalRelayCandidates(code)
	if len(cands) == 0 {
		e.app.netLog("info", "全局大厅池无可用候选（relayMuxV1 + 非对称 NAT 过滤后为空）")
		e.relayClearFailures()
		_ = e.sendSignal("relay_declined", map[string]any{"reason": keyRelayNoCandidates}, targetID)
		return
	}

	// host 对外映射地址：跨房 relay_setup 要靠它让候选者反向打通
	hostMapped := e.relaySelfMapped()
	if hostMapped == nil || !hostMapped.valid() {
		e.app.netLog("warn", "全局中继需要本端对外映射地址，暂缺")
		_ = e.sendSignal("relay_declined", map[string]any{"reason": keyRelayNoCandidates}, targetID)
		return
	}

	replyHostID := e.relayPeerID()
	for i, c := range cands {
		// ① host ↔ 候选者专用 mux 腿（独立 punch，绝不复用既有连接）
		e.startXRelayLeg(c)

		// ② 跨房 relay_setup（服务端 SignalAppend 转到候选者所在房间，仅房主可发起）
		setup := map[string]any{
			"targetClientId": targetID,
			"targetIp":       targetIP,
			"targetPort":     targetPort,
			"hostIp":         hostMapped.IP,
			"hostPort":       hostMapped.Port,
			"replyHostId":    replyHostID,
			"crossRoom":      true,
		}
		if err := e.sendSignal("relay_setup", setup, c.ClientID); err != nil {
			e.app.netLog("warn", "跨房 relay_setup 发送失败: "+err.Error())
		}
		// ③ sym 房客照旧收房内 relay_notify（打候选者，v2 语义不变）
		notify := map[string]any{"relayIp": c.MappedIP, "relayPort": c.MappedPort}
		if err := e.sendSignal("relay_notify", notify, targetID); err != nil {
			e.app.netLog("warn", "relay_notify 发送失败: "+err.Error())
		}
		// 日志不落候选地址（应用端安全纪律：玩家可见日志禁暴露节点/对端公网地址）
		e.app.netLog("info", fmt.Sprintf("[Relay] 全局候选 #%d 已派发（crossRoom relayMuxV1）", i+1))
	}

	// 8s 失败预算：无 accept 即按无候选收场（accept 到达会 relayEndManualRelay 撤销本任务）
	e.mu.Lock()
	mySess := e.sess
	e.relayGlobalSess = sess
	e.relayGlobalUntil = time.Now().Add(globalRelayBatchTimeout).UnixMilli()
	e.mu.Unlock()
	if mySess != sess {
		return
	}
	time.AfterFunc(globalRelayBatchTimeout, func() {
		e.mu.Lock()
		still := e.sess == sess && e.relayGlobalUntil > 0 && time.Now().UnixMilli() >= e.relayGlobalUntil
		if still {
			e.relayGlobalUntil = 0
		}
		done := e.gP2PDone || e.rDone
		e.mu.Unlock()
		if !still || done {
			return
		}
		e.app.netLog("warn", "[Relay] 全局中继批超时（8s 无 accept）")
		_ = e.sendSignal("relay_declined", map[string]any{"reason": keyRelayNoCandidates}, targetID)
	})
}

// startXRelayLeg host → 全局候选者的专用 mux 传输腿。
// 成功后挂在 activeTransports 的 "xrelay-<clientId>" 键上（relay_accept 靠这个键找腿）。
//
// 与 v2 房内候选的关键差异：这条腿是本端**新建**的双向打洞，不复用这条房客连接，
// 因此跨房也不会把别人的 MC 会话字节混进来。
func (e *connEngine) startXRelayLeg(cand relayGlobalCandidate) {
	key := "xrelay-" + cand.ClientID
	e.mu.Lock()
	if _, exists := e.relayLegs[key]; exists {
		e.mu.Unlock()
		return
	}
	if e.relayLegs == nil {
		e.relayLegs = map[string]*rudpConn{}
	}
	e.relayLegs[key] = nil // 占位防重复起腿
	e.mu.Unlock()

	go func() {
		sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			e.mu.Lock()
			delete(e.relayLegs, key)
			e.mu.Unlock()
			return
		}
		target := net.ParseIP(cand.MappedIP)
		if target == nil {
			_ = sock.Close()
			e.mu.Lock()
			delete(e.relayLegs, key)
			e.mu.Unlock()
			return
		}
		win, actual, perr := punchRelayOnce(sock, &net.UDPAddr{IP: target, Port: cand.MappedPort}, xrelayHostLegTimeout)
		if perr != nil {
			_ = sock.Close()
			e.mu.Lock()
			delete(e.relayLegs, key)
			e.mu.Unlock()
			e.app.netLog("warn", "[Relay] 全局候选专用腿打洞失败（候选已排除）")
			e.relayMarkFailed(cand.ClientID)
			return
		}
		if win != sock {
			_ = sock.Close()
			sock = win
		}
		rc := newRudp(sock, actual)
		rc.enableMux() // 双方均 mux：线上字节=子通道帧（与 v2 同款）
		rc.Start()
		e.mu.Lock()
		if e.sess == nil {
			e.mu.Unlock()
			_ = rc.Close()
			return
		}
		e.relayLegs[key] = rc
		e.mu.Unlock()
		e.app.netLog("info", "[Relay] 全局候选专用 mux 腿已就绪")
	}()
}

// dropRelayLeg 关掉并移除一条 xrelay 腿（退房/失败收尾用）。
func (e *connEngine) dropRelayLeg(key string) {
	e.mu.Lock()
	rc := e.relayLegs[key]
	delete(e.relayLegs, key)
	e.mu.Unlock()
	if rc != nil {
		_ = rc.Close()
	}
}

// relayLeg 取一条 xrelay 腿（relay_accept 找腿用；nil=还没建好）。
func (e *connEngine) relayLeg(key string) *rudpConn {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.relayLegs[key]
}

// stopAllRelayLegs 退房/终局时清掉全部跨房专用腿。
func (e *connEngine) stopAllRelayLegs() {
	e.mu.Lock()
	legs := make([]*rudpConn, 0, len(e.relayLegs))
	for k, rc := range e.relayLegs {
		if rc != nil {
			legs = append(legs, rc)
		}
		delete(e.relayLegs, k)
	}
	e.mu.Unlock()
	for _, rc := range legs {
		_ = rc.Close()
	}
}

// guestCrossRoomRelaySetup 候选者侧 v3：收到跨房 relay_setup（crossRoom=true）。
//
// 两条腿 + 一条桥（模组 handleCrossRoomRelaySetup 逐段对齐）：
//
//	① 打通请求方房主（hostIp:hostPort）→ 专用 mux 腿 hostLeg
//	② 打通 sym 房客（targetIp:targetPort，端口预测同 v2）
//	③ RelayBridge：hostLeg 的 mux 子通道 ↔ sym 直连腿
//	④ 回 relay_accept{forClientId, chan, crossRoom:true} 给 replyHostId
//
// 与 v2（房内）的差别只有 ①：v2 复用本端到自家房主的 joiner 传输，v3 必须新建专用腿
// ——这正是 v1 跨房注入的病根所在，绝不能再走。
func (e *connEngine) guestCrossRoomRelaySetup(data map[string]any) {
	if !e.app.relayEnabled() {
		replyTo := sigStr(data, "replyHostId")
		if replyTo == "" {
			replyTo = "host"
		}
		e.app.netLog("info", "中继开关已关闭，婉拒跨房 relay_setup")
		_ = e.sendSignal("relay_declined", map[string]any{}, replyTo)
		return
	}
	replyTo := sigStr(data, "replyHostId")
	if replyTo == "" {
		replyTo = "host"
	}
	targetClientID := sigStr(data, "targetClientId")
	targetIP := sigStr(data, "targetIp")
	targetPort := sigInt(data, "targetPort")
	hostIP := sigStr(data, "hostIp")
	hostPort := sigInt(data, "hostPort")

	decline := func(reason string) {
		e.app.netLog("warn", "[Relay] 跨房中继婉拒: "+reason)
		_ = e.sendSignal("relay_declined", map[string]any{}, replyTo)
	}
	if !selfSupportsCap(relayMuxCapability) {
		decline("本端未实现 relayMuxV1")
		return
	}
	hostAddr := net.ParseIP(hostIP)
	targetAddr := net.ParseIP(targetIP)
	if hostAddr == nil || hostPort <= 0 || targetAddr == nil || targetPort <= 0 {
		decline("host/target 地址非法")
		return
	}

	go func() {
		// ① host 专用 mux 腿
		hs, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			decline("建 socket 失败")
			return
		}
		hWin, hActual, herr := punchRelayOnce(hs, &net.UDPAddr{IP: hostAddr, Port: hostPort}, xrelayHostLegTimeout)
		if herr != nil {
			_ = hs.Close()
			decline("打请求方房主失败")
			return
		}
		if hWin != hs {
			_ = hs.Close()
			hs = hWin
		}
		hostLeg := newRudp(hs, hActual)
		hostLeg.enableMux()
		hostLeg.Start()

		// ② sym 直连腿（与 v2 同款：端口预测打洞）
		ts, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			_ = hostLeg.Close()
			decline("建 socket 失败")
			return
		}
		tWin, tActual, terr := punchRelayOnce(ts, &net.UDPAddr{IP: targetAddr, Port: targetPort}, xrelayPeerLegTimeout)
		if terr != nil {
			_ = ts.Close()
			_ = hostLeg.Close()
			decline("打需中继房客失败")
			return
		}
		if tWin != ts {
			_ = ts.Close()
			ts = tWin
		}
		peerLeg := newRudp(ts, tActual)
		peerLeg.Start()

		// ③ 桥接：host 腿 chan 子通道 ↔ sym 直连腿
		muxChan := allocateRelayMuxChan()
		peerID := targetClientID
		if peerID == "" {
			peerID = "sym"
		}
		relayKey := fmt.Sprintf("xmuxrelay-%d-%s", muxChan, peerID)
		rb := getRelayBridge(e.app.netLog)
		rb.startRelay(relayKey, fmt.Sprintf("xhost-chan%d", muxChan), peerID,
			relayLegOf(rudpMuxRelayStream(hostLeg, muxChan)),
			relayLegOf(rudpBridgeStream(peerLeg)))

		e.mu.Lock()
		e.rAsNodeStop = rb.stopAllRelays
		if e.relayLegs == nil {
			e.relayLegs = map[string]*rudpConn{}
		}
		// 候选者侧两条腿都随会话存：hostLeg 供 mux 子通道桥引用（relay_session 已持流），
		// 登记键用 peer 维度，退房时由 stopAllRelayLegs 一并关掉。
		e.relayLegs[fmt.Sprintf("xmux-%s", peerID)] = hostLeg
		e.mu.Unlock()

		// ④ 回执（跨房：to=replyHostId=host-<对方房号>，服务端归一投递到该房房主）
		reply := map[string]any{
			"forClientId": peerID,
			"chan":        muxChan,
			"crossRoom":   true,
		}
		if err := e.sendSignal("relay_accept", reply, replyTo); err != nil {
			e.app.netLog("warn", "跨房 relay_accept 发送失败: "+err.Error())
		}
		e.app.netLog("info", "[Relay] 跨房中继桥已建立（chan 子通道）")
	}()
}
