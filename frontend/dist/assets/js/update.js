/* ============================================================
 * VoxLink Desktop — update.js
 * 自动更新：启动检查 → 弹窗提示 → 立即更新(下载+替换+重启) / 稍后更新
 * ============================================================ */
"use strict";

(function (VL) {
  var els = {};
  var info = null;
  var busy = false;

  function cache() {
    els.modal = VL.$("#upd-modal");
    els.current = VL.$("#upd-current");
    els.latest = VL.$("#upd-latest");
    els.notes = VL.$("#upd-notes");
    els.src = VL.$("#upd-src");
    els.progress = VL.$("#upd-progress");
    els.fill = VL.$("#upd-progress-fill");
    els.progressText = VL.$("#upd-progress-text");
    els.now = VL.$("#upd-now");
    els.later = VL.$("#upd-later");
  }

  function openModal(v) {
    if (!els.modal) return;
    info = v;
    if (els.current) els.current.textContent = "v" + (v.current || "");
    if (els.latest) els.latest.textContent = "v" + (v.version || "");
    if (els.notes) els.notes.textContent = v.notes || VL.t("voxlink.app.update.no_notes", "本次更新没有额外说明");
    if (els.src) {
      els.src.textContent = v.source
        ? VL.t("voxlink.app.update.from", "更新来自 %s", v.source === "gitee" ? "Gitee" : "GitHub")
        : "";
    }
    if (els.progress) els.progress.classList.add("hidden");
    if (els.now) els.now.disabled = false;
    if (els.later) els.later.disabled = false;
    els.modal.classList.remove("hidden");
  }

  function closeModal() {
    if (els.modal) els.modal.classList.add("hidden");
  }

  /* ---------------- 立即更新 ---------------- */
  async function startNow() {
    if (busy || !info) return;
    busy = true;
    if (els.now) els.now.disabled = true;
    if (els.later) els.later.disabled = true;
    if (els.progress) els.progress.classList.remove("hidden");
    if (els.progressText) els.progressText.textContent = VL.t("voxlink.app.update.downloading", "正在下载…");

    try {
      var r = await VL.api("StartUpdateDownload");
      if (!r || !r.started) throw new Error((r && r.error) || "启动更新失败");
    } catch (e) {
      busy = false;
      if (els.now) els.now.disabled = false;
      if (els.later) els.later.disabled = false;
      VL.toast(VL.t("voxlink.app.update.failed", "更新失败：%s", e.message || e), "error");
      if (els.progress) els.progress.classList.add("hidden");
    }
  }

  /* ---------------- 稍后更新 ---------------- */
  async function later() {
    if (busy) return;
    try {
      await VL.api("RememberUpdateChoice", "later", info ? info.version : "");
    } catch (e) {
      /* 记录失败不拦玩家，下次启动仍会检查 */
    }
    VL.toast(VL.t("voxlink.app.update.later_toast", "好的，下次启动时再提醒你"), "info");
    closeModal();
  }

  /* ---------------- 事件：下载进度 / 失败 ---------------- */
  function bindEvents() {
    VL.on("update:progress", function (ev) {
      var p = (ev && ev.data && ev.data.progress) || 0;
      if (els.fill) els.fill.style.width = p + "%";
      if (els.progressText) {
        els.progressText.textContent = VL.t("voxlink.app.update.progress", "正在下载 %s%%…", p);
      }
    });
    VL.on("update:failed", function (ev) {
      busy = false;
      if (els.now) els.now.disabled = false;
      if (els.later) els.later.disabled = false;
      if (els.progress) els.progress.classList.add("hidden");
      var msg = (ev && ev.data && ev.data.error) || "未知错误";
      VL.toast(VL.t("voxlink.app.update.failed", "更新失败：%s", msg), "error");
    });
    VL.on("update:ready", function () {
      if (els.progressText) els.progressText.textContent = VL.t("voxlink.app.update.restarting", "更新完成，正在重启…");
      if (els.now) els.now.disabled = true;
      if (els.later) els.later.disabled = true;
    });
  }

  /* ---------------- 启动检查 ---------------- */
  async function checkOnBoot() {
    try {
      var r = await VL.api("AutoUpdateOnBoot");
      if (r && r.prompt && r.info) openModal(r.info);
    } catch (e) {
      /* 检查失败静默：开机不该被网络问题打断 */
    }
  }

  function bind() {
    cache();
    if (els.modal) {
      els.modal.addEventListener("click", function (ev) {
        // 点遮罩关闭 = 等于"稍后更新"，避免玩家直接关掉不再提醒
        if (ev.target === els.modal && !busy) later();
      });
    }
    if (els.now) els.now.addEventListener("click", startNow);
    if (els.later) els.later.addEventListener("click", later);
    document.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape" && els.modal && !els.modal.classList.contains("hidden") && !busy) {
        later();
      }
    });
  }

  VL.update = {
    bind: bind,
    bindEvents: bindEvents,
    checkOnBoot: checkOnBoot,
    openModal: openModal,
    closeModal: closeModal,
    isOpen: function () { return els.modal && !els.modal.classList.contains("hidden"); }
  };
})(window.VL);
