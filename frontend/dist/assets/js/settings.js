/* ============================================================
 * VoxLink Desktop — settings.js
 * 设置：主题 / 玩家中继开关 / 加入前必装模组检查 / MC 实例管理 / 版本号
 * ============================================================ */
"use strict";

(function (VL) {
  var FALLBACK_VERSION = "1.0.2";
  // 加载器就给这三个，跟「创建房间」页的新建实例下拉保持一致
  var LOADERS = ["fabric", "forge", "neoforge"];
  var els = {};
  var msView = null;   // GetModSyncSettings 的回包，界面和写回都以它为准
  var msBusy = false;  // 一次只让一个写回去跑，连点会互相盖掉
  var armTimer = null; // 「删除」的二次确认，几秒不点就自己缩回去
  // 玩家手动改过加载器/版本后，自动识别就不再覆盖他的选择
  var msUserEdited = { loader: false, gameVersion: false };

  function cacheEls() {
    els.theme = VL.$("#set-theme");
    els.language = VL.$("#set-language");
    els.relay = VL.$("#set-relay");
    els.msJoin = VL.$("#set-ms-join");
    els.save = VL.$("#set-save");
    els.error = VL.$("#settings-error");
    els.version = VL.$("#settings-version");
    // MC 实例管理
    els.list = VL.$("#set-msi-list");
    els.empty = VL.$("#set-msi-empty");
    els.count = VL.$("#set-msi-count");
    els.msError = VL.$("#set-msi-error");
    els.msForm = VL.$("#set-msi-form");
    els.msNew = VL.$("#set-msi-new");
    els.msRefresh = VL.$("#set-msi-refresh");
    els.msName = VL.$("#set-msi-name");
    els.msLoader = VL.$("#set-msi-loader");
    els.msDir = VL.$("#set-msi-dir");
    els.msInferTip = VL.$("#set-msi-infer-tip");
    els.msGv = VL.$("#set-msi-gv");
    els.msSaveNew = VL.$("#set-msi-save-new");
    els.msCancelNew = VL.$("#set-msi-cancel-new");
    els.msFormTitle = VL.$("#set-msi-form-title");
    els.msModal = VL.$("#msi-modal");
    els.msModalClose = VL.$("#set-msi-modal-close");
    els.autoUpdate = VL.$("#set-auto-update");
    els.checkUpdate = VL.$("#set-check-update");
    els.updateStatus = VL.$("#set-update-status");
    els.msBrowse = VL.$("#set-msi-browse");
    els.msMakeActive = VL.$("#set-msi-make-active");
  }

  // 「加入前检查必装 Mod」和实例清单存在同一份 modsync.json（不是 settings.json），
  // 一次 GetModSyncSettings 把两边都刷回来。
  // SaveModSyncSettings 是整体回写，所以这里只取回来显示，写的时候再读一次现状。
  function loadModSync() {
    return VL.api("GetModSyncSettings").then(function (v) {
      msView = v || null;
      if (els.msJoin) els.msJoin.checked = !(msView && msView.joinRequiredModsCheck === false);
      renderInstances();
    }, function () { /* 后端不可用时保持默认勾选、列表留空，不打扰玩家 */ });
  }

  // 语言下拉的候选来自后端（languages = lang/ 里真有的代码），语言名用该语言自己的写法
  // （"日本語"而不是"Japanese"）——专有名称不随界面语言变，所以这份对照表内联在 core.js，
  // 不走文案表；只有"跟随系统"这一句是界面文案。
  // 手动检查更新：查到有更新就直接弹窗（等同开机提示）
  async function checkUpdate() {
    if (!els.checkUpdate) return;
    els.checkUpdate.disabled = true;
    if (els.updateStatus) els.updateStatus.textContent = VL.t("voxlink.app.settings.update_checking", "正在检查…");
    try {
      var info = await VL.api("CheckUpdate");
      info = info || {};
      if (info.error) {
        if (els.updateStatus) els.updateStatus.textContent = VL.t("voxlink.app.settings.update_failed", "检查失败：%s", info.error);
      } else if (info.available) {
        if (els.updateStatus) els.updateStatus.textContent = VL.t("voxlink.app.settings.update_found", "发现新版本 v%s", info.version || "");
        if (VL.update) VL.update.openModal(info);
      } else {
        if (els.updateStatus) els.updateStatus.textContent = VL.t("voxlink.app.settings.update_latest", "已是最新版本");
        VL.toast(VL.t("voxlink.app.settings.update_latest", "已是最新版本"), "success");
      }
    } catch (e) {
      if (els.updateStatus) els.updateStatus.textContent = VL.t("voxlink.app.settings.update_failed", "检查失败：%s", VL.errText(e));
    }
    els.checkUpdate.disabled = false;
  }

  function renderLanguageOptions() {
    if (!els.language) return;
    var html = '<option value="auto">' + VL.esc(VL.t("voxlink.app.settings.lang_auto", "跟随系统")) + "</option>";
    (VL.lang.available || []).forEach(function (code) {
      html += '<option value="' + VL.esc(code) + '">' + VL.esc(VL.langName(code)) + "</option>";
    });
    els.language.innerHTML = html;
    els.language.value = VL.lang.setting || "auto";
    if (els.language.selectedIndex < 0) els.language.value = "auto";
  }

  async function load() {
    try {
      var data = await VL.api("GetSettings");
      data = data || {};
      // 版本号从后端读取；后端未返回时兜底显示当前版本
      els.version.textContent = "v" + (data.version || FALLBACK_VERSION);
      VL.themes.current = VL.applyTheme(data.theme || "light");
      els.theme.value = VL.themes.current;
      // relayEnabled 缺省按模组的默认值 true（VoxLinkConfig.java:41）
      els.relay.checked = data.relayEnabled !== false;
      if (els.autoUpdate) els.autoUpdate.checked = data.autoUpdate !== false;
      if (data.language) VL.lang.setting = data.language;
      if (Array.isArray(data.languages)) VL.lang.available = data.languages;
      renderLanguageOptions();
    } catch (e) {
      els.version.textContent = "v" + FALLBACK_VERSION;
    }
  }

  // 改动即时保存；以后端返回值为准——非法代码会被后端静默忽略，界面不能显示自己传进去的那个。
  async function changeLanguage() {
    var picked = els.language.value;
    try {
      await VL.setLanguage(picked);
    } catch (e) { /* 无后端：语言包取不到，界面继续显示中文，不打扰玩家 */ }
    renderLanguageOptions();
  }

  // SaveSettings 保存主题与玩家中继开关（应用端不暴露服务器地址等参数）
  async function save() {
    els.error.classList.add("hidden");
    els.save.disabled = true;
    try {
      await VL.api("SaveSettings", JSON.stringify({
        theme: VL.themes.current,
        relayEnabled: !!els.relay.checked,
        autoUpdate: els.autoUpdate ? !!els.autoUpdate.checked : true
      }));
      VL.toast(VL.t("voxlink.app.action.saved", "已保存"), "success");
    } catch (e) {
      if (e.code !== "NO_BACKEND") {
        els.error.textContent = VL.errText(e);
        els.error.classList.remove("hidden");
      }
    }
    els.save.disabled = false;
  }

  async function changeTheme() {
    var t = VL.applyTheme(els.theme.value);
    VL.themes.current = t;
    try {
      await VL.api("SaveSettings", JSON.stringify({ theme: t }));
    } catch (e) { /* 主题即时生效，持久化失败不弹错误 */ }
  }

  async function changeRelay() {
    try {
      await VL.api("SaveSettings", JSON.stringify({ relayEnabled: !!els.relay.checked }));
    } catch (e) {
      if (e.code !== "NO_BACKEND") {
        els.error.textContent = VL.errText(e);
        els.error.classList.remove("hidden");
      }
      els.relay.checked = !els.relay.checked; // 保存失败就退回原状，别让界面骗人
    }
  }

  // 改动即时保存；失败就退回原状，别让界面骗人。
  // 写之前重新读一次现状：这个文件里还有实例清单和房主侧开关，少带字段等于把它们抹掉。
  async function changeModSyncJoin() {
    var before = els.msJoin.checked;
    try {
      var cur = await VL.api("GetModSyncSettings") || {};
      await VL.api("SaveModSyncSettings", JSON.stringify({
        instances: cur.instances || [],
        activeInstance: cur.activeInstance || "",
        joinRequiredModsCheck: !!before,
        hostModSyncPublish: !(cur.hostModSyncPublish === false)
      }));
    } catch (e) {
      if (e.code !== "NO_BACKEND") {
        els.error.textContent = VL.errText(e);
        els.error.classList.remove("hidden");
      }
      els.msJoin.checked = !before;
    }
  }

  /* ---------------- MC 实例管理 ----------------
   * 实例以前只能在「创建房间」页就地新建/删除，建好了就改不动。这里给每个实例
   * 摊开四个可以改的输入框：名字 / mods 目录 / 加载器 / MC 版本，外加删除和设为当前。
   * 存的是同一份 modsync.json，所以每次写回都必须带上完整的 instances /
   * activeInstance / 两个开关 —— 一律走 writeMs()，别处不要直接调 SaveModSyncSettings。 */

  function showMsError(msg) {
    if (!els.msError) return;
    els.msError.textContent = msg;
    els.msError.classList.remove("hidden");
  }

  function clearMsError() {
    if (!els.msError) return;
    els.msError.textContent = "";
    els.msError.classList.add("hidden");
  }

  function setMsBusy(b) {
    msBusy = b;
    [els.msNew, els.msSaveNew, els.msRefresh].forEach(function (el) {
      if (el) el.disabled = b;
    });
  }

  // 所有写回的唯一出口：先 Get 一次现状，只替换 patch 里带来的那部分，
  // 两个布尔开关原样带上（缺省按 true，跟后端一致），再整体写回。
  // SaveModSyncSettings 是整体覆盖，少带一个字段就等于把它删掉 —— 别处不要直接调它。
  function writeMs(patch) {
    if (msBusy) return Promise.resolve(null);
    setMsBusy(true);
    clearMsError();
    return VL.api("GetModSyncSettings").then(function (cur) {
      var base = cur || {};
      var payload = {
        instances: base.instances || [],
        activeInstance: base.activeInstance || "",
        joinRequiredModsCheck: !(base.joinRequiredModsCheck === false),
        hostModSyncPublish: !(base.hostModSyncPublish === false)
      };
      Object.keys(patch || {}).forEach(function (k) { payload[k] = patch[k]; });
      return VL.api("SaveModSyncSettings", JSON.stringify(payload));
    }).then(function (v) {
      if (v) msView = v;
      if (els.msJoin) els.msJoin.checked = !(msView && msView.joinRequiredModsCheck === false);
      setMsBusy(false);
      renderInstances(); // 画的就是刚写进去的那份，界面不会骗人
      return v || msView;
    }, function (e) {
      setMsBusy(false);
      // 失败时不重画：玩家已经打进框里的字不能被一次失败抹掉
      if (e && e.code !== "NO_BACKEND") showMsError(VL.errText(e));
      return null;
    });
  }

  function loaderOptions(current) {
    var html = "";
    LOADERS.forEach(function (l) {
      html += '<option value="' + l + '"' + (l === current ? " selected" : "") + ">" + l + "</option>";
    });
    return html;
  }

  // 编辑中的实例下标（-1 = 新增）。同一时刻只编辑一个：一张表单管"新建"和"改某个"，
  // 不会出现"改了三处到底存没存"的模糊状态。
  var editIdx = -1;

  function loaderOptions(current) {
    var html = "";
    LOADERS.forEach(function (l) {
      html += '<option value="' + l + '"' + (l === current ? " selected" : "") + ">" + l + "</option>";
    });
    return html;
  }

  function instanceNames(excludeIdx) {
    return ((msView && msView.instances) || [])
      .map(function (it, i) { return i === excludeIdx ? null : it.name; })
      .filter(Boolean);
  }

  /* 一个实例一张卡，**全部展开**。
   *
   * 旧版是"折叠成一行、点开才能改、改完要滚到最下面点全局保存"。三处成本叠加：
   *   - 折叠时看不到 mods 目录 —— 偏偏那是区分实例最关键的字段（理解成本）；
   *   - 改完得记住"还要去底部点保存"，忘了就走人，改动静默丢失（不确定感）；
   *   - "设为当前"即时生效、"保存修改"要手动点，两套语义并存（记忆成本）。
   * 现在每张卡自带「编辑」「删除」，当前实例用单选表达，一次操作一个明确结果。 */
  function cardHtml(it, i, isActive) {
    var nm = it.name || VL.t("voxlink.app.ms.unnamed", "（未命名）");
    return '<div class="msi-card' + (isActive ? " msi-card-active" : "") + '" data-idx="' + i + '" data-orig="' + VL.esc(it.name) + '">' +
      '<div class="msi-card-head">' +
        // 当前实例用单选表达：一眼看出"同时只有一个在生效"，切换是明确动作
        '<label class="msi-pick" title="' + VL.esc(VL.t("voxlink.app.ms.use_title", "创建房间时用这个实例的 mods 清单")) + '">' +
          '<input type="radio" name="msi-active" value="' + i + '"' + (isActive ? " checked" : "") +
          ' aria-label="' + VL.esc(VL.t("voxlink.app.ms.set_current", "设为当前实例")) + '">' +
          '<span class="msi-pick-dot" aria-hidden="true"></span>' +
        "</label>" +
        '<div class="msi-card-id">' +
          '<div class="msi-card-name">' + VL.esc(nm) + "</div>" +
          '<div class="msi-card-path" title="' + VL.esc(it.modsDir || "") + '">' +
            VL.esc(it.modsDir || VL.t("voxlink.app.ms.no_dir", "（没有填 mods 目录）")) + "</div>" +
        "</div>" +
        '<div class="msi-card-badges">' +
          (isActive ? '<span class="pill pill-accent">' + VL.esc(VL.t("voxlink.app.ms.in_use", "当前")) + "</span>" : "") +
          '<span class="pill">' + VL.esc(it.loader || "fabric") + "</span>" +
          (it.gameVersion ? '<span class="pill">' + VL.esc(it.gameVersion) + "</span>" : "") +
        "</div>" +
        '<div class="msi-card-acts">' +
          '<button type="button" class="btn btn-ghost btn-sm" data-act="edit">' +
            VL.esc(VL.t("voxlink.app.action.edit", "编辑")) + "</button>" +
          '<button type="button" class="btn btn-danger btn-sm" data-act="del">' +
            VL.esc(VL.t("voxlink.app.create.delete", "删除")) + "</button>" +
        "</div>" +
      "</div>" +
    "</div>";
  }

  function renderInstances() {
    if (!els.list) return;
    var list = (msView && msView.instances) || [];
    var active = (msView && msView.activeInstance) || "";
    clearTimeout(armTimer);
    if (els.count) {
      els.count.textContent = list.length
        ? VL.t("voxlink.app.settings.instance_count", "%s 个实例", list.length)
        : VL.t("voxlink.app.settings.no_instance", "还没有实例");
    }
    if (els.empty) els.empty.classList.toggle("hidden", list.length > 0);
    var html = "";
    list.forEach(function (it, i) { html += cardHtml(it, i, !!it.name && it.name === active); });
    els.list.innerHTML = html;
  }

  /* ---------------- 编辑表单（新建 / 改现有，共用一套） ---------------- */

  // 目录确定后自动识别加载器与 MC 版本；玩家手改过的字段不覆盖
  function inferFromDir() {
    var dir = (els.msDir.value || "").trim();
    if (!dir) return;
    VL.api("InferInstanceFromDir", JSON.stringify({ modsDir: dir })).then(function (res) {
      res = res || {};
      // 后端可能把玩家拖进来的 .minecraft 根目录下沉到 mods，回写真实路径
      if (res.modsDir && res.modsDir !== dir) els.msDir.value = res.modsDir;
      var applied = [];
      if (!msUserEdited.loader && res.loader) {
        els.msLoader.value = res.loader;
        applied.push(res.loader);
      }
      if (!msUserEdited.gameVersion && res.gameVersion) {
        els.msGv.value = res.gameVersion;
        applied.push(res.gameVersion);
      }
      if (els.msInferTip) {
        if (applied.length) {
          els.msInferTip.textContent = VL.t("voxlink.app.ms.inferred", "已自动识别：%s（可自行修改）", applied.join(" · "));
          els.msInferTip.classList.remove("hidden");
        } else {
          els.msInferTip.classList.add("hidden");
        }
      }
    }, function () { /* 识别失败静默：玩家手填一样能用 */ });
  }

  // 拖入文件夹：必须走 Wails 原生 OnFileDrop 才拿得到绝对路径。
  // WebView2 的 dataTransfer 里只有 File 对象、没有路径，靠它取路径是"拖进来没反应"的根因。
  function bindDropOn(input) {
    if (!input) return;
    // 拖到弹窗任意位置都算选中（玩家不用瞄准那条输入框）
    var box = (els.msModal && els.msModal.querySelector(".vl-modal")) || (input.closest ? input.closest(".msi-dir") : input);
    if (!box) box = input;
    // Wails 认这个 CSS 属性（默认 --wails-drop-target: drop）作为"这是放置目标"
    box.style.setProperty("--wails-drop-target", "drop");

    // 整窗提示层：拖入时明说"松手即选中"
    var veil = box.querySelector(".vl-drop-veil");
    if (!veil && box.classList.contains("vl-modal")) {
      veil = document.createElement("div");
      veil.className = "vl-drop-veil";
      veil.textContent = VL.t("voxlink.app.ms.drop_hint", "松手即选中这个 mods 文件夹");
      box.appendChild(veil);
    }

    function paint(on) {
      if (on) {
        box.classList.add("dragging");
        input.classList.add("drop-hot");
      } else {
        box.classList.remove("dragging");
        input.classList.remove("drop-hot");
      }
    }

    // 高亮只做视觉反馈；路径一律由 OnFileDrop 回调给
    box.addEventListener("dragover", function (ev) {
      ev.preventDefault();
      paint(true);
    });
    box.addEventListener("dragleave", function (ev) {
      // 只有真正离开 box 才熄灭（relatedTarget 仍在 box 内属于掠过子元素）
      var to = ev.relatedTarget;
      if (to && box.contains(to)) return;
      paint(false);
    });

    var rt = window.runtime;
    if (!rt || typeof rt.OnFileDrop !== "function") {
      return;
    }
    if (rt.CanResolveFilePaths && !rt.CanResolveFilePaths()) {
      return;
    }
    rt.OnFileDrop(function (x, y, paths) {
      paint(false);
      // 只在弹窗开着且落点在弹窗上时才接管，避免误吃别处的拖放
      if (!els.msModal || els.msModal.classList.contains("hidden")) return;
      var el = document.elementFromPoint(x, y);
      if (!el || !box.contains(el)) return;
      var path = (paths && paths[0]) || "";
      if (!path) return;
      input.value = path;
      input.dispatchEvent(new Event("input", { bubbles: true }));
      inferFromDir();
    }, true);
  }

  function openEditor(idx) {
    if (!els.msModal && !els.msForm) return;
    var list = (msView && msView.instances) || [];
    var it = idx >= 0 ? list[idx] : null;
    editIdx = it ? idx : -1;
    if (els.msFormTitle) {
      els.msFormTitle.textContent = it
        ? VL.t("voxlink.app.ms.edit_instance_title", "编辑实例")
        : VL.t("voxlink.app.ms.new_instance_title", "添加实例");
    }
    // 新建时按钮说「新建实例」，编辑时说「保存实例」——文案跟动作对齐
    if (els.msSaveNew) {
      els.msSaveNew.textContent = it
        ? VL.t("voxlink.app.ms.save_instance", "保存实例")
        : VL.t("voxlink.app.ms.create_instance", "新建实例");
    }
    els.msName.value = it ? (it.name || "") : "";
    els.msDir.value = it ? (it.modsDir || "") : "";
    els.msGv.value = it ? (it.gameVersion || "") : "";
    els.msLoader.value = it ? (it.loader || "fabric") : "fabric";
    // 编辑已有实例：默认不改写它已有的值，但空字段允许自动补全（纠错不覆盖）
    msUserEdited.loader = !!(it && it.loader);
    msUserEdited.gameVersion = !!(it && it.gameVersion);
    if (els.msInferTip) els.msInferTip.classList.add("hidden");
    if (els.msMakeActive) {
      // 新建且当前没有任何实例 → 默认勾上（否则"建了却没用上"很反直觉）
      els.msMakeActive.checked = it ? false : list.length === 0;
      els.msMakeActive.disabled = !!(it && it.name === ((msView && msView.activeInstance) || ""));
    }
    clearMsError();
    if (els.msModal) {
      els.msModal.classList.remove("hidden");
    } else if (els.msForm) {
      els.msForm.classList.remove("hidden");
    }
    if (els.msName) els.msName.focus();
  }

  function closeEditor() {
    if (els.msModal) els.msModal.classList.add("hidden");
    if (els.msForm) els.msForm.classList.add("hidden");
    editIdx = -1;
    els.msName.value = "";
    els.msDir.value = "";
    els.msGv.value = "";
    clearMsError();
  }

  // 校验并保存（新建追加 / 编辑原地替换）
  function commitEditor() {
    if (msBusy) return Promise.resolve(false);
    var name = (els.msName.value || "").trim();
    var dir = (els.msDir.value || "").trim();
    if (!name) { showMsError(VL.t("voxlink.app.ms.need_name", "给实例起个名字吧")); return Promise.resolve(false); }
    if (!dir) { showMsError(VL.t("voxlink.app.ms.need_dir", "要填 mods 目录，点「浏览」选一下")); return Promise.resolve(false); }
    if (instanceNames(editIdx).indexOf(name) >= 0) {
      showMsError(VL.t("voxlink.app.ms.dup_name", "已经有叫「%s」的实例了，换个名字吧", name));
      return Promise.resolve(false);
    }

    var list = ((msView && msView.instances) || []).slice();
    var item = {
      name: name,
      loader: els.msLoader.value || "fabric",
      gameVersion: (els.msGv.value || "").trim(),
      modsDir: dir
    };
    var active = (msView && msView.activeInstance) || "";
    var editing = editIdx >= 0;
    if (editing) {
      var orig = list[editIdx] && list[editIdx].name;
      list[editIdx] = item;
      // 改名的正是当前实例 → activeInstance 跟着搬，别让"当前"悄悄失效
      if (orig && orig === active) active = name;
      if (els.msMakeActive && els.msMakeActive.checked && !els.msMakeActive.disabled) active = name;
    } else {
      list.push(item);
      if (!active || (els.msMakeActive && els.msMakeActive.checked)) active = name;
    }
    return writeMs({ instances: list, activeInstance: active }).then(function (ok) {
      if (!ok) return false;
      closeEditor();
      VL.toast(VL.t("voxlink.app.ms.saved_instance", "已保存实例「%s」", name), "success");
      return true;
    });
  }

  /* ---------------- 单卡操作 ---------------- */

  function setActive(idx) {
    if (msBusy) return;
    var list = (msView && msView.instances) || [];
    var it = list[idx];
    if (!it) return;
    writeMs({ activeInstance: it.name }).then(function (ok) {
      if (ok) VL.toast(VL.t("voxlink.app.ms.now_sync", "创建房间现在用「%s」的 mods 清单", it.name), "success");
    });
  }

  function disarm(btn) {
    clearTimeout(armTimer);
    if (!btn) return;
    btn.classList.remove("armed");
    btn.textContent = VL.t("voxlink.app.create.delete", "删除");
  }

  // 删除要点两次：第一次变成「真删？再点一次」，几秒不点就缩回去
  function delInstance(btn, idx) {
    if (msBusy) return;
    if (!btn.classList.contains("armed")) {
      VL.$$(".msi-card .btn-danger.armed", els.list).forEach(disarm);
      btn.classList.add("armed");
      btn.textContent = VL.t("voxlink.app.ms.confirm_del", "真删？再点一次");
      armTimer = setTimeout(function () { disarm(btn); }, 5000);
      return;
    }
    disarm(btn);
    var list = ((msView && msView.instances) || []).slice();
    var it = list[idx];
    if (!it) return;
    var active = (msView && msView.activeInstance) || "";
    var wasActive = it.name === active;
    var rest = list.filter(function (_, i) { return i !== idx; });
    var patch = { instances: rest };
    // 删掉的正是「当前」那个：activeInstance 不能留一个已不存在的名字
    if (wasActive) patch.activeInstance = rest.length ? rest[0].name : "";
    writeMs(patch).then(function (ok) {
      if (!ok) return;
      if (editIdx === idx) closeEditor();
      if (!wasActive) {
        VL.toast(VL.t("voxlink.app.ms.deleted", "已删除实例「%s」", it.name), "success");
        return;
      }
      VL.toast(rest.length
        ? VL.t("voxlink.app.ms.deleted_active", "已删除当前实例「%s」，改用「%s」", it.name, rest[0].name)
        : VL.t("voxlink.app.ms.deleted_last", "已删除「%s」，实例已清空", it.name), "success");
    });
  }

  function bindInstances() {
    if (els.list) {
      // 编辑 / 删除
      els.list.addEventListener("click", function (ev) {
        var btn = ev.target.closest("button[data-act]");
        if (!btn) return;
        var card = btn.closest(".msi-card");
        if (!card) return;
        var idx = parseInt(card.getAttribute("data-idx"), 10);
        if (isNaN(idx)) return;
        if (btn.getAttribute("data-act") === "edit") openEditor(idx);
        else delInstance(btn, idx);
      });
      // 「当前实例」单选：用 change 而不是 click，键盘方向键也能切换
      els.list.addEventListener("change", function (ev) {
        if (!ev.target.matches('input[name="msi-active"]')) return;
        var idx = parseInt(ev.target.value, 10);
        if (!isNaN(idx)) setActive(idx);
      });
      // 点卡片主体（名字/mods 目录/徽章）等价于点那枚单选：
      // 整张卡是"这个实例"的语义单元，只有那个小圆点能点太考究鼠标精度。
      els.list.addEventListener("click", function (ev) {
        if (ev.target.closest("button[data-act]") || ev.target.closest(".msi-pick")) return;
        var card = ev.target.closest(".msi-card");
        if (!card || card.classList.contains("msi-card-active")) return;
        var radio = card.querySelector('input[name="msi-active"]');
        if (!radio) return;
        radio.checked = true;
        radio.dispatchEvent(new Event("change", { bubbles: true }));
      });
    }
    if (els.msModalClose) els.msModalClose.addEventListener("click", function () { closeEditor(); });
    if (els.msModal) {
      els.msModal.addEventListener("click", function (ev) {
        // 点遮罩（不是弹窗本体）即取消
        if (ev.target === els.msModal) closeEditor();
      });
      document.addEventListener("keydown", function (ev) {
        if (ev.key === "Escape" && !els.msModal.classList.contains("hidden")) closeEditor();
      });
    }
    if (els.msNew) els.msNew.addEventListener("click", function () { openEditor(-1); });
    if (els.msCancelNew) els.msCancelNew.addEventListener("click", function () { closeEditor(); });
    if (els.msSaveNew) els.msSaveNew.addEventListener("click", function () { commitEditor(); });
    if (els.msRefresh) els.msRefresh.addEventListener("click", function () { clearMsError(); closeEditor(); loadModSync(); });
    [els.msName, els.msDir, els.msGv].forEach(function (el) {
      if (el) el.addEventListener("keydown", function (ev) { if (ev.key === "Enter") commitEditor(); });
    });
    // 目录输入框：失焦即识别（手输路径也能自动填版本）
    if (els.msDir) {
      els.msDir.addEventListener("change", function () { inferFromDir(); });
      els.msDir.addEventListener("blur", function () { inferFromDir(); });
      bindDropOn(els.msDir);
    }
    // 玩家自己动过加载器/版本，就不再让自动识别覆盖
    if (els.msLoader) els.msLoader.addEventListener("change", function () { msUserEdited.loader = true; });
    if (els.msGv) els.msGv.addEventListener("input", function () { msUserEdited.gameVersion = true; });
    // 目录浏览：全局委托只认 data-browse-dir
    if (els.msBrowse) els.msBrowse.setAttribute("data-browse-dir", "1");
  }

  VL.initSettings = function () {
    cacheEls();
    els.save.addEventListener("click", save);
    els.theme.addEventListener("change", changeTheme);
    if (els.language) els.language.addEventListener("change", changeLanguage);
    els.relay.addEventListener("change", changeRelay);
    if (els.msJoin) els.msJoin.addEventListener("change", changeModSyncJoin);
    if (els.checkUpdate) {
      els.checkUpdate.addEventListener("click", function () { checkUpdate(); });
    }
    bindInstances();
    renderLanguageOptions();
    load().then(loadModSync);
    // 实例清单里的每一条文案都是 JS 拼出来的，换语言时得重画一遍（设置页里只有这一处）
    VL.onLangChange(function () { renderInstances(); renderLanguageOptions(); });
    // 设置页每次被打开时重新取一遍：建房页也会写同一份 modsync.json，只看一次会显示旧值
    // （实例清单跟那个开关在同一份文件里，一次 Get 顺便都刷回来）
    VL.refreshSettings = loadModSync;
  };
})(window.VL);
