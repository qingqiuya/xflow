package top.qingqiuya.xflow;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.database.Cursor;
import android.graphics.Color;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.text.InputType;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * 互传页面：设备 / 收件箱 / 剪贴板 / 发文件。
 *
 * 设备来源两路：mDNS 自动发现（Beacon.PEERS）+ 手动填地址（公网用）。
 * 局域网内的请求免令牌；公网的会被要求令牌，第一次 401 时让你输一次，按地址记住。
 */
public class ShareActivity extends Activity {

    private static final int REQ_FILE = 3101;

    private final Handler ui = new Handler(Looper.getMainLooper());
    private SharedPreferences sp;
    private Beacon.Peer target;
    private final List<Beacon.Peer> manual = new ArrayList<>();

    private LinearLayout devicesBox, inboxBox;
    private TextView targetText, status, selfTag;
    private EditText addrInput, textInput;
    private String lastDevSig = "";

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        setContentView(R.layout.share);
        sp = getSharedPreferences("xflow_share", Context.MODE_PRIVATE);

        devicesBox = findViewById(R.id.devicesBox);
        inboxBox = findViewById(R.id.inboxBox);
        targetText = findViewById(R.id.targetText);
        status = findViewById(R.id.status);
        selfTag = findViewById(R.id.selfTag);
        addrInput = findViewById(R.id.addrInput);
        textInput = findViewById(R.id.textInput);

        findViewById(R.id.backBtn).setOnClickListener(v -> finish());
        findViewById(R.id.addrAdd).setOnClickListener(v -> addManual());
        findViewById(R.id.pickFile).setOnClickListener(v -> pickFile());
        findViewById(R.id.clipSend).setOnClickListener(v -> sendMyClipboard());
        findViewById(R.id.clipGet).setOnClickListener(v -> getPeerClipboard());
        findViewById(R.id.textSend).setOnClickListener(v -> {
            String t = textInput.getText().toString().trim();
            if (t.isEmpty()) return;
            postClipboard(t, true);
        });

