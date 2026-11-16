package top.qingqiuya.xflow;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.util.Log;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * 手机端 HTTP API。
 *
 * 有了它，电脑（或另一台手机）就能查任务、加任务、清历史，
 * 也是以后「任务同步」的地基 —— 桌面版那套 /api/* 在手机上有个简化版。
 *
 * 默认关，界面里手动打开；只监听局域网可用的端口，不往公网暴露。
 */
public class ApiServer {

    // 跟桌面版 XFlow 用同一个端口，两端记一个数就行。
    // 17777 不是 80/443/8080 那种重点封控对象，运营商走公网也过得去。
    public static final int DEFAULT_PORT = 17777;

    private final Context ctx;
    private volatile String token = "";
    private ServerSocket server;
    private Thread loop;
    private volatile boolean running;

    /** 待确认的接收（自动接收关掉时用） */
    private static class Pend {
        String id = "";
        String name = "";
        String from = "";
        String tmp = "";
        long size = 0;
    }

    private final java.util.List<Pend> pending = new java.util.ArrayList<>();

    private boolean autoAccept() {
        return ctx.getSharedPreferences("xflow_share", Context.MODE_PRIVATE)
                .getBoolean("auto_accept", true);
    }

    public ApiServer(Context ctx) {
        this.ctx = ctx.getApplicationContext();
    }

    public boolean isRunning() {
        return running;
    }

    /** 设置访问令牌；空字符串表示不校验（只用在纯回环调试） */
    public void setToken(String t) {
        token = t == null ? "" : t.trim();
    }

    public String getToken() {
        return token;
    }

    public synchronized void start(int port) {
        if (running) return;
        try {
            server = new ServerSocket();
            server.setReuseAddress(true);
            // 绑 :: 而不是 0.0.0.0：Linux/Android 默认 dual-stack，
            // 一个 socket 同时收 IPv4 和 IPv6，省得开两个
            server.bind(new InetSocketAddress(java.net.InetAddress.getByName("::"), port));
        } catch (Exception e) {
            return;
        }
        running = true;
        loop = new Thread(() -> {
            while (running) {
                try {
                    Socket s = server.accept();
                    new Thread(() -> handle(s), "xflow-api").start();
                } catch (Exception e) {
                    break;
                }
            }
        }, "xflow-api-accept");
        loop.setDaemon(true);
        loop.start();
    }

    public synchronized void stop() {
        running = false;
        try {
            if (server != null) server.close();
        } catch (Exception ignored) {
        }
        server = null;
    }

    // ------------------------------------------------------------ 请求处理

