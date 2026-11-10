// 汐流（XFlow）浏览器接管 —— MV3 service worker
//
// 流程对照了 Motrix Next 和 Aria2 Explorer 两个成熟实现，关键两条：
//   1. **先发给下载器，确认收下了才取消浏览器下载**。顺序反了是真 bug：
//      下载器没开时文件被取消掉，用户白点一次、还得重来。
//   2. 同时挂 onDeterminingFilename 和 onCreated，谁先到用谁。
//      onDeterminingFilename 给的 filename 更准（浏览器已解析完 Content-Disposition），
//      但它要求同步 suggest()，worker 冷启动时可能来不及；onCreated 更可靠。
//      两个事件用 download id 去重，不会重复接管。

const DEFAULTS = {
  enabled: true,
  port: 17777,
  notify: true,
  segs: 16,
  minMB: 1, // 小于这个尺寸不接管，交回浏览器（小文件走下载器反而慢）
  pauseUntil: 0, // 临时暂停接管的截止时间戳；0 = 没暂停
};
const MENU_ID = "xflow-download-link";
const DUP_WINDOW_MS = 5000; // 同一 URL 这么短时间内重复出现，当成连点

let C = { ...DEFAULTS }; // 内存缓存：onDeterminingFilename 必须同步回调，不能 await

async function reloadCfg() {
  const c = await chrome.storage.local.get(DEFAULTS);
  C = { ...DEFAULTS, ...c };
}
reloadCfg();
chrome.storage.onChanged.addListener((_ch, area) => {
  if (area === "local") reloadCfg();
});

const DOC_EXT = /\.(?:html?|xhtml|mhtml?|xml|svg)$/i; // 整页另存不当下载
const recent = new Map(); // url -> 时间戳
const claimed = new Set(); // 已接管的 download id（两个事件共用）

function api(path) {
  return `http://127.0.0.1:${C.port}${path}`;
}

function baseName(p) {
  if (!p) return "";
  const s = String(p).split(/[\\/]/).filter(Boolean);
  return s.length ? s[s.length - 1] : "";
}

function nameFromURL(u) {
  try {
    const last = new URL(u).pathname.split("/").filter(Boolean).pop() || "";
    return decodeURIComponent(last).replace(/[\\/:*?"<>|]/g, "_");
  } catch (e) {
    return "";
  }
}

async function cookieHeader(url) {
  try {
    const list = await chrome.cookies.getAll({ url });
    return (list || []).map((c) => `${c.name}=${c.value}`).join("; ");
  } catch (e) {
    return "";
  }
}

async function notify(title, message) {
  if (!C.notify) return;
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

// 推任务给 XFlow；失败抛错，调用方据此决定是否取消浏览器下载
async function push(url, name, referer, cookie, dir) {
  const body = JSON.stringify({
    url,
    name: name || "",
    referer: referer || "",
    cookie: cookie || "",
    dir: dir || "",
    segs: C.segs,
  });
  const r = await fetch(api("/api/add"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  const j = await r.json().catch(() => ({}));
  if (j.ok === false) throw new Error(j.err || "下载器拒绝了任务");
  return j;
}

// 同步判定：只看这条下载本身，不碰网络
function shouldTake(item) {
  if (!C.enabled) return false;
  if (C.pauseUntil && Date.now() < C.pauseUntil) return false; // 临时暂停中
  if (!item || item.state !== "in_progress" || item.error) return false;
  if (!/^https?:\/\//i.test(item.url || "")) return false; // blob: / data: / ftp:
  if (item.byExtensionId) return false; // 别的扩展（含自己）发起的，不抢
  if (item.incognito) return false;

  const name = baseName(item.filename) || nameFromURL(item.url);
  if (DOC_EXT.test(name)) return false;

  const size = item.totalBytes || item.fileSize || -1;
  if (size >= 0 && size < C.minMB * 1024 * 1024) return false;

  const now = Date.now();
  for (const [k, t] of recent) if (now - t > DUP_WINDOW_MS) recent.delete(k);
  if (recent.has(item.url)) return false;
  recent.set(item.url, now);
  return true;
}

// 两个事件都会走到这里；用 download id 保证同一任务只接一次
function claim(item) {
  if (claimed.has(item.id)) return false;
  if (!shouldTake(item)) return false;
  claimed.add(item.id);
  setTimeout(() => claimed.delete(item.id), 30000);
  return true;
}

// 接管一条下载：先发，成功了才取消浏览器那边
async function takeover(item) {
  const name = baseName(item.filename) || nameFromURL(item.url);
  const cookie = await cookieHeader(item.url);
  try {
    await push(item.url, name, item.referrer || "", cookie);
    try {
      await chrome.downloads.cancel(item.id);
      await chrome.downloads.erase({ id: item.id });
    } catch (e) {
      /* 取消失败说明已经跑完了，无所谓 */
    }
    await notify("已转给汐流", name || item.url);
  } catch (e) {
    // 下载器没接住：浏览器那边继续下，用户不丢文件
    await notify("汐流没接住，已交回浏览器", `${e.message}（确认 XFlow 在运行？）`);
  }
}

chrome.downloads.onDeterminingFilename.addListener((item, suggest) => {
  suggest(); // 必须同步调用，且不干预浏览器的命名决定
  if (claim(item)) takeover(item);
});

chrome.downloads.onCreated.addListener((item) => {
  if (claim(item)) takeover(item);
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
  await reloadCfg();
  if (C.pauseUntil && Date.now() < C.pauseUntil) {
    await notify("接管已暂停", "先恢复接管再送任务（或者直接拖进汐流窗口）");
    return;
  }
  const url = info.linkUrl || info.srcUrl;
  // 磁力链接浏览器不会当"下载"，只能靠右键菜单送过去
  if (!/^(https?:\/\/|magnet:\?)/i.test(url || "")) {
    await notify("这个链接发不了", "只认 http/https 直链和 magnet 磁力链接");
    return;
  }
  const isMagnet = /^magnet:\?/i.test(url);
  const name = isMagnet ? "" : nameFromURL(url);
  const cookie = isMagnet ? "" : await cookieHeader(url);
  try {
    await push(url, name, (tab && tab.url) || "", cookie);
    await notify("已转给汐流", name || url);
  } catch (e) {
    await notify("汐流没接住", `${e.message}（确认 XFlow 在运行？）`);
  }
});

// popup 用：探一下 XFlow 在不在
chrome.runtime.onMessage.addListener((msg, _sender, reply) => {
  if (!msg || msg.type !== "ping") return;
  fetch(api("/api/jobs"))
    .then((r) => r.json())
    .then((j) => reply({ online: true, jobs: (j.jobs || []).length, port: C.port }))
    .catch(() => reply({ online: false, port: C.port }));
  return true; // 异步回复
});