        loadManual();
        selfTag.setText("本机 " + localIpTag());
        refreshDevices();
        loadInbox();
    }

    // ---------------------------------------------------------------- 设备

    private void loadManual() {
        manual.clear();
        String raw = sp.getString("manual_addrs", "");
        for (String s : raw.split("\n")) {
            s = s.trim();
            if (s.isEmpty()) continue;
            Beacon.Peer p = parseAddr(s);
            if (p != null) manual.add(p);
        }
    }

    private void saveManual() {
        StringBuilder sb = new StringBuilder();
        for (Beacon.Peer p : manual) sb.append(p.ip).append(":").append(p.port).append("\n");
        sp.edit().putString("manual_addrs", sb.toString()).apply();
    }

    private void addManual() {
        String s = addrInput.getText().toString().trim();
        if (s.isEmpty()) return;
        Beacon.Peer p = parseAddr(s);
        if (p == null) {
            say("地址格式不对，写成 [240e:…]:17777 或 1.2.3.4:17777");
            return;
        }
        for (Beacon.Peer x : manual) {
            if (x.ip.equals(p.ip) && x.port == p.port) {
                say("已经加过了");
                return;
            }
        }
        manual.add(p);
        saveManual();
        addrInput.setText("");
        refreshDevices();
        say("已添加 " + p.ip + ":" + p.port);
    }

    /** 支持 [v6]:port、ip:port、裸 v6（默认 17777）。 */
    private Beacon.Peer parseAddr(String s) {
        String ip;
        int port = 17777;
        if (s.startsWith("[")) {
            int e = s.indexOf(']');
            if (e < 0) return null;
            ip = s.substring(1, e);
            int c = s.indexOf(':', e);
            if (c > 0) {
                try {
                    port = Integer.parseInt(s.substring(c + 1).trim());
                } catch (Exception ignored) {
                }
            }
        } else if (s.chars().filter(ch -> ch == ':').count() > 1) {
            ip = s; // 裸 v6
        } else {
            int c = s.lastIndexOf(':');
            if (c < 0) return null;
            ip = s.substring(0, c);
            try {
                port = Integer.parseInt(s.substring(c + 1).trim());
            } catch (Exception e) {
                return null;
            }
        }
        if (ip.isEmpty() || port <= 0 || port > 65535) return null;
        Beacon.Peer p = new Beacon.Peer();
        p.ip = ip;
        p.port = port;
        p.name = "手动 " + ip;
        p.platform = "manual";
        return p;
    }

    private void refreshDevices() {
        Runnable[] tick = new Runnable[1];
        tick[0] = () -> {
            List<Beacon.Peer> list = new ArrayList<>();
            synchronized (Beacon.PEERS) {
                list.addAll(Beacon.PEERS.values());
            }
            list.addAll(manual);
            StringBuilder sig = new StringBuilder();
            for (Beacon.Peer p : list) sig.append(p.ip).append(':').append(p.port).append(',');
            if (!sig.toString().equals(lastDevSig)) {
                lastDevSig = sig.toString();
                renderDevices(list);
            }
            ui.postDelayed(tick[0], 1500);
        };
        ui.postDelayed(tick[0], 300);
    }

    private void renderDevices(List<Beacon.Peer> list) {
        devicesBox.removeAllViews();
        if (list.isEmpty()) {
            TextView t = new TextView(this);
            t.setText("正在搜寻…（同一 WiFi 里开一台 XFlow 就会出现在这儿；公网请手动加地址）");
            t.setTextColor(Color.parseColor("#8b93a7"));
            t.setTextSize(12.5f);
            devicesBox.addView(t);
            return;
        }
        for (Beacon.Peer p : list) {
            boolean on = target != null && target.ip.equals(p.ip) && target.port == p.port;
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(14), dp(12), dp(14), dp(12));
            GradientDrawable bg = new GradientDrawable();
            bg.setCornerRadius(dp(12));
            bg.setColor(on ? 0xFF1d2c3d : 0xFF141b26);
            bg.setStroke(dp(1), on ? 0xFF3d7fae : 0xFF232936);
            row.setBackground(bg);
            ViewGroup.MarginLayoutParams lp = new ViewGroup.MarginLayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(8);
            row.setLayoutParams(lp);

            LinearLayout texts = new LinearLayout(this);
            texts.setOrientation(LinearLayout.VERTICAL);
            texts.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f));
            TextView n = new TextView(this);
            n.setText(p.name);
            n.setTextColor(Color.parseColor("#e8ebf2"));
            n.setTextSize(14f);
            TextView a = new TextView(this);
            a.setText(p.ip + ":" + p.port + ("manual".equals(p.platform) ? "　手动" : ""));
            a.setTextColor(Color.parseColor("#8b93a7"));
            a.setTextSize(11.5f);
            texts.addView(n);
            texts.addView(a);
            row.addView(texts);

            TextView tag = new TextView(this);
            tag.setText(on ? "已选" : "选它");
            tag.setTextColor(on ? Color.parseColor("#3ddc84") : Color.parseColor("#7fd4ff"));
            tag.setTextSize(12.5f);
            row.addView(tag);

            row.setOnClickListener(v -> {
                target = p;
                targetText.setText(p.name + "\n" + p.ip + ":" + p.port);
                renderDevices(list);
                say("");
            });
            row.setOnLongClickListener(v -> {
                if ("manual".equals(p.platform)) {
                    manual.remove(p);
                    saveManual();
                    if (target == p) target = null;
                    refreshDevices();
                    say("已移除手动设备");
                }
                return true;
            });
            devicesBox.addView(row);
        }
    }

    // ---------------------------------------------------------------- 收件箱

    private void loadInbox() {
        new Thread(() -> {
            String json = http("GET", "http://127.0.0.1:" + ApiServer.DEFAULT_PORT + "/api/files", null, false);
            List<String[]> items = new ArrayList<>();
            try {
                JSONObject o = new JSONObject(json);
                JSONArray arr = o.optJSONArray("items");
                if (arr != null) {
                    for (int i = 0; i < arr.length(); i++) {
                        JSONObject it = arr.getJSONObject(i);
                        items.add(new String[]{it.optString("name"), String.valueOf(it.optLong("size")),
                                it.optString("uri")});
                    }
                }
            } catch (Exception ignored) {
            }
            ui.post(() -> renderInbox(items));
        }).start();
    }

    private void renderInbox(List<String[]> items) {
        inboxBox.removeAllViews();
        if (items.isEmpty()) {
            TextView t = new TextView(this);
            t.setText("还没有收到的文件");
            t.setTextColor(Color.parseColor("#8b93a7"));
            t.setTextSize(12.5f);
            inboxBox.addView(t);
            return;
        }
        for (String[] it : items) {
            LinearLayout row = new LinearLayout(this);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(14), dp(11), dp(14), dp(11));
            GradientDrawable bg = new GradientDrawable();
            bg.setCornerRadius(dp(12));
            bg.setColor(0xFF141b26);
            bg.setStroke(dp(1), 0xFF232936);
            row.setBackground(bg);
            ViewGroup.MarginLayoutParams lp = new ViewGroup.MarginLayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(8);
            row.setLayoutParams(lp);

            LinearLayout texts = new LinearLayout(this);
            texts.setOrientation(LinearLayout.VERTICAL);
            texts.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f));
            TextView n = new TextView(this);
            n.setText(it[0]);
            n.setTextColor(Color.parseColor("#e8ebf2"));
            n.setTextSize(13.5f);
            TextView sz = new TextView(this);
            sz.setText(human(Long.parseLong(it[1])));
            sz.setTextColor(Color.parseColor("#8b93a7"));
            sz.setTextSize(11.5f);
            texts.addView(n);
            texts.addView(sz);
            row.addView(texts);

            final String uri = it[2];
            TextView open = new TextView(this);
            open.setText("打开");
            open.setTextColor(Color.parseColor("#7fd4ff"));
            open.setTextSize(12.5f);
            open.setPadding(dp(10), dp(6), dp(10), dp(6));
            open.setOnClickListener(v -> {
                try {
                    Intent i = new Intent(Intent.ACTION_VIEW);
                    i.setDataAndType(Uri.parse(uri), "*/*");
                    i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    startActivity(i);
                } catch (Exception e) {
                    say("打不开：" + e.getMessage());
                }
            });
            row.addView(open);

            TextView send = new TextView(this);
            send.setText("发走");
            send.setTextColor(Color.parseColor("#7fd4ff"));
            send.setTextSize(12.5f);
            send.setPadding(dp(10), dp(6), dp(4), dp(6));
            send.setOnClickListener(v -> {
                if (target == null) {
                    say("先在上面选一台设备");
                    return;
                }
                final Beacon.Peer t = target;
                final Uri u = Uri.parse(uri);
                new Thread(() -> uploadFromUri(u, it[0], querySizeOf(u), t)).start();
            });
            row.addView(send);
            inboxBox.addView(row);
        }
    }

    // ---------------------------------------------------------------- 动作

    private void pickFile() {
        if (target == null) {
            say("先在上面选一台设备");
            return;
        }
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        startActivityForResult(i, REQ_FILE);
    }

    @Override
    protected void onActivityResult(int req, int res, Intent data) {
        super.onActivityResult(req, res, data);
        if (req != REQ_FILE || res != RESULT_OK || data == null || data.getData() == null) return;
        final Uri uri = data.getData();
        final String name = queryName(uri);
        final long size = querySize(uri);
        final Beacon.Peer t = target;
        say("上传中：" + name);
        new Thread(() -> uploadFromUri(uri, name, size, t)).start();
    }

    /** 把 content:// 或 file:// 的流直接推到目标设备（选文件/收件箱转手都用它）。 */
    private void uploadFromUri(Uri uri, String name, long size, Beacon.Peer t) {
        try {
            String url = "http://" + hostOf(t) + ":" + t.port + "/api/upload";
            HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
            c.setRequestMethod("POST");
            c.setDoOutput(true);
            c.setConnectTimeout(10000);
            c.setReadTimeout(300000);
            String tok = tokOf(t.ip);
            if (!tok.isEmpty()) c.setRequestProperty("X-API-Token", tok);
            c.setRequestProperty("X-File-Name", name);
            c.setRequestProperty("Content-Type", "application/octet-stream");
            if (size > 0) c.setFixedLengthStreamingMode(size);
            else c.setChunkedStreamingMode(64 * 1024);
            try (OutputStream out = c.getOutputStream(); InputStream in = getContentResolver().openInputStream(uri)) {
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
            }
            int code = c.getResponseCode();
            c.disconnect();
            ui.post(() -> say(code == 200 ? ("已发送：" + name + " → " + t.name) : ("失败：对面返回 " + code)));
        } catch (Exception e) {
            ui.post(() -> say("失败：" + e.getMessage()));
        }
    }

    private long querySizeOf(Uri uri) {
        return querySize(uri);
    }

    private void sendMyClipboard() {
        if (target == null) {
            say("先选一台设备");
            return;
        }
        String t = "";
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
            ClipData cp = cm.getPrimaryClip();
            if (cp != null && cp.getItemCount() > 0) t = String.valueOf(cp.getItemAt(0).coerceToText(this));
        } catch (Exception ignored) {
        }
        if (t.isEmpty()) {
            say("本机剪贴板是空的");
            return;
        }
        postClipboard(t, false);
    }

    private void postClipboard(String text, boolean clearInput) {
        if (target == null) {
            say("先选一台设备");
            return;
        }
        final Beacon.Peer t = target;
        new Thread(() -> {
            try {
                String url = "http://" + hostOf(t) + ":" + t.port + "/api/clipboard";
                String body = "{\"text\":\"" + jsonEsc(text) + "\"}";
                int code = httpCode("POST", url, body, tokOf(t.ip));
                ui.post(() -> {
                    say(code == 200 ? ("已发送 " + text.length() + " 字") : ("失败：对面返回 " + code));
                    if (code == 200 && clearInput) textInput.setText("");
                });
            } catch (Exception e) {
                ui.post(() -> say("失败：" + e.getMessage()));
            }
        }).start();
    }

    private void getPeerClipboard() {
        if (target == null) {
            say("先选一台设备");
            return;
        }
        final Beacon.Peer t = target;
        new Thread(() -> {
            try {
                String url = "http://" + hostOf(t) + ":" + t.port + "/api/clipboard";
                String json = http("GET", url, null, false, tokOf(t.ip));
                String text = "";
                try {
                    text = new JSONObject(json).optString("text");
                } catch (Exception ignored) {
                }
                final String f = text;
                ui.post(() -> {
                    if (f.isEmpty()) {
                        say("对方剪贴板是空的（或系统不让我读）");
                        return;
                    }
                    try {
                        ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
                        cm.setPrimaryClip(ClipData.newPlainText("XFlow", f));
                        say("已复制到本机剪贴板（" + f.length() + " 字）");
                    } catch (Exception e) {
                        say("取到了但复制失败：" + e.getMessage());
                    }
                });
            } catch (Exception e) {
                ui.post(() -> say("失败：" + e.getMessage()));
            }
        }).start();
    }

    // ---------------------------------------------------------------- 工具

    private String tokOf(String ip) {
        return sp.getString("tok_" + ip, "");
    }

    /** v6 地址要加方括号。 */
    private String hostOf(Beacon.Peer p) {
        return p.ip.contains(":") ? ("[" + p.ip + "]") : p.ip;
    }

    private String localIpTag() {
        try {
            java.util.Enumeration<java.net.NetworkInterface> es = java.net.NetworkInterface.getNetworkInterfaces();
            while (es.hasMoreElements()) {
                java.net.NetworkInterface ni = es.nextElement();
                java.util.Enumeration<java.net.InetAddress> as = ni.getInetAddresses();
                while (as.hasMoreElements()) {
                    java.net.InetAddress a = as.nextElement();
                    if (!a.isLoopbackAddress() && a instanceof java.net.Inet4Address) {
                        return a.getHostAddress();
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    private int httpCode(String method, String url, String body, String tok) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
            c.setRequestMethod(method);
            c.setConnectTimeout(8000);
            c.setReadTimeout(20000);
            if (tok != null && !tok.isEmpty()) c.setRequestProperty("X-API-Token", tok);
            if (body != null) {
                c.setDoOutput(true);
                c.setRequestProperty("Content-Type", "application/json; charset=utf-8");
                try (OutputStream o = c.getOutputStream()) {
                    o.write(body.getBytes("UTF-8"));
                }
            }
            int code = c.getResponseCode();
            c.disconnect();
            return code;
        } catch (Exception e) {
            return -1;
        }
    }

    private String http(String method, String url, String body, boolean ignored) {
        return http(method, url, body, ignored, "");
    }

    private String http(String method, String url, String body, boolean ignored, String tok) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
            c.setRequestMethod(method);
            c.setConnectTimeout(8000);
            c.setReadTimeout(30000);
            if (tok != null && !tok.isEmpty()) c.setRequestProperty("X-API-Token", tok);
            if (body != null) {
                c.setDoOutput(true);
                c.setRequestProperty("Content-Type", "application/json; charset=utf-8");
                try (OutputStream o = c.getOutputStream()) {
                    o.write(body.getBytes("UTF-8"));
                }
            }
            int code = c.getResponseCode();
            if (code != 200) {
                c.disconnect();
                return "{\"ok\":false,\"code\":" + code + "}";
            }
            StringBuilder sb = new StringBuilder();
            try (InputStream in = c.getInputStream()) {
                byte[] buf = new byte[8192];
                int n;
                while ((n = in.read(buf)) > 0) sb.append(new String(buf, 0, n, "UTF-8"));
            }
            c.disconnect();
            return sb.toString();
        } catch (Exception e) {
            return "{\"ok\":false,\"err\":\"" + e.getMessage() + "\"}";
        }
    }

    private String queryName(Uri uri) {
        try (Cursor c = getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (i >= 0 && c.getString(i) != null) return c.getString(i);
            }
        } catch (Exception ignored) {
        }
        String p = uri.getLastPathSegment();
        return p == null ? "xflow-upload" : p;
    }

    private long querySize(Uri uri) {
        try (Cursor c = getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.SIZE);
                if (i >= 0 && !c.isNull(i)) return c.getLong(i);
            }
        } catch (Exception ignored) {
        }
        return -1;
    }

    private static String human(long n) {
        if (n <= 0) return "0 B";
        String[] u = {"B", "KB", "MB", "GB", "TB"};
        int i = 0;
        double d = n;
        while (d >= 1024 && i < u.length - 1) {
            d /= 1024;
            i++;
        }
        return String.format(i == 0 ? "%.0f %s" : "%.1f %s", d, u[i]);
    }

    private static String jsonEsc(String s) {
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < s.length(); i++) {
            char ch = s.charAt(i);
            switch (ch) {
                case '"':
                    b.append("\\\"");
                    break;
                case '\\':
                    b.append("\\\\");
                    break;
                case '\n':
                    b.append("\\n");
                    break;
                case '\r':
                    b.append("\\r");
                    break;
                case '\t':
                    b.append("\\t");
                    break;
                default:
                    if (ch < 0x20) b.append(String.format("\\u%04x", (int) ch));
                    else b.append(ch);
            }
        }
        return b.toString();
    }

    private void say(String s) {
        status.setText(s);
        if (!s.isEmpty()) Toast.makeText(this, s, Toast.LENGTH_SHORT).show();
    }

    private int dp(int v) {
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v, getResources().getDisplayMetrics());
    }
}
