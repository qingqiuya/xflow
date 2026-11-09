// popup 逻辑：只管开关和端口，重活都在 background.js
const DEFAULTS = { enabled: true, port: 17777, notify: true, segs: 16 };

const el = (id) => document.getElementById(id);

async function load() {
  const c = await chrome.storage.local.get(DEFAULTS);
  el("enabled").checked = c.enabled !== false;
  el("notify").checked = c.notify !== false;
  el("port").value = c.port || 17777;
  test();
}

async function save(patch) {
  await chrome.storage.local.set(patch);
}

function setStatus(online, port, jobs) {
  const dot = el("dot");
  dot.className = "dot " + (online ? "on" : "off");
  el("status").textContent = online
    ? `已连接 · 127.0.0.1:${port}${jobs ? " · " + jobs + " 个任务" : ""}`
    : `没连上 · 127.0.0.1:${port}`;
}

function test() {
  el("status").textContent = "检测中…";
  el("dot").className = "dot";
  chrome.runtime.sendMessage({ type: "ping" }, (res) => {
    if (chrome.runtime.lastError || !res) {
      setStatus(false, el("port").value || 17777);
      return;
    }
    setStatus(res.online, res.port, res.jobs);
  });
}

el("enabled").addEventListener("change", (e) => save({ enabled: e.target.checked }));
el("notify").addEventListener("change", (e) => save({ notify: e.target.checked }));
el("port").addEventListener("change", (e) => {
  const v = parseInt(e.target.value, 10);
  if (v > 0 && v < 65536) {
    save({ port: v });
    test();
  }
});
el("retest").addEventListener("click", test);

load();
