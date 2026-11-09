// 汐流（XFlow）浏览器接管 —— MV3 service worker
//
// 思路：浏览器开始下载 → 立刻取消 → 把 URL + Referer + Cookie 发给本机 XFlow。
// 能接管的只有 http(s) 直链；blob:/data:/POST 表单生成的下载拿不到可复用 URL，只能放行。

const DEFAULTS = { enabled: true, port: 17777, notify: true, segs: 16 };
const MENU_ID = "xflow-download-link";

async function cfg() {
  const c = await chrome.storage.local.get(DEFAULTS);
  return Object.assign({}, DEFAULTS, c);
}

function api(port, path) {
  return `http://127.0.0.1:${port}${path}`;
}

function baseName(p) {
  if (!p) return "";
  const s = String(p).split(/[\\/]/).filter(Boolean);
  return s.length ? s[s.length - 1] : "";
}

// 把 URL 里的文件名还原出来（下载项没给 filename 时用）
function nameFromURL(u) {
  try {
    const url = new URL(u);
    const last = url.pathname.split("/").filter(Boolean).pop() || "";
    return decodeURIComponent(last).replace(/[\\/:*?"<>|]/g, "_");
  } catch (e) {
    return "";
  }
}

async function cookieHeader(url) {
  try {
    const list = await chrome.cookies.getAll({ url });
    if (!list || !list.length) return "";
    return list.map((c) => `${c.name}=${c.value}`).join("; ");
  } catch (e) {
    return "";
  }
}

async function notify(title, message) {
  const c = await cfg();
  if (!c.notify) return;
  try {
    await chrome.notifications.create("", {
      type: "basic",
      iconUrl: "icon128.png",
      title,
      message: message || "",
    });
  } catch (e) {
    /* 通知失败不影响下载 */
  }
}

// 把任务推给 XFlow，返回 {ok, id} 或抛错
async function push(url, name, referer, cookie, dir) {
  const c = await cfg();
  const body = JSON.stringify({
    url,
    name: name || "",
    referer: referer || "",
    cookie: cookie || "",
    dir: dir || "",
    segs: c.segs,
  });
  const r = await fetch(api(c.port, "/api/add"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok || j.ok === false) {
    throw new Error(j.err || `HTTP ${r.status}`);
  }
  return j;
}

// 只处理浏览器真的能重放的地址
function usable(url) {
  return /^https?:\/\//i.test(url || "");
}

chrome.downloads.onCreated.addListener(async (item) => {
  const c = await cfg();
  if (!c.enabled) return;
  if (item.byExtensionId && item.byExtensionId === chrome.runtime.id) return;
  if (!usable(item.url)) {
    // blob: / data: / 扩展自身 —— 交给浏览器
    return;
  }
  // 先取消，再擦掉这条记录，免得浏览器还在后台偷偷跑
  try {
    await chrome.downloads.cancel(item.id);
    await chrome.downloads.erase({ id: item.id });
  } catch (e) {
    /* 已经跑完的会取消失败，那就别管了 */
  }
  const name = baseName(item.filename) || nameFromURL(item.url);
  const cookie = await cookieHeader(item.url);
  try {
    await push(item.url, name, item.referrer, cookie);
    await notify("已转给汐流", name || item.url);
  } catch (e) {
    await notify("汐流没接住", `${e.message}（确认 XFlow 在运行？）`);
  }
});

// 右键菜单：只把链接送过去，不碰浏览器自己的下载
chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: MENU_ID,
    title: "用汐流下载此链接",
    contexts: ["link", "image", "video", "audio"],
  });
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId !== MENU_ID) return;
  const url = info.linkUrl || info.srcUrl;
  if (!usable(url)) {
    await notify("这个链接发不了", "只有 http/https 直链能交给下载器");
    return;
  }
  const cookie = await cookieHeader(url);
  const referer = tab && tab.url ? tab.url : "";
  try {
    await push(url, nameFromURL(url), referer, cookie);
    await notify("已转给汐流", nameFromURL(url) || url);
  } catch (e) {
    await notify("汐流没接住", `${e.message}（确认 XFlow 在运行？）`);
  }
});

// popup 用：探一下 XFlow 在不在
chrome.runtime.onMessage.addListener((msg, _sender, reply) => {
  if (msg && msg.type === "ping") {
    cfg()
      .then((c) =>
        fetch(api(c.port, "/api/jobs"))
          .then((r) => r.json())
          .then((j) => reply({ online: true, jobs: (j.jobs || []).length, port: c.port }))
          .catch(() => reply({ online: false, port: c.port }))
      )
      .catch(() => reply({ online: false }));
    return true; // 异步回复
  }
});
