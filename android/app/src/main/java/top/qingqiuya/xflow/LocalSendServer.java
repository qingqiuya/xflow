package top.qingqiuya.xflow;

import android.content.Context;
import android.net.wifi.WifiManager;
import android.util.Log;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.DatagramPacket;
import java.net.InetAddress;
import java.net.MulticastSocket;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URLDecoder;
import java.net.URLEncoder;
import java.util.HashMap;
import java.util.Map;

/**
 * LocalSend v2 协议兼容（手机端，接收方向）。
 *
 * 为什么：手机上装 LocalSend 的人比装我们 App 的多，按它的协议说话就能直接收文件。
 * 端口 53317，端点跟桌面端 localsend.go 一致：
 *   GET  /api/localsend/v2/info
 *   POST /api/localsend/v2/register
 *   POST /api/localsend/v2/prepare-upload
 *   POST /api/localsend/v2/upload?sessionId=&fileId=&token=
 *   POST /api/localsend/v2/cancel?sessionId=
 *
 * 只放行局域网来源；「自动接收」关着时 prepare-upload 直接 403（让对面重试）。
 * 落盘复用 Transfer.saveStream（存到 Download/XFlow），跟互传走同一条路。
 */
public class LocalSendServer {
    private static final String TAG = "xflow-ls";
    public static final int PORT = 53317;
    private static final String MCAST = "224.0.0.167";

    private final Context ctx;
    private ServerSocket server;
    private volatile boolean running;
    private MulticastSocket mcast;
    private Thread acceptThread, mcastThread;
    private WifiManager.MulticastLock lock;
    private final String fingerprint = Long.toHexString(System.nanoTime()) + "ls";

    private static class Session {
        String from = "";
        long created = System.currentTimeMillis();
        // fileId -> token
        final Map<String, String> tokens = new HashMap<>();
        // fileId -> 文件名
        final Map<String, String> names = new HashMap<>();
        // fileId -> 大小
        final Map<String, Long> sizes = new HashMap<>();
    }

    private final Map<String, Session> sessions = new HashMap<>();

    /** 组播里见过的 LocalSend 设备（给 SharePanel 显示用）。 */
    public static class LsPeer {
        public String name = "";
        public String ip = "";
        public int port = PORT;
        public long lastSeen = System.currentTimeMillis();
    }

    public static final java.util.List<LsPeer> PEERS = new java.util.ArrayList<>();

    private static void rememberPeer(String name, String ip, int port) {
        synchronized (PEERS) {
            long now = System.currentTimeMillis();
            PEERS.removeIf(p -> now - p.lastSeen > 120000);
            for (LsPeer p : PEERS) {
                if (p.ip.equals(ip)) {
                    p.name = name;
                    p.port = port;
                    p.lastSeen = now;
                    return;
                }
            }
            LsPeer p = new LsPeer();
            p.name = name;
            p.ip = ip;
            p.port = port;
            PEERS.add(p);
        }
    }

    public LocalSendServer(Context ctx) {
        this.ctx = ctx.getApplicationContext();
    }

    public synchronized void start() {
        if (running) return;
        try {
            server = new ServerSocket();
            server.setReuseAddress(true);
            server.bind(new java.net.InetSocketAddress(PORT));
        } catch (Exception e) {
            Log.w(TAG, "端口 " + PORT + " 起不来（可能装了真 LocalSend）：" + e.getMessage());
            return;
        }
        running = true;
        acceptThread = new Thread(this::acceptLoop, "ls-accept");
        acceptThread.start();
        startMulticast();
        Log.i(TAG, "LocalSend 兼容层已启动 :" + PORT);
    }

    public synchronized void stop() {
        running = false;
        try {
            if (server != null) server.close();
        } catch (Exception ignored) {
        }
        try {
            if (mcast != null) mcast.close();
        } catch (Exception ignored) {
        }
        releaseLock();
    }

    // ---------------------------------------------------------------- 组播

