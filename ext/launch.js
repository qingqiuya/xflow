// 启动页：把 xflow:// 链接交给浏览器弹确认框
//
// 为什么要这么绕：MV3 的 service worker 里直接 chrome.tabs.create("xflow://…")
// 没有用户手势，Edge/Chrome 会静默拦掉（2026-11-15 在 9950 实测：地址栏手输
// 同一个链接能拉起，插件拉不动）。所以 background.js 改成打开这个页面，
// 由用户点一下按钮 —— 那一下点击就是合规的用户手势。

const raw = decodeURIComponent((location.hash || "").slice(1));
let proto = "";
let fname = "";

try {
  const u = new URL(raw);
  if (u.protocol === "xflow:") {
    proto = raw;
    fname = u.searchParams.get("name") || "";
  }
} catch (e) {
  /* 参数坏了，下面显示提示 */
}

const $go = document.getElementById("go");
const $bad = document.getElementById("bad");
const $fname = document.getElementById("fname");

if (proto) {
  $fname.textContent = fname || "（未命名任务）";
  $go.href = proto;
  $go.hidden = false;
  $go.focus();
  // 拉起之后这个标签页就没用了；用户点"取消"时浏览器不会导航，页面自然留着
  $go.addEventListener("click", () => {
    setTimeout(() => window.close(), 3000);
  });
} else {
  $bad.hidden = false;
}