    private void handle(Socket s) {
        try (Socket sock = s) {
            sock.setSoTimeout(60000);
            // 从原始字节流读，别用 Reader：上传的是二进制，字符流会把文件搞坏
            java.io.InputStream raw = sock.getInputStream();
            String reqLine = readLine(raw);
            if (reqLine == null) return;
            String[] parts = reqLine.split(" ");
            if (parts.length < 2) return;
            String method = parts[0];
            String target = parts[1];

            int len = 0;
            Map<String, String> headers = new HashMap<>();
            String line;
            while ((line = readLine(raw)) != null && !line.isEmpty()) {
                int i = line.indexOf(':');
                if (i > 0) {
                    String k = line.substring(0, i).trim().toLowerCase();
                    String v = line.substring(i + 1).trim();
                    headers.put(k, v);
                    if (k.equals("content-length")) {
                        try {
                            len = Integer.parseInt(v);
                        } catch (Exception ignored) {
                        }
                    }
                }
            }

            String path = target;
            Map<String, String> q = new HashMap<>();
            int qi = target.indexOf('?');
            if (qi >= 0) {
                path = target.substring(0, qi);
                for (String kv : target.substring(qi + 1).split("&")) {
                    int e = kv.indexOf('=');
                    if (e > 0) {
                        q.put(dec(kv.substring(0, e)), dec(kv.substring(e + 1)));
                    } else if (!kv.isEmpty()) {
                        q.put(dec(kv), "");
                    }
                }
            }

            // 令牌校验：header 或 ?token= 都行（浏览器直接打开方便）
            // 例外：互传/剪贴板这几个接口在同一局域网内自己设备之间用，不折腾人抄令牌
            // （收文件那头有「自动接收」开关把关）
            boolean sharePath = path.equals("/api/fs") || path.equals("/api/upload")
                    || path.equals("/api/download") || path.equals("/api/share")
                    || path.equals("/api/files") || path.equals("/api/clipboard")
                    || path.equals("/api/send") || path.equals("/api/incoming");
            // 只有「同一局域网（私有地址）」来的互传请求才免令牌。
            // 公网来的（比如以后开了端口映射/DDNS）一律要令牌 —— 清秋 2026-11-16 定。
            boolean lanPeer = isPrivateAddr(sock.getInetAddress());
            if (!token.isEmpty() && !(sharePath && lanPeer)) {
                String got = headers.getOrDefault("x-api-token", "");
                if (got.isEmpty()) got = q.getOrDefault("token", "");
                if (!token.equals(got)) {
                    Log.w("xflow-api", "拒绝未授权请求 " + path + " from " + sock.getInetAddress());
                    writeResponse(sock, 401, "{\"ok\":false,\"err\":\"需要 X-API-Token\"}");
                    return;
                }
            }

            // ① 裸 body 上传（桌面端用这套：X-File-Name 头 + 文件字节流）
            String fname = headers.getOrDefault("x-file-name", "");
            if (!fname.isEmpty() && "/api/upload".equals(path)) {
                try {
                    if (autoAccept()) {
                        String saved = Transfer.saveStream(ctx, raw, dec(fname), len);
                        writeResponse(sock, 200, "{\"ok\":true,\"saved\":[\"" + esc(saved)
                                + "\"],\"dir\":\"Download/" + Transfer.SUBDIR + "\"}");
                    } else {
                        // 手动同意：先落临时，等用户点接收
                        String id = Long.toHexString(System.nanoTime());
                        java.io.File dir = new java.io.File(ctx.getCacheDir(), "incoming");
                        dir.mkdirs();
                        java.io.File tmp = new java.io.File(dir, id);
                        long got = 0;
                        try (java.io.FileOutputStream fo = new java.io.FileOutputStream(tmp)) {
                            byte[] buf = new byte[64 * 1024];
                            long remain = len;
                            while (remain > 0) {
                                int want = (int) Math.min(buf.length, remain);
                                int n = raw.read(buf, 0, want);
                                if (n <= 0) break;
                                fo.write(buf, 0, n);
                                remain -= n;
                                got += n;
                            }
                        }
                        Pend p = new Pend();
                        p.id = id;
                        p.name = dec(fname);
                        p.from = String.valueOf(sock.getInetAddress().getHostAddress());
                        p.tmp = tmp.getAbsolutePath();
                        p.size = got;
                        synchronized (pending) {
                            pending.add(p);
                        }
                        writeResponse(sock, 200, "{\"ok\":true,\"pending\":true,\"id\":\"" + id + "\"}");
                    }
                } catch (Exception e) {
                    writeResponse(sock, 500, "{\"ok\":false,\"err\":\"" + esc(String.valueOf(e.getMessage())) + "\"}");
                }
                return;
            }

            // ② 下载：直接把文件字节写回去
            if ("/api/download".equals(path)) {
                java.io.InputStream fin;
                try {
                    fin = Transfer.open(ctx, q.getOrDefault("uri", ""));
                } catch (Exception e) {
                    writeResponse(sock, 404, "{\"ok\":false,\"err\":\"" + esc(String.valueOf(e.getMessage())) + "\"}");
                    return;
                }
                writeStream(sock, q.getOrDefault("name", "file"), fin);
                return;
            }

            // ③ 其余：body 当 JSON/表单读
            String body = "";
            if (len > 0) {
                byte[] buf = new byte[len];
                int got = 0;
                while (got < len) {
                    int n = raw.read(buf, got, len - got);
                    if (n < 0) break;
                    got += n;
                }
                body = new String(buf, 0, got, StandardCharsets.UTF_8);
            }
            if (!body.isEmpty()) {
                parseFlatJSON(body, q);
                // 也认表单格式（curl -d "autoAccept=false" 这种）
                String bt = body.trim();
                if (!bt.startsWith("{") && bt.contains("=")) {
                    for (String kv : bt.split("&")) {
                        int e = kv.indexOf('=');
                        if (e > 0) q.put(dec(kv.substring(0, e)), dec(kv.substring(e + 1)));
                    }
                }
            }

            String out = route(method, path, q);
            if (out == null) return;
            writeResponse(sock, 200, out);
        } catch (Exception e) {
            Log.w("xflow-api", "handle 出错: " + e);
        }
    }