    private void startMulticast() {
        try {
            mcast = new MulticastSocket(PORT);
            mcast.setReuseAddress(true);
            InetAddress group = InetAddress.getByName(MCAST);
            mcast.joinGroup(group);
            acquireLock();
        } catch (Exception e) {
            Log.w(TAG, "组播起不来（别人发现不了这台手机）：" + e.getMessage());
            return;
        }
        mcastThread = new Thread(() -> {
            byte[] buf = new byte[64 * 1024];
            while (running) {
                try {
                    DatagramPacket p = new DatagramPacket(buf, buf.length);
                    mcast.receive(p);
                    String s = new String(p.getData(), 0, p.getLength(), "UTF-8");
                    JSONObject o = new JSONObject(s);
                    String fp = o.optString("fingerprint", "");
                    if (fp.isEmpty() || fp.equals(fingerprint)) continue;   // 自己喊的
                    if (!o.optBoolean("announce", false)) continue;
                    if (!isPrivateAddr(p.getAddress())) continue;
                    // 对方在喊：回一个 register，让它在设备列表里看到我们
                    final String ip = p.getAddress().getHostAddress();
                    final int port = o.optInt("port", PORT);
                    rememberPeer(o.optString("alias", ip), ip, port);
                    new Thread(() -> registerBack(ip, port), "ls-reg").start();
                } catch (Exception e) {
                    if (!running) return;
                }
            }
        }, "ls-mcast");
        mcastThread.start();

        // 每 30 秒喊一嗓子
        new Thread(() -> {
            while (running) {
                announce();
                try {
                    Thread.sleep(30000);
                } catch (InterruptedException e) {
                    return;
                }
            }
        }, "ls-announce").start();
    }

    private void acquireLock() {
        try {
            WifiManager wm = (WifiManager) ctx.getApplicationContext()
                    .getSystemService(Context.WIFI_SERVICE);
            if (wm != null) {
                lock = wm.createMulticastLock("xflow-ls");
                lock.setReferenceCounted(false);
                lock.acquire();
            }
        } catch (Exception ignored) {
        }
    }

    private void releaseLock() {
        try {
            if (lock != null && lock.isHeld()) lock.release();
        } catch (Exception ignored) {
        }
    }

    private void announce() {
        try {
            JSONObject o = selfJSON();
            o.put("announce", true);
            byte[] b = o.toString().getBytes("UTF-8");
            DatagramPacket p = new DatagramPacket(b, b.length,
                    InetAddress.getByName(MCAST), PORT);
            if (mcast != null) mcast.send(p);
        } catch (Exception ignored) {
        }
    }

    private void registerBack(String ip, int port) {
        try {
            JSONObject body = selfJSON();
            int code = httpJSON("http://" + ip + ":" + port + "/api/localsend/v2/register",
                    body.toString());
            if (code == 200) return;
        } catch (Exception ignored) {
        }
        // HTTP 不通就退回 UDP（协议允许）
        try {
            JSONObject o = selfJSON();
            o.put("announce", false);
            byte[] b = o.toString().getBytes("UTF-8");
            DatagramPacket p = new DatagramPacket(b, b.length, InetAddress.getByName(MCAST), PORT);
            if (mcast != null) mcast.send(p);
        } catch (Exception ignored) {
        }
    }

    private JSONObject selfJSON() throws Exception {
        JSONObject o = new JSONObject();
        o.put("alias", deviceAlias());
        o.put("version", "2.1");
        o.put("deviceModel", android.os.Build.MODEL == null ? "Android" : android.os.Build.MODEL);
        o.put("deviceType", "mobile");
        o.put("fingerprint", fingerprint);
        o.put("port", PORT);
        o.put("protocol", "http");
        o.put("download", false);
        return o;
    }

    private String deviceAlias() {
        String n = android.os.Build.MODEL;
        return (n == null || n.isEmpty()) ? "XFlow 手机" : "XFlow " + n;
    }

    // ---------------------------------------------------------------- HTTP

    private void acceptLoop() {
        while (running) {
            try {
                Socket s = server.accept();
                new Thread(() -> handle(s), "ls-conn").start();
            } catch (Exception e) {
                if (!running) return;
            }
        }
    }

    private void handle(Socket s) {
        try (Socket sock = s) {
            sock.setSoTimeout(60000);
            InputStream raw = sock.getInputStream();
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
                    if (e > 0) q.put(dec(kv.substring(0, e)), dec(kv.substring(e + 1)));
                }
            }

            if (!isPrivateAddr(sock.getInetAddress())) {
                write(sock, 403, "{\"err\":\"only private network\"}");
                return;
            }
            if (!shareEnabled()) {
                write(sock, 403, "{\"err\":\"互传已关闭\"}");
                return;
            }

