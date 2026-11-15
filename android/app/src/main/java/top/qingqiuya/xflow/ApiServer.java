package top.qingqiuya.xflow;

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

    public static final int DEFAULT_PORT = 8080;

    private final Context ctx;
    private volatile String token = "";
    private ServerSocket server;
    private Thread loop;
    private volatile boolean running;

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
            sock.setSoTimeout(8000);
            BufferedReader in = new BufferedReader(
                    new InputStreamReader(sock.getInputStream(), StandardCharsets.UTF_8));
            String reqLine = in.readLine();
            if (reqLine == null) return;
            String[] parts = reqLine.split(" ");
            if (parts.length < 2) return;
            String method = parts[0];
            String target = parts[1];

            // 读完 header，拿 Content-Length
            int len = 0;
            Map<String, String> headers = new HashMap<>();
            String line;
            while ((line = in.readLine()) != null && !line.isEmpty()) {
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
            String body = "";
            if (len > 0) {
                char[] buf = new char[len];
                int got = 0;
                while (got < len) {
                    int n = in.read(buf, got, len - got);
                    if (n < 0) break;
                    got += n;
                }
                body = new String(buf, 0, got);
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
            // POST body 里的 JSON 字段也塞进 q（简单扁平 JSON）
            if (!body.isEmpty()) parseFlatJSON(body, q);

            // 令牌校验：header 或 ?token= 都行（浏览器直接打开方便）
            if (!token.isEmpty()) {
                String got = headers.getOrDefault("x-api-token", "");
                if (got.isEmpty()) got = q.getOrDefault("token", "");
                if (!token.equals(got)) {
                    Log.w("xflow-api", "拒绝未授权请求 " + path + " from " + sock.getInetAddress());
                    writeResponse(sock, 401,
                            "{\"ok\":false,\"err\":\"需要 X-API-Token\"}");
                    return;
                }
            }

            String out = route(method, path, q);
            writeResponse(sock, 200, out);
        } catch (Exception ignored) {
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
