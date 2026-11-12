const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function element() {
  const classes = new Set();
  return {
    textContent: "",
    innerHTML: "",
    children: [],
    style: {},
    classList: {
      add(...xs) { xs.forEach(x => classes.add(x)); },
      remove(...xs) { xs.forEach(x => classes.delete(x)); },
      toggle(x, force) {
        if (force === undefined) { classes.has(x) ? classes.delete(x) : classes.add(x); }
        else if (force) classes.add(x); else classes.delete(x);
      },
      contains(x) { return classes.has(x); }
    },
    addEventListener() {},
    querySelector() { return null; },
    insertBefore() {},
    removeChild() {}
  };
}

const elements = new Map();
const VL = {
  $(selector) {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  },
  t(key, fallback, ...args) {
    let i = 0;
    return String(fallback).replace(/%s/g, () => String(args[i++] ?? ""));
  },
  esc(value) { return String(value); },
  relTime() { return "now"; },
  iconSvg() { return ""; },
  api: async () => ({}),
  showPage() {},
  onLangChange() {},
  copy() {}
};
const window = { VL, setTimeout, clearTimeout, setInterval, clearInterval };
const document = { createElement: element };
const file = path.join(__dirname, "..", "frontend", "dist", "assets", "js", "room.js");
let source = fs.readFileSync(file, "utf8");
source = source.replace("})(window.VL);", `
  VL.__stateTest = {
    start: connStart,
    reset: connReset,
    state: () => conn,
    setSession: value => { session = value; }
  };
})(window.VL);`);
vm.runInNewContext(source, { window, document, console, setTimeout, clearTimeout, setInterval, clearInterval });

function fresh() {
  VL.__stateTest.reset();
  VL.__stateTest.setSession({ state: "in_room", code: "TEST", isHost: false, clientType: "app" });
  VL.__stateTest.start();
}

fresh();
VL.onConnStateEvent({ phase: "turn", status: "trying", detail: "turn" });
VL.onConnStateEvent({ phase: "prelay", status: "trying", detail: "player" });
VL.onConnStateEvent({ phase: "p2p", status: "trying", detail: "中继均不可用，继续打洞…", progress: { round: 2 } });
let state = VL.__stateTest.state();
assert.strictEqual(state.lanes.turn, true);
assert.strictEqual(state.lanes.prelay, true);
assert.strictEqual(state.detail, "中继均不可用，继续打洞…");
assert.strictEqual(state.progress.round, 2);
assert.match(VL.$("#conn-phase-text").textContent, /继续打洞/);

VL.onConnStateEvent({ phase: "p2p", status: "trying", detail: "", progress: { round: 3 } });
state = VL.__stateTest.state();
assert.strictEqual(state.progress.round, 3);
assert.strictEqual(state.detail, "中继均不可用，继续打洞…");
assert.match(VL.$("#conn-phase-text").textContent, /继续打洞/);

fresh();
VL.onConnStateEvent({ phase: "turn", status: "success", address: "127.0.0.1:12345" });
VL.onConnStateEvent({ phase: "turn", status: "trying", detail: "link_lost" });
state = VL.__stateTest.state();
assert.strictEqual(state.status, "trying");
assert.strictEqual(state.address, "");

VL.__stateTest.reset();
console.log("room state regression: PASS");