            String base = "/api/localsend/v2";
            if (path.equals(base + "/info")) {
                write(sock, 200, selfJSON().toString());
                return;
            }
            if (path.equals(base + "/register")) {
                readBody(raw, len); // 内容我们不存，够用
                write(sock, 200, selfJSON().toString());
                return;
            }
            if (path.equals(base + "/prepare-upload")) {
                if (!autoAccept()) {
                    write(sock, 403, "{\"err\":\"接收方没开自动接收\"}");
                    return;
                }
                String body = readBody(raw, len);
                JSONObject o = new JSONObject(body);
                JSONObject files = o.optJSONObject("files");
                if (files == null || files.length() == 0) {
                    sock.getOutputStream().write("HTTP/1.1 204 No Content\r\n\r\n".getBytes("UTF-8"));
                    return;
                }
                Session se = new Session();
                JSONObject info = o.optJSONObject("info");
                if (info != null) se.from = info.optString("alias", "");
                synchronized (sessions) {
                    // 清掉十分钟前的
                    long now = System.currentTimeMillis();
                    sessions.entrySet().removeIf(en -> now - en.getValue().created > 600000);
                    for (java.util.Iterator<String> it = files.keys(); it.hasNext(); ) {
                        String id = it.next();
                        JSONObject f = files.optJSONObject(id);
                        if (f == null) continue;
                        se.tokens.put(id, Long.toHexString(System.nanoTime()));
                        se.names.put(id, f.optString("fileName", "file"));
                        se.sizes.put(id, f.optLong("size", 0));
                    }
                    String sid = Long.toHexString(System.nanoTime());
                    sessions.put(sid, se);
                    JSONObject resp = new JSONObject();
                    resp.put("sessionId", sid);
                    JSONObject tk = new JSONObject();
                    for (Map.Entry<String, String> e : se.tokens.entrySet()) tk.put(e.getKey(), e.getValue());
                    resp.put("files", tk);
                    write(sock, 200, resp.toString());
                }
                return;
            }
            if (path.equals(base + "/upload")) {
                String sid = q.getOrDefault("sessionId", "");
                String fid = q.getOrDefault("fileId", "");
                String tok = q.getOrDefault("token", "");
                Session se;
                synchronized (sessions) {
                    se = sessions.get(sid);
                }
                if (se == null || !tok.equals(se.tokens.get(fid))) {
                    write(sock, 403, "{\"err\":\"invalid token\"}");
                    return;
                }
                String name = se.names.getOrDefault(fid, "localsend-file");
                long size = se.sizes.containsKey(fid) ? se.sizes.get(fid) : len;
                String saved = Transfer.saveStream(ctx, raw, name, size);
                Log.i(TAG, "收到 " + name + " -> " + saved);
                write(sock, 200, "");
                return;
            }
            if (path.equals(base + "/cancel")) {
                synchronized (sessions) {
                    sessions.remove(q.getOrDefault("sessionId", ""));
                }
                write(sock, 200, "");
                return;
            }
            write(sock, 404, "{\"err\":\"not found\"}");
        } catch (Exception e) {
            Log.w(TAG, "handle 出错：" + e.getMessage());
        }
    }

    private String readBody(InputStream in, int len) throws Exception {
        if (len <= 0) return "";
        byte[] b = new byte[len];
        int off = 0;
        while (off < len) {
            int n = in.read(b, off, len - off);
            if (n <= 0) break;
            off += n;
        }
        return new String(b, 0, off, "UTF-8");
    }

    private static void write(Socket s, int code, String json) {
        try {
            String reason = code == 200 ? "OK" : code == 403 ? "Forbidden" : code == 404 ? "Not Found" : "Error";
            byte[] body = json == null ? new byte[0] : json.getBytes("UTF-8");
            StringBuilder sb = new StringBuilder();
            sb.append("HTTP/1.1 ").append(code).append(' ').append(reason).append("\r\n");
            sb.append("Content-Type: application/json; charset=utf-8\r\n");
            sb.append("Content-Length: ").append(body.length).append("\r\n");
            sb.append("Connection: close\r\n\r\n");
            OutputStream out = s.getOutputStream();
            out.write(sb.toString().getBytes("UTF-8"));
            out.write(body);
            out.flush();
        } catch (Exception ignored) {
        }
    }

    private static int httpJSON(String url, String json) throws Exception {
        java.net.HttpURLConnection c = (java.net.HttpURLConnection) new java.net.URL(url).openConnection();
        c.setConnectTimeout(3000);
        c.setReadTimeout(5000);
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setRequestProperty("Content-Type", "application/json");
        byte[] b = json.getBytes("UTF-8");
        c.setFixedLengthStreamingMode(b.length);
        try (OutputStream o = c.getOutputStream()) {
            o.write(b);
        }
        int code = c.getResponseCode();
        c.disconnect();
        return code;
    }

    // ---------------------------------------------------------------- 小工具

    private boolean shareEnabled() {
        return true; // 由 MainActivity 的互传开关控制 ApiServer；这里只在它开着时才会启动
    }

    private boolean autoAccept() {
        return ctx.getSharedPreferences("xflow_share", Context.MODE_PRIVATE)
                .getBoolean("auto_accept", true);
    }

    private static boolean isPrivateAddr(InetAddress a) {
        if (a == null) return false;
        if (a.isLoopbackAddress() || a.isSiteLocalAddress()) return true;
        byte[] b = a.getAddress();
        if (b.length == 4) {
            int f = b[0] & 0xFF, s = b[1] & 0xFF;
            if (f == 100 && s >= 64 && s <= 127) return true;
        }
        return false;
    }

    private static String readLine(InputStream in) throws Exception {
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        int c;
        while ((c = in.read()) != -1) {
            if (c == '\n') break;
            if (c != '\r') bos.write(c);
        }
        if (c == -1 && bos.size() == 0) return null;
        return new String(bos.toByteArray(), "UTF-8");
    }

    private static String dec(String s) {
        try {
            return URLDecoder.decode(s, "UTF-8");
        } catch (Exception e) {
            return s;
        }
    }

    @SuppressWarnings("unused")
    private static String enc(String s) {
        try {
            return URLEncoder.encode(s, "UTF-8");
        } catch (Exception e) {
            return s;
        }
    }

    /** 按 LocalSend v2 协议把文件推给对方（prepare-upload → upload）。返回 null 表示成功，否则是错误信息。 */
    public static String sendTo(String ip, int port, java.io.InputStream in, String name, long size) {
        try {
            String base = "http://" + ip + ":" + port + "/api/localsend/v2";
            String fileId = Long.toHexString(System.nanoTime());
            JSONObject meta = new JSONObject();
            meta.put("id", fileId);
            meta.put("fileName", name);
            meta.put("size", size);
            meta.put("fileType", "application/octet-stream");
            JSONObject files = new JSONObject();
            files.put(fileId, meta);
            JSONObject info = new JSONObject();
            info.put("alias", "XFlow 手机");
            info.put("version", "2.1");
            info.put("deviceModel", android.os.Build.MODEL == null ? "Android" : android.os.Build.MODEL);
            info.put("deviceType", "mobile");
            info.put("fingerprint", "xflow-mobile-" + Long.toHexString(System.nanoTime()));
            info.put("port", PORT);
            info.put("protocol", "http");
            JSONObject body = new JSONObject();
            body.put("info", info);
            body.put("files", files);

            java.net.HttpURLConnection c = (java.net.HttpURLConnection)
                    new java.net.URL(base + "/prepare-upload").openConnection();
            c.setRequestMethod("POST");
            c.setDoOutput(true);
            c.setConnectTimeout(8000);
            c.setReadTimeout(15000);
            c.setRequestProperty("Content-Type", "application/json");
            byte[] bb = body.toString().getBytes("UTF-8");
            c.setFixedLengthStreamingMode(bb.length);
            try (java.io.OutputStream o = c.getOutputStream()) {
                o.write(bb);
            }
            int code = c.getResponseCode();
            if (code != 200) {
                c.disconnect();
                return "对方拒绝（HTTP " + code + "）";
            }
            java.io.InputStream ri = c.getInputStream();
            ByteArrayOutputStream rb = new ByteArrayOutputStream();
            byte[] tmp = new byte[4096];
            int rn;
            while ((rn = ri.read(tmp)) > 0) rb.write(tmp, 0, rn);
            ri.close();
            c.disconnect();
            JSONObject resp = new JSONObject(new String(rb.toByteArray(), "UTF-8"));
            String sid = resp.optString("sessionId", "");
            JSONObject tk = resp.optJSONObject("files");
            String tok = tk == null ? "" : tk.optString(fileId, "");
            if (sid.isEmpty() || tok.isEmpty()) return "对方没给 session/token";

            String up = base + "/upload?sessionId=" + URLEncoder.encode(sid, "UTF-8")
                    + "&fileId=" + URLEncoder.encode(fileId, "UTF-8")
                    + "&token=" + URLEncoder.encode(tok, "UTF-8");
            java.net.HttpURLConnection c2 = (java.net.HttpURLConnection) new java.net.URL(up).openConnection();
            c2.setRequestMethod("POST");
            c2.setDoOutput(true);
            c2.setConnectTimeout(10000);
            c2.setReadTimeout(600000);
            c2.setRequestProperty("Content-Type", "application/octet-stream");
            if (size > 0) c2.setFixedLengthStreamingMode(size);
            else c2.setChunkedStreamingMode(64 * 1024);
            try (java.io.OutputStream out = c2.getOutputStream()) {
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
            }
            int code2 = c2.getResponseCode();
            c2.disconnect();
            return code2 == 200 ? null : ("上传失败（HTTP " + code2 + "）");
        } catch (Exception e) {
            return String.valueOf(e.getMessage());
        }
    }
}
