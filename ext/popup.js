// popup 逻辑：只管开关和端口，重活都在 background.js
const DEFAULTS = { enabled: true, port: 17777, notify: true, segs: 16, minMB: 1, pauseUntil: 0, pauseMin: 30 };

const el = (id) => document.getElementById(id);

async function load() {
  const c = await chrome.storage.local.get(DEFAULTS);
  el("enabled").checked = c.enabled !== false;
  el("notify").checked = c.notify !== false;
  el("port").value = c.port || 17777;
  el("minMB").value = c.minMB === undefined ? 1 : c.minMB;
  el("pauseMin").value = c.pauseMin || 30;
  renderPause();
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
el("minMB").addEventListener("change", (e) => {
  const v = parseFloat(e.target.value);
  if (!isNaN(v) && v >= 0) save({ minMB: v });
});
el("retest").addEventListener("click", test);

// 暂停按钮：没暂停 → 按下拉里填的分钟数暂停；正在暂停 → 恢复
async function renderPause() {
  const c = await chrome.storage.local.get(DEFAULTS);
  const btn = el("pause");
  const forever = c.pauseUntil === -1;
  const left = (c.pauseUntil || 0) - Date.now();
  if (forever) {
    btn.textContent = "恢复（长期）";
    btn.style.color = "#f0a020";
    el("pauseForever").style.color = "#f0a020";
  } else if (left > 0) {
    const m = Math.ceil(left / 60000);
    btn.textContent = `恢复（剩 ${m} 分）`;
    btn.style.color = "#f0a020";
    el("pauseForever").style.color = "";
  } else {
    btn.textContent = "暂停";
    btn.style.color = "";
    el("pauseForever").style.color = "";
  }
}

el("pauseMin").addEventListener("change", (e) => {
  const v = parseInt(e.target.value, 10);
  if (v > 0 && v <= 1440) save({ pauseMin: v });
});

el("pauseForever").addEventListener("click", async () => {
  const c = await chrome.storage.local.get(DEFAULTS);
  const pausing = c.pauseUntil === -1 || (c.pauseUntil || 0) > Date.now();
  await save({ pauseUntil: pausing ? 0 : -1 });
  renderPause();
});

el("pause").addEventListener("click", async () => {
  const c = await chrome.storage.local.get(DEFAULTS);
  const pausing = c.pauseUntil === -1 || (c.pauseUntil || 0) > Date.now();
  if (pausing) {
    await save({ pauseUntil: 0 });
  } else {
    const mins = parseInt(el("pauseMin").value, 10) || 30;
    await save({ pauseUntil: Date.now() + mins * 60000, pauseMin: mins });
  }
  renderPause();
});

load();
