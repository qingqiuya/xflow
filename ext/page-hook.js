// 汐流 · 页面侧钩子（world: MAIN，document_start）
//
// 为什么需要它：很多站点下载不是给一个 http 直链，而是页面里 fetch 完原文件、
// 用 URL.createObjectURL 变成 blob: 再触发保存。blob URL 只有这个页面自己能读，
// 插件（background）拿到 blob:xxx 束手无策。
// 这里在页面里记下「哪个 blob 是从哪个 http 地址来的」，插件下载时回来问一句，
// 就能把原直链交给汐流，浏览器那条 blob 下载照旧取消。
//
// 三条硬约束：
//   1. 极轻：只包两个函数，不读响应体、不改任何返回值、出错一律吞掉；
//   2. 不联网：查到的地址只回给同页面的桥接脚本，不外发；
//   3. 不碰页面逻辑：hook 里 try/catch 包死，失败就当没装。

(function () {
  if (window.__xflowHookInstalled) return;
  window.__xflowHookInstalled = true;

  var MAX = 60;          // 最近请求记录条数
  var WINDOW_MS = 15000; // 只认 15 秒内的请求（太老的八成不是这次下载）
  var reqs = [];         // {t, url, mime, len}
  var blobs = new Map(); // blobUrl -> {t, size, mime, from}

  function pushReq(r) {
    reqs.push(r);
    if (reqs.length > MAX) reqs.splice(0, reqs.length - MAX);
  }

  // 找一个跟这个 blob 对得上的 http 地址：时间最近 + MIME 相同优先
  function matchFrom(mime) {
    var now = Date.now();
    var best = null;
    for (var i = reqs.length - 1; i >= 0; i--) {
      var r = reqs[i];
      if (now - r.t > WINDOW_MS) continue;
      if (!/^https?:/i.test(r.url || "")) continue;
      var sameType = mime && r.mime && mime === r.mime;
      if (sameType) return r.url;
      if (!best) best = r.url;
    }
    return best;
  }

  // ---- 1. URL.createObjectURL ----
  try {
    var origCreate = URL.createObjectURL;
    URL.createObjectURL = function (obj) {
      var u = origCreate.apply(this, arguments);
      try {
        var mime = (obj && obj.type) || "";
        var size = (obj && typeof obj.size === "number") ? obj.size : 0;
        blobs.set(u, { t: Date.now(), size: size, mime: mime, from: matchFrom(mime) });
        if (blobs.size > MAX) {
          var oldest = blobs.keys().next().value;
          blobs.delete(oldest);
        }
      } catch (e) {}
      return u;
    };
  } catch (e) {}

  // ---- 2. fetch ----
  try {
    var origFetch = window.fetch;
    if (origFetch) {
      window.fetch = function (input, init) {
        var p = origFetch.apply(this, arguments);
        try {
          var guess = "";
          if (typeof input === "string") guess = input;
          else if (input && input.url) guess = input.url;
          if (guess) {
            p.then(function (res) {
              try {
                pushReq({
                  t: Date.now(),
                  url: res.url || guess,
                  mime: (res.headers.get("content-type") || "").split(";")[0].trim(),
                  len: parseInt(res.headers.get("content-length") || "0", 10) || 0
                });
              } catch (e) {}
            }, function () {});
          }
        } catch (e) {}
        return p;
      };
    }
  } catch (e) {}

  // ---- 3. XMLHttpRequest ----
  try {
    var origOpen = XMLHttpRequest.prototype.open;
    var origSend = XMLHttpRequest.prototype.send;
    XMLHttpRequest.prototype.open = function (method, url) {
      try { this.__xflowUrl = url; } catch (e) {}
      return origOpen.apply(this, arguments);
    };
    XMLHttpRequest.prototype.send = function () {
      var xhr = this;
      try {
        if (xhr.__xflowUrl) {
          xhr.addEventListener("loadend", function () {
            try {
              if (xhr.status < 200 || xhr.status >= 300) return;
              pushReq({
                t: Date.now(),
                url: xhr.responseURL || xhr.__xflowUrl,
                mime: (xhr.getResponseHeader("content-type") || "").split(";")[0].trim(),
                len: parseInt(xhr.getResponseHeader("content-length") || "0", 10) || 0
              });
            } catch (e) {}
          });
        }
      } catch (e) {}
      return origSend.apply(this, arguments);
    };
  } catch (e) {}

  // ---- 4. 回答桥接脚本的查询 ----
  window.addEventListener("message", function (ev) {
    try {
      if (ev.source !== window) return;
      var d = ev.data;
      if (!d || d.__xflow !== "query" || !d.rid) return;
      var rec = blobs.get(d.blobUrl) || null;
      var info = null;
      if (rec) {
        info = { from: rec.from || "", mime: rec.mime || "", size: rec.size || 0 };
      }
      window.postMessage({ __xflow: "info", rid: d.rid, info: info }, "*");
    } catch (e) {}
  }, false);
})();