    /** 逐字节读一行（header 用），不缓冲、不吃掉二进制。 */
    private static String readLine(java.io.InputStream in) throws Exception {
        java.io.ByteArrayOutputStream b = new java.io.ByteArrayOutputStream();
        int c = -1;
        while ((c = in.read()) >= 0) {
            if (c == '\n') break;
            if (c != '\r') b.write(c);
            if (b.size() > 8192) break;
        }
        if (c < 0 && b.size() == 0) return null;
        return new String(b.toByteArray(), StandardCharsets.UTF_8);
    }

    /** 把文件流式写回给调用方（下载用），不占内存。 */
    private void writeStream(Socket sock, String name, java.io.InputStream fin) throws Exception {
        java.io.OutputStream os = sock.getOutputStream();
        String head = "HTTP/1.1 200 OK\r\n" +
                "Content-Type: application/octet-stream\r\n" +
                "Content-Disposition: attachment; filename=\"" + name.replace("\"", "") + "\"\r\n" +
                "Connection: close\r\n\r\n";
        os.write(head.getBytes(StandardCharsets.UTF_8));
        byte[] buf = new byte[64 * 1024];
        int n;
        try {
            while ((n = fin.read(buf)) > 0) os.write(buf, 0, n);
            os.flush();
        } finally {
            try {
                fin.close();
            } catch (Exception ignored) {
            }
        }
    }

    private void writeResponse(Socket sock, int code, String body) throws Exception {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        OutputStream os = sock.getOutputStream();
        String status = code == 200 ? "200 OK" : (code == 401 ? "401 Unauthorized" : code + " Error");
        os.write(("HTTP/1.1 " + status + "\r\n" +
                "Content-Type: application/json; charset=utf-8\r\n" +
                "Content-Length: " + b.length + "\r\n" +
                "Access-Control-Allow-Origin: *\r\n" +
                "Connection: close\r\n\r\n").getBytes(StandardCharsets.UTF_8));
        os.write(b);
        os.flush();
    }

