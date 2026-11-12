/* ============================================================
 * VoxLink Desktop — create.js
 * 创建房间表单 → CreateRoom → 进入房间页
 * 表单：房间名 / 密码 / 分类（含自定义）/ 启动器 + 版本 / 本地 MC 端口（MC 进程检测）
 * 应用端不管理人数，人数交给 MC 自己决定。
 * ============================================================ */
"use strict";

(function (VL) {
  var CUSTOM = "__custom__"; // 分类下拉中"自定义"选项的值
  var els = {};
  var detectedPorts = [];   // [{port, pid}]  本机检测到的 MC 端口

  function cacheEls() {
    els.name = VL.$("#create-name");
    els.pw = VL.$("#create-pw");
    els.loader = VL.$("#create-loader");
    els.version = VL.$("#create-version");
    els.category = VL.$("#create-category");
    els.categoryCustomField = VL.$("#create-category-custom-field");
    els.categoryCustom = VL.$("#create-category-custom");
    els.port = VL.$("#create-host-port");
    els.portSelect = VL.$("#create-port-select");
    els.portDetect = VL.$("#create-port-detect");
    els.portHint = VL.$("#create-port-hint");
    els.portError = VL.$("#create-port-error");
    els.portRow = VL.$("#create-port-row");
    els.visible = VL.$("#create-visible");
    els.msPublish = VL.$("#create-ms-publish");
    els.msInstance = VL.$("#create-ms-instance");
    els.msManage = VL.$("#create-ms-manage");
    els.warn = VL.$("#create-pw-warn");
    els.error = VL.$("#create-error");
    els.btn = VL.$("#create-btn");
  }

  function clearPortError() {
    if (!els.portError) return;
    els.portError.textContent = "";
    els.portError.classList.add("hidden");
  }

  function showPortError(msg) {
    if (!els.portError) return;
    els.portError.textContent = msg;
    els.portError.classList.remove("hidden");
  }

  /* ---------------- 模组同步：MC 实例 + 两个开关 ----------------
   * 实例（mods 目录 + 加载器 + MC 版本）原来埋在「设置 → MC 实例」里：玩家往往
   * 同时装了好几个版本的实例，开房时才想起来要选哪个，所以整套挪到建房页，
   * 并且可以就地新建/删除，下次开房在下拉里直接挑。
   * 存的是同一份全局设置（SaveModSyncSettings 整体覆盖），因此每次写回都必须
   * 带上完整的 instances / activeInstance / 两个开关，否则会把别的字段抹掉。 */
  var msView = null;

  function msInstances() {
    return (msView && msView.instances) || [];
  }

  function renderInstances() {
    if (!els.msInstance) return;
    var list = msInstances();
    var active = (msView && msView.activeInstance) || "";
    var html = '<option value="">' + VL.esc(VL.t("voxlink.app.create.ms_none", "不同步模组")) + "</option>";
    list.forEach(function (it) {
      html += '<option value="' + VL.esc(it.name) + '">' + VL.esc(it.name) +
        (it.loader || it.gameVersion ? "（" + VL.esc([it.loader, it.gameVersion].filter(Boolean).join(" · ")) + "）" : "") +
        "</option>";
    });
    els.msInstance.innerHTML = html;
    els.msInstance.value = list.some(function (it) { return it.name === active; }) ? active : "";
  }

  function loadModSync() {
    if (!els.msInstance) return;
    VL.api("GetModSyncSettings").then(function (v) {
      msView = v || null;
      renderInstances();
      els.msPublish.checked = !(msView && msView.hostModSyncPublish === false);
      autofillFromEnv();
    }, function () { /* 后端不可用时保持默认，不打扰玩家 */ });
  }

  // 用后端推断出的 (加载器, MC 版本) 自动填进表单。
  //
  // 这两个字段以前必须玩家手填：后端探测默认实例时 loader 硬编码 fabric、
  // gameVersion 留空。可它们本来就写在磁盘上（versions/ 目录名 + mods 里的 jar 名），
  // 后端已改成自动推断（modsync_env.go: modSyncInferFromModsDir）。
  // 这里只负责把推断结果铺到表单——只填空着的框，玩家填过的绝不覆盖。
  function autofillFromEnv() {
    if (!msView) return;
    var loader = String(msView.loader || "").toLowerCase();
    if (loader && els.loader && !els.loader.dataset.userEdited) {
      if (els.loader.value !== loader) els.loader.value = loader;
    }
    var ver = String(msView.mcVersion || "").trim();
    if (ver && els.version && !els.version.value.trim()) {
      els.version.value = ver;
    }
  }

  // SaveModSyncSettings 是整体回写：少带任何一个字段就等于把它删掉。
  // 所以每次写之前都重新读一次现状，而不是用页面里那份可能已经过期的 msView ——
  // 「加入前检查必装 Mod」现在归设置页写，建房页若拿旧快照回写就会把它悄悄改回去。
  function saveModSync(patch) {
    return VL.api("GetModSyncSettings").then(function (cur) {
      if (cur) msView = cur;
      var base = msView || {};
      var payload = {
        instances: msInstances(),
        activeInstance: base.activeInstance || "",
        joinRequiredModsCheck: !(base.joinRequiredModsCheck === false),
        hostModSyncPublish: !(base.hostModSyncPublish === false)
      };
      Object.keys(patch || {}).forEach(function (k) { payload[k] = patch[k]; });
      return VL.api("SaveModSyncSettings", JSON.stringify(payload));
    }).then(function (v) {
      msView = v || msView;
      renderInstances();
      return v;
    }, function (e) {
      if (e && e.code !== "NO_BACKEND") VL.toast(VL.errText(e), "error");
    });
  }

  function bindModSync() {
    if (!els.msInstance) return;
    els.msInstance.addEventListener("change", function () {
      saveModSync({ activeInstance: els.msInstance.value });
      applyInstanceToForm(els.msInstance.value);
    });
    els.msPublish.addEventListener("change", function () {
      saveModSync({ hostModSyncPublish: !!els.msPublish.checked });
    });
    // 实例的新建/编辑/删除统一收口到设置页（准则：避免同一件事两个入口）。
    // 建房时只需要"选哪个"，改配置再跳过去，回来下拉自然是新清单。
    if (els.msManage) els.msManage.addEventListener("click", function () { VL.showPage("settings"); });
  }

  // 选中某个实例后，把它的加载器与 MC 版本带进表单——玩家选了实例却还要再填一遍，纯属重复劳动。
  // 选「不同步模组」时不动表单，玩家手填的值仍然有效。
  function applyInstanceToForm(name) {
    if (!name) return;
    var it = msInstances().filter(function (x) { return x.name === name; })[0];
    if (!it) return;
    if (els.loader && it.loader) {
      els.loader.value = String(it.loader).toLowerCase();
      // 来自实例的值算"玩家已定"：后续自动推断不该再改它
      els.loader.dataset.userEdited = "1";
    }
    if (els.version && it.gameVersion) {
      els.version.value = String(it.gameVersion);
    }
  }

  // 有密码 → 自动大厅隐藏并提示
  function syncVisible() {
    var hasPw = !!els.pw.value;
    els.visible.checked = hasPw ? false : els.visible.checked;
    els.visible.disabled = hasPw;
    els.warn.classList.toggle("hidden", !hasPw);
  }

  // 分类选了"自定义" → 展示文本输入框
  function syncCategoryCustom() {
    var isCustom = els.category.value === CUSTOM;
    els.categoryCustomField.classList.toggle("hidden", !isCustom);
    if (isCustom) els.categoryCustom.focus();
  }

  // 提交时真正发送的 category 字符串
  function resolveCategory() {
    if (els.category.value === CUSTOM) {
      return els.categoryCustom.value.trim().slice(0, 24);
    }
    return els.category.value || "";
  }

  function showError(msg) {
    els.error.textContent = msg;
    els.error.classList.remove("hidden");
  }

  /* ---------------- 端口提示与选择 ---------------- */
  function setPortHint(level, text) {
    if (!els.portHint) return;
    els.portHint.textContent = text;
    els.portHint.classList.remove("form-hint-ok", "form-hint-neutral");
    if (level === "ok") els.portHint.classList.add("form-hint-ok");
    else if (level === "neutral") els.portHint.classList.add("form-hint-neutral");
  }

  // 0 个：中性提示；≥1 个：绿字提示，多个时旁挂一个只列端口的下拉
  function renderPortState() {
    if (!els.portSelect) return;
    // 只要检测到了，就必须把结果**填进输入框**：早先只有"检测到多个"那条分支才回填，
    // 单个端口时框里还停在 25565、提示却写着"已检测到 65239，请确认"——没有可确认的东西，
    // 玩家就这么建房，房间就开在一个没人监听的端口上，对端隧道打通了也连不进去。
    var cur = parseInt(els.port.value, 10);
    var has = detectedPorts.some(function (p) { return p.port === cur; });
    if (detectedPorts.length && !has) {
      els.port.value = detectedPorts[0].port;
      cur = detectedPorts[0].port;
    }
    if (detectedPorts.length > 1) {
      var opts = "";
      detectedPorts.forEach(function (p) {
        opts += '<option value="' + VL.esc(p.port) + '">' + VL.esc(p.port) + "</option>";
      });
      els.portSelect.innerHTML = opts;
      els.portSelect.value = String(els.port.value);
      els.portSelect.classList.remove("hidden");
    } else {
      els.portSelect.classList.add("hidden");
      els.portSelect.innerHTML = "";
    }

    renderPortHint();
  }

  // 提示文字单独成一个函数：换语言时只需要重说这句话，
  // 不能顺带重跑 renderPortState —— 它会把检测到的端口回填进输入框，
  // 玩家手改过的端口会被一次语言切换悄悄换掉。
  function portHintText() {
    if (!detectedPorts.length) {
      return VL.t("voxlink.app.create.port_none", "未检测到正在运行的 MC，请手动输入端口");
    }
    if (detectedPorts.length === 1) {
      return VL.t("voxlink.app.create.port_one", "已填入检测到的 MC 端口 %s，不对请手动改", detectedPorts[0].port);
    }
    return VL.t("voxlink.app.create.port_many", "检测到 %s 个 MC 端口，已选用第一个，可在旁边下拉里换", detectedPorts.length);
  }

  function renderPortHint() {
    setPortHint(detectedPorts.length ? "ok" : "neutral", portHintText());
  }

  /* ---------------- 端口自动检测 ---------------- */
  async function detectPorts() {
    els.portDetect.disabled = true;
    var label = els.portDetect.textContent;
    els.portDetect.textContent = VL.t("voxlink.app.create.detecting", "检测中…");
    if (els.portHint) setPortHint("neutral", VL.t("voxlink.app.create.detecting_hint", "正在检测本地 MC 端口…"));
    try {
      var data = await VL.api("DetectMcPorts");
      var ports = (data && data.ports) || [];
      detectedPorts = ports.filter(function (p) {
        return p && (p.port !== undefined && p.port !== null) && /^\d+$/.test(String(p.port));
      });
    } catch (e) {
      detectedPorts = [];
      if (e.code !== "NO_BACKEND") {
        setPortHint("neutral", VL.t("voxlink.app.create.detect_failed", "检测失败：%s", VL.errText(e)));
      } else {
        setPortHint("neutral", VL.t("voxlink.app.create.port_none", "未检测到正在运行的 MC，请手动输入端口"));
      }
    }
    renderPortState();
    els.portDetect.disabled = false;
    els.portDetect.textContent = label;
  }

  // 进入创建页时被 app.js 调用一次：自动检测 + 若首个端口自动填进端口框
  function autoDetectOnEnter() {
    // 进入页时主动探测一次
    detectPorts();
  }

  /* ---------------- 提交 ---------------- */
  // hostPort 必填：1024-65535 的整数
  function resolveHostPort() {
    var raw = String(els.port.value || "").trim();
    if (!/^\d+$/.test(raw)) return NaN;
    var port = parseInt(raw, 10);
    if (port < 1024 || port > 65535) return NaN;
    return port;
  }

// 启动器：fabric / forge / neoforge / unknown（"其他/未知"，应用端可为任意游戏开房）
function resolveLoader() {
    var v = (els.loader.value || "").toLowerCase();
    if (v === "forge" || v === "neoforge" || v === "fabric" || v === "unknown") return v;
    // 空 = 玩家没指定；不再默认成 fabric（会让"其他游戏"的房被错标成 MC 房）
    return "";
  }

  // 游戏版本：非空、≤16 字符
  function resolveGameVersion() {
    var v = String(els.version.value || "").trim();
    if (!v) return "";
    return v.slice(0, 16);
  }

  async function create() {
    els.error.classList.add("hidden");
    clearPortError();

    var category = resolveCategory();
    if (els.category.value === CUSTOM && !category) {
      showError(VL.t("voxlink.app.create.need_custom_category", "选了自定义分类，就填一个分类名称吧"));
      els.categoryCustom.focus();
      return;
    }

    var hostPort = resolveHostPort();
    if (!isFinite(hostPort)) {
      showError(VL.t("voxlink.app.create.need_port", "请填写本地 MC 端口（1024 - 65535 的整数），可点右侧“重新检测”"));
      els.port.focus();
      return;
    }

    // 游戏版本非必填：不填的房间只在网页端/应用端可见（游戏内大厅按版本筛不到它）
    var gameVersion = resolveGameVersion();

    var req = {
      name: els.name.value.trim() || "VoxLink Room",
      password: els.pw.value,
      category: category,
      visible: els.visible.checked,
      hostPort: hostPort,
      loader: resolveLoader(),
      gameVersion: gameVersion
    };

    setBusy(true);
    try {
      var data = await VL.api("CreateRoom", JSON.stringify(req));
      VL.toast(VL.t("voxlink.create_room.success", "房间创建成功！ :D"), "success");
      if (VL.onHosted) VL.onHosted(data);
    } catch (e) {
      // PORT_NOT_LISTENING：服务端拒绝建房的原因是本地端口没人监听，
      // 把 message 红字直接显示在端口框下方，其他错误走顶部 form-error。
      if (e.code === "PORT_NOT_LISTENING") {
        showPortError(e.message || VL.t("voxlink.app.create.port_not_listening", "端口没有服务在监听"));
        els.port.focus();
      } else if (e.code !== "NO_BACKEND") {
        showError(VL.errText(e));
      }
    }
    setBusy(false);
  }

  function setBusy(b) {
    els.btn.disabled = b;
    var label = els.btn.querySelector("span:last-child");
    if (label) {
      label.textContent = b
        ? VL.t("voxlink.create_flow.phase_registering", "正在连接信令服务器...")
        : VL.t("voxlink.create_room", "创建房间");
    }
  }

  VL.initCreate = function () {
    cacheEls();
    els.pw.addEventListener("input", syncVisible);
    els.category.addEventListener("change", syncCategoryCustom);
    els.portDetect.addEventListener("click", detectPorts);

    // 初始化即触发一次自动检测，避免用户看到空白后再点按钮
    detectPorts();

    // 用户自己改端口时：如有多个检测结果，旁挂小下拉跟着同步
    els.port.addEventListener("input", function () {
      els.error.classList.add("hidden");
      if (els.portSelect && !els.portSelect.classList.contains("hidden")) {
        var cur = parseInt(els.port.value, 10);
        var has = detectedPorts.some(function (p) { return p.port === cur; });
        if (has) els.portSelect.value = els.port.value;
      }
    });
    if (els.portSelect) {
      els.portSelect.addEventListener("change", function () {
        if (els.portSelect.value) {
          els.port.value = els.portSelect.value;
          els.error.classList.add("hidden");
        }
      });
    }

    els.categoryCustom.addEventListener("input", function () { els.error.classList.add("hidden"); });
    els.version.addEventListener("input", function () { els.error.classList.add("hidden"); });
    // 玩家一旦自己动过加载器，就不再让自动推断覆盖它（下回进页面清空标记重新推断）
    if (els.loader) {
      els.loader.addEventListener("change", function () { els.loader.dataset.userEdited = "1"; });
    }
    els.btn.addEventListener("click", create);
    if (els.port) {
      els.port.addEventListener("input", function () { clearPortError(); });
    }
    if (els.portDetect) {
      els.portDetect.addEventListener("click", function () { clearPortError(); });
    }
    els.name.addEventListener("keydown", function (ev) { if (ev.key === "Enter") create(); });
    els.port.addEventListener("keydown", function (ev) { if (ev.key === "Enter") create(); });
    els.version.addEventListener("keydown", function (ev) { if (ev.key === "Enter") create(); });
    els.categoryCustom.addEventListener("keydown", function (ev) { if (ev.key === "Enter") create(); });

    bindModSync();

    syncVisible();
    syncCategoryCustom();
    renderPortState();
    loadModSync();

    // 端口提示那几行和实例下拉都是拼出来的：换语言后按现有数据重说一遍，
    // 不重发 DetectMcPorts，也不碰玩家已经填过的端口
    VL.onLangChange(function () {
      renderInstances();
      renderPortHint();
      setBusy(!!els.btn.disabled);
    });
  };

  // 进入创建页时被 app.js 调用：触发自动检测 + 刷新模组同步设置
  VL.createAutoDetect = function () {
    autoDetectOnEnter();
    loadModSync();
  };

  VL.resetCreateForm = function () {
    if (!els.name) return;
    els.name.value = "";
    els.pw.value = "";
    els.version.value = "";
    if (els.loader) els.loader.value = "fabric";
    els.category.value = "";
    els.categoryCustom.value = "";
    els.categoryCustomField.classList.add("hidden");
    els.port.value = "";
    detectedPorts = [];
    if (els.portSelect) {
      els.portSelect.classList.add("hidden");
      els.portSelect.innerHTML = "";
    }
    if (els.portHint) {
      els.portHint.classList.remove("form-hint-ok", "form-hint-neutral");
      els.portHint.textContent = VL.t("voxlink.app.create.port_autohint", "进入此页后会自动检测本机 MC 端口…");
    }
    clearPortError();
    els.visible.checked = true;
    els.visible.disabled = false;
    els.warn.classList.add("hidden");
    els.error.classList.add("hidden");
  };
})(window.VL);