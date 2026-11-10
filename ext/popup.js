// popup 逻辑：只管开关和端口，重活都在 background.js
const DEFAULTS = { enabled: true, port: 17777, notify: true, segs: 16, minMB: 1, pauseUntil: 0 };

const el = (id) => document.getElementById(id);

async function load() {
  const c = await chrome.storage.local.get(DEFAULTS);
  el("enabled").checked = c.enabled !== false;
  el("notify").checked = c.notify !== false;
  el("port").value = c.port || 17777;
  el("minMB").value = c.minMB === undefined ? 1 : c.minMB;
  renderPause(c);
  test();
}

async function save(patch) {
  await chrome.storage.local.set(patch);
}

// 暂停状态：-1 = 长期；>0 且未到点 = 倒计时
function pauseLeft(c) {
  if (c.pauseUntil === -1) return Infinity;
  const left = (c.pauseUntil || 0) - Date.now();
  return left > 0 ? left : 0;
}

function renderPause(c) {
  const left = pauseLeft(c);
  const paused = left > 0;

  // 顶部副标题跟着状态走
  const sub = el("substate");
  if (paused) {
    sub.textContent = left === Infinity
      ? "已长期暂停 · 点「长期」恢复"
      : `已暂停 · 还有 ${Math.ceil(left / 60000)} 分钟`;
    sub.style.color = "#f0a020";
  } else {
    sub.textContent = "下载直接交给汐流";
    sub.style.color = "";
  }

  // 暂停按钮高亮：正在暂停时，把对应时长那个点亮
  document.querySelectorAll(".pause-btns button").forEach((b) => {
    b.classList.remove("active");
    if (!paused) return;
    if (b.id === "pauseForever" && left === Infinity) b.classList.add("active");
    if (b.dataset.min && left !== Infinity) {
      const mins = Math.ceil(left / 60000);
      if (Math.abs(mins - Number(b.dataset.min)) <= 1) b.classList.add("active");
    }
  });

  // 状态点
  const dot = el("dot");
  if (paused) {
    dot.className = "dot pause";
  }
}

function setStatus(online, port, jobs, c) {
  const dot = el("dot");
  const paused = pauseLeft(c) > 0;
  if (!online) {
    dot.className = "dot off";
    el("status").textContent = "未连接";
  } else if (paused) {
    dot.className = "dot pause";
    el("status").textContent = "暂停中";
  } else {
    dot.className = "dot on";
    el("status").textContent = jobs ? `${jobs} 个任务` : "已连接";
  }
}

function test() {
  chrome.runtime.sendMessage({ type: "ping" }, (res) => {
    const c = res ? { pauseUntil: res.pauseUntil || 0 } : {};
    if (chrome.runtime.lastError || !res) {
      setStatus(false, 17777, 0, c);
      return;
    }
    setStatus(res.online, res.port, res.jobs, c);
  });
}

// ---------------- 交互 ----------------

el("enabled").addEventListener("change", async (e) => {
  await save({ enabled: e.target.checked });
  const c = await chrome.storage.local.get(DEFAULTS);
  renderPause(c);
  test();
});

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

// 5 分 / 30 分 / 1 小时：同一个按钮再点一次就恢复
document.querySelectorAll(".pause-btns button[data-min]").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const c = await chrome.storage.local.get(DEFAULTS);
    const mins = Number(btn.dataset.min);
    const cur = Math.ceil(pauseLeft(c) / 60000);
    const isThisOne = pauseLeft(c) > 0 && c.pauseUntil !== -1 && Math.abs(cur - mins) <= 1;
    await save({ pauseUntil: isThisOne ? 0 : Date.now() + mins * 60000 });
    load();
  });
});

// 长期：再点一次恢复
el("pauseForever").addEventListener("click", async () => {
  const c = await chrome.storage.local.get(DEFAULTS);
  await save({ pauseUntil: c.pauseUntil === -1 ? 0 : -1 });
  load();
});

load();
// 每 20 秒刷一次状态（倒计时会自己走）
setInterval(load, 20000);