    private String route(String method, String path, Map<String, String> q) {
        if (path.equals("/api/clipboard")) {
            ClipboardManager cm = (ClipboardManager) ctx.getSystemService(Context.CLIPBOARD_SERVICE);
            if ("GET".equals(method)) {
                // Android 10+ 后台读剪贴板会被拦住，读不到就返回空串，不报错
                String t = "";
                try {
                    ClipData cp = cm.getPrimaryClip();
                    if (cp != null && cp.getItemCount() > 0) {
                        t = String.valueOf(cp.getItemAt(0).coerceToText(ctx));
                    }
                } catch (Exception ignored) {
                }
                return "{\"ok\":true,\"text\":\"" + esc(t) + "\",\"note\":\"后台读取可能被系统限制\"}";
            }
            String text = q.getOrDefault("text", "");
            try {
                cm.setPrimaryClip(ClipData.newPlainText("XFlow", text));
                return "{\"ok\":true,\"len\":" + text.length() + "}";
            } catch (Exception e) {
                return "{\"ok\":false,\"err\":\"" + esc(String.valueOf(e.getMessage())) + "\"}";
            }
        }
        if (path.equals("/api/share")) {
            // 互传：收件目录 + 自动接收开关（POST 可以改）
            if ("POST".equals(method)) {
                String v = q.getOrDefault("autoAccept", "");
                if (!v.isEmpty()) {
                    boolean b = "true".equalsIgnoreCase(v) || "1".equals(v);
                    ctx.getSharedPreferences("xflow_share", Context.MODE_PRIVATE)
                            .edit().putBoolean("auto_accept", b).apply();
                }
            }
            return "{\"ok\":true,\"platform\":\"android\",\"dir\":\"Download/" + Transfer.SUBDIR
                    + "\",\"autoAccept\":" + autoAccept() + "}";
        }
        if (path.equals("/api/incoming")) {
            if ("POST".equals(method)) {
                String id = q.getOrDefault("id", "");
                String act = q.getOrDefault("act", "");
                Pend hit = null;
                synchronized (pending) {
                    for (Pend p : pending) {
                        if (p.id.equals(id)) {
                            hit = p;
                            break;
                        }
                    }
                    if (hit != null) pending.remove(hit);
                }
                if (hit == null) return "{\"ok\":false,\"err\":\"没这个待接收文件\"}";
                java.io.File tmp = new java.io.File(hit.tmp);
                if (!"accept".equals(act)) {
                    tmp.delete();
                    return "{\"ok\":true,\"act\":\"reject\"}";
                }
                try (java.io.FileInputStream in = new java.io.FileInputStream(tmp)) {
                    String saved = Transfer.saveStream(ctx, in, hit.name, tmp.length());
                    tmp.delete();
                    return "{\"ok\":true,\"act\":\"accept\",\"saved\":\"" + esc(saved) + "\"}";
                } catch (Exception e) {
                    return "{\"ok\":false,\"err\":\"" + esc(String.valueOf(e.getMessage())) + "\"}";
                }
            }
            StringBuilder sb = new StringBuilder("{\"ok\":true,\"files\":[");
            synchronized (pending) {
                for (int i = 0; i < pending.size(); i++) {
                    if (i > 0) sb.append(',');
                    Pend p = pending.get(i);
                    sb.append("{\"id\":\"").append(p.id).append("\",\"name\":\"").append(esc(p.name))
                            .append("\",\"size\":").append(p.size)
                            .append(",\"from\":\"").append(esc(p.from)).append("\"}");
                }
            }
            return sb.append("]}").toString();
        }
        if (path.equals("/api/files")) {
            List<Transfer.Item> items = Transfer.list(ctx);
            StringBuilder sb = new StringBuilder("{\"ok\":true,\"dir\":\"Download/" + Transfer.SUBDIR + "\",\"items\":[");
            for (int i = 0; i < items.size(); i++) {
                if (i > 0) sb.append(',');
                Transfer.Item it = items.get(i);
                sb.append("{\"name\":\"").append(esc(it.name)).append("\",\"size\":").append(it.size)
                        .append(",\"uri\":\"").append(esc(it.uri)).append("\"}");
            }
            return sb.append("]}").toString();
        }
        if (path.equals("/api/version")) {
            String v = "";
            try {
                v = ctx.getPackageManager().getPackageInfo(ctx.getPackageName(), 0).versionName;
            } catch (Exception ignored) {
            }
            return "{\"ok\":true,\"app\":\"XFlow\",\"platform\":\"android\",\"version\":\"" + esc(v) + "\"}";
        }
        if (path.equals("/api/jobs")) {
            List<Job> jobs;
            synchronized (DownloadService.JOBS) {
                jobs = new ArrayList<>(DownloadService.JOBS);
            }
            StringBuilder sb = new StringBuilder("{\"ok\":true,\"jobs\":[");
            for (int i = 0; i < jobs.size(); i++) {
                if (i > 0) sb.append(',');
                sb.append(jobJSON(jobs.get(i)));
            }
            return sb.append("]}").toString();
        }
        if (path.equals("/api/add")) {
            String url = q.getOrDefault("url", "").trim();
            if (url.isEmpty()) return "{\"ok\":false,\"err\":\"缺 url\"}";
            Intent i = new Intent(ctx, DownloadService.class);
            i.setAction(DownloadService.ACTION_ADD);
            i.putExtra("url", url);
            i.putExtra("name", q.getOrDefault("name", ""));
            ctx.startForegroundService(i);
            return "{\"ok\":true}";
        }
        if (path.equals("/api/clear")) {
            int n = 0;
            synchronized (DownloadService.JOBS) {
                for (int i = DownloadService.JOBS.size() - 1; i >= 0; i--) {
                    Job j = DownloadService.JOBS.get(i);
                    if ("done".equals(j.state) || "error".equals(j.state)) {
                        DownloadService.JOBS.remove(i);
                        n++;
                    }
                }
            }
            return "{\"ok\":true,\"removed\":" + n + "}";
        }
        if (path.startsWith("/api/job/")) {
            String id = path.substring("/api/job/".length());
            String act = q.getOrDefault("action", "");
            Job target = null;
            synchronized (DownloadService.JOBS) {
                for (Job j : DownloadService.JOBS) {
                    if (j.id.equals(id)) {
                        target = j;
                        break;
                    }
                }
            }
            if (target == null) return "{\"ok\":false,\"err\":\"没这个任务\"}";
            Intent i = new Intent(ctx, DownloadService.class);
            i.putExtra("id", id);
            switch (act) {
                case "pause":
                    i.setAction(DownloadService.ACTION_PAUSE);
                    break;
                case "resume":
                    i.setAction(DownloadService.ACTION_RESUME);
                    break;
                case "delete":
                    i.setAction(DownloadService.ACTION_CANCEL);
                    break;
                default:
                    return "{\"ok\":false,\"err\":\"action 只能是 pause/resume/delete\"}";
            }
            ctx.startForegroundService(i);
            return "{\"ok\":true}";
        }
        return "{\"ok\":false,\"err\":\"没有这个接口\"}";
    }

