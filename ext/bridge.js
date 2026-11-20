// 汐流 · 桥接脚本（isolated world）
//
// background 想知道「这个 blob 是从哪个 http 地址来的」，但扩展世界看不到页面的
// 变量，只能靠 postMessage 去问 page-hook.js。这一层就干这一件事，3 秒拿不到答案
// 就回空，绝不拖住下载。

chrome.runtime.onMessage.addListener(function (msg, _sender, reply) {
  if (!msg || msg.type !== "blobInfo" || !msg.url) return;

  const rid = Math.random().toString(36).slice(2);
  let done = false;

  const finish = (info) => {
    if (done) return;
    done = true;
    window.removeEventListener("message", onMsg, false);
    try {
      reply({ info: info || null });
    } catch (e) {
      /* 通道关了就算了 */
    }
  };

  const onMsg = (ev) => {
    if (ev.source !== window) return;
    const d = ev.data;
    if (!d || d.__xflow !== "info" || d.rid !== rid) return;
    finish(d.info);
  };

  window.addEventListener("message", onMsg, false);
  try {
    window.postMessage({ __xflow: "query", rid, blobUrl: msg.url }, "*");
  } catch (e) {
    finish(null);
    return;
  }
  setTimeout(() => finish(null), 3000);
  return true; // 异步回复
});