    private String jobJSON(Job j) {
        StringBuilder sb = new StringBuilder("{");
        sb.append("\"id\":\"").append(esc(j.id)).append("\",");
        sb.append("\"name\":\"").append(esc(j.name)).append("\",");
        sb.append("\"url\":\"").append(esc(j.url)).append("\",");
        sb.append("\"state\":\"").append(esc(j.state)).append("\",");
        sb.append("\"kind\":\"").append(esc(j.kind)).append("\",");
        sb.append("\"done\":").append(j.done).append(',');
        sb.append("\"total\":").append(j.total).append(',');
        sb.append("\"percent\":").append(j.percent()).append(',');
        sb.append("\"speed\":").append(j.speed).append(',');
        sb.append("\"path\":\"").append(esc(j.path)).append("\",");
        sb.append("\"err\":\"").append(esc(j.err)).append("\"");
        return sb.append('}').toString();
    }

    // ------------------------------------------------------------ 小工具

    /** 私有局域网地址（10/8、172.16/12、192.168/16）+ 回环 + Tailscale 的 100.64/10。 */
    private static boolean isPrivateAddr(java.net.InetAddress a) {
        if (a == null) return false;
        if (a.isLoopbackAddress() || a.isSiteLocalAddress()) return true;
        byte[] b = a.getAddress();
        if (b.length == 4) {
            int f = b[0] & 0xFF;
            int s = b[1] & 0xFF;
            if (f == 100 && s >= 64 && s <= 127) return true; // CGNAT / Tailscale
        }
        return false;
    }

    private static String dec(String s) {
        try {
            return URLDecoder.decode(s, "UTF-8");
        } catch (Exception e) {
            return s;
        }
    }

    private static String esc(String s) {
        if (s == null) return "";
        return s.replace("\\", "\\\\").replace("\"", "\\\"")
                .replace("\n", "\\n").replace("\r", "");
    }

    /** 只认 {"k":"v","n":123} 这种扁平 JSON，够用了，不引第三方库 */
    private static void parseFlatJSON(String s, Map<String, String> out) {
        int i = 0, n = s.length();
        while (i < n) {
            int k = s.indexOf('"', i);
            if (k < 0) break;
            int k2 = s.indexOf('"', k + 1);
            if (k2 < 0) break;
            String key = s.substring(k + 1, k2);
            int c = s.indexOf(':', k2);
            if (c < 0) break;
            int v = c + 1;
            while (v < n && Character.isWhitespace(s.charAt(v))) v++;
            String val;
            if (v < n && s.charAt(v) == '"') {
                StringBuilder sb = new StringBuilder();
                int p = v + 1;
                while (p < n) {
                    char ch = s.charAt(p);
                    if (ch == '\\' && p + 1 < n) {
                        sb.append(s.charAt(p + 1));
                        p += 2;
                        continue;
                    }
                    if (ch == '"') break;
                    sb.append(ch);
                    p++;
                }
                val = sb.toString();
                i = p + 1;
            } else {
                int p = v;
                while (p < n && s.charAt(p) != ',' && s.charAt(p) != '}') p++;
                val = s.substring(v, p).trim();
                i = p;
            }
            out.put(key, val);
        }
    }
}
