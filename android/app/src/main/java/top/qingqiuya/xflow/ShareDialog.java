package top.qingqiuya.xflow;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.database.Cursor;
import android.graphics.Color;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.text.InputType;
import android.util.TypedValue;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.ArrayList;
import java.util.List;

/**
 * 手机端「互传」面板：选设备 → 发文件 / 发剪贴板 / 取剪贴板。
 *
 * 走的是桌面版同一套接口（/api/upload、/api/clipboard），
 * 收件由对面决定存哪，这边只管发。
 */
public class ShareDialog {

    private static final int REQ_PICK_FILE = 2101;
    private static Activity activityRef;
    private static String pendIp = "";
    private static int pendPort = 0;
    private static String pendTok = "";
    private static TextView statusRef;
    private static final Handler UI = new Handler(Looper.getMainLooper());

    public static void show(Activity act) {
        activityRef = act;
        List<Beacon.Peer> peers = new ArrayList<>(Beacon.PEERS.values());
        if (peers.isEmpty()) {
            new AlertDialog.Builder(act)
                    .setTitle("互传")
                    .setMessage("还没发现别的设备。\n\n同一局域网里另开一台 XFlow（电脑或另一台手机）就会出现。")
                    .setPositiveButton("知道了", null)
                    .show();
            return;
        }
        String[] names = new String[peers.size()];
        for (int i = 0; i < peers.size(); i++) {
            Beacon.Peer p = peers.get(i);
            names[i] = p.name + "\n" + p.ip + ":" + p.port;
        }
        new AlertDialog.Builder(act)
                .setTitle("发给哪台设备")
                .setItems(names, (d, which) -> openPanel(act, peers.get(which)))
                .setNegativeButton("取消", null)
                .show();
    }

    private static void openPanel(Activity act, Beacon.Peer peer) {
        LinearLayout box = new LinearLayout(act);
        box.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(act, 18);
        box.setPadding(pad, pad, pad, pad);
        box.setBackgroundColor(Color.parseColor("#12151c"));

        TextView title = new TextView(act);
        title.setText(peer.name + "  " + peer.ip + ":" + peer.port);
        title.setTextColor(Color.parseColor("#e8ebf2"));
        title.setTextSize(15);
        box.addView(title);

        EditText tok = new EditText(act);
        tok.setHint("令牌（不用填 —— 互传已在局域网内免校验；只有对方是老版本才要）");
        tok.setTextSize(13);
        tok.setSingleLine(true);
        tok.setInputType(InputType.TYPE_CLASS_TEXT);
        tok.setText(prefs(act).getString("tok_" + peer.ip, ""));
        box.addView(tok);

        statusRef = new TextView(act);
        statusRef.setTextColor(Color.parseColor("#ffc857"));
        statusRef.setTextSize(12);
        statusRef.setText("");
        box.addView(statusRef);

        Button sendFile = mkButton(act, "发送文件…");
        Button sendClip = mkButton(act, "发送本机剪贴板");
        Button getClip = mkButton(act, "取对方剪贴板");
        box.addView(sendFile);
        box.addView(sendClip);
        box.addView(getClip);

        AlertDialog dlg = new AlertDialog.Builder(act)
                .setView(box)
                .setNegativeButton("关闭", null)
                .create();

        // 令牌随时记
        pendIp = peer.ip;
        pendPort = peer.port;
        sendFile.setOnClickListener(v -> {
            pendTok = tok.getText().toString().trim();
            prefs(act).edit().putString("tok_" + peer.ip, pendTok).apply();
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            i.addCategory(Intent.CATEGORY_OPENABLE);
            i.setType("*/*");
            act.startActivityForResult(i, REQ_PICK_FILE);
        });
        sendClip.setOnClickListener(v -> {
            pendTok = tok.getText().toString().trim();
            prefs(act).edit().putString("tok_" + peer.ip, pendTok).apply();
            ClipboardManager cm = (ClipboardManager) act.getSystemService(Context.CLIPBOARD_SERVICE);
            String t = "";
            try {
                ClipData cp = cm.getPrimaryClip();
                if (cp != null && cp.getItemCount() > 0) {
                    t = String.valueOf(cp.getItemAt(0).coerceToText(act));
                }
            } catch (Exception ignored) {
            }
            if (t.isEmpty()) {
                say("本机剪贴板是空的（或系统不让读）");
                return;
            }
            postClipboard(t);
        });
        getClip.setOnClickListener(v -> {
            pendTok = tok.getText().toString().trim();
            prefs(act).edit().putString("tok_" + peer.ip, pendTok).apply();
            getClipboard();
        });
        dlg.show();
        // 面板宽度撑开一点
        if (dlg.getWindow() != null) {
            dlg.getWindow().setLayout((int) (act.getResources().getDisplayMetrics().widthPixels * 0.92f),
                    ViewGroup.LayoutParams.WRAP_CONTENT);
        }
    }

    // ---------------------------------------------------------- 实际动作

    /** MainActivity.onActivityResult 转发过来。 */
    public static void onPickResult(int requestCode, int resultCode, Intent data) {
        if (requestCode != REQ_PICK_FILE || resultCode != Activity.RESULT_OK || data == null) return;
        Uri uri = data.getData();
        if (uri == null || activityRef == null) return;
        String name = queryName(activityRef, uri);
        say("上传中：" + name);
        new Thread(() -> {
            try {
                long size = querySize(activityRef, uri);
                HttpURLConnection c = (HttpURLConnection)
                        new URL("http://" + pendIp + ":" + pendPort + "/api/upload").openConnection();
                c.setRequestMethod("POST");
                c.setDoOutput(true);
                c.setConnectTimeout(10000);
                c.setReadTimeout(300000);
                if (!pendTok.isEmpty()) c.setRequestProperty("X-API-Token", pendTok);
                c.setRequestProperty("X-File-Name", name);
                c.setRequestProperty("Content-Type", "application/octet-stream");
                if (size > 0) c.setFixedLengthStreamingMode(size);
                else c.setChunkedStreamingMode(64 * 1024);
                try (OutputStream out = c.getOutputStream();
                     InputStream in = activityRef.getContentResolver().openInputStream(uri)) {
                    byte[] buf = new byte[64 * 1024];
                    int n;
                    while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
                }
                int code = c.getResponseCode();
                if (code == 200) say("已发送：" + name);
                else say("失败：对面返回 " + code);
                c.disconnect();
            } catch (Exception e) {
                say("失败：" + e.getMessage());
            }
        }).start();
    }

    private static void postClipboard(String text) {
        new Thread(() -> {
            try {
                HttpURLConnection c = (HttpURLConnection)
                        new URL("http://" + pendIp + ":" + pendPort + "/api/clipboard").openConnection();
                c.setRequestMethod("POST");
                c.setDoOutput(true);
                c.setConnectTimeout(8000);
                c.setReadTimeout(15000);
                if (!pendTok.isEmpty()) c.setRequestProperty("X-API-Token", pendTok);
                c.setRequestProperty("Content-Type", "application/json; charset=utf-8");
                String body = "{\"text\":\"" + jsonEsc(text) + "\"}";
                try (OutputStream out = c.getOutputStream()) {
                    out.write(body.getBytes("UTF-8"));
                }
                int code = c.getResponseCode();
                say(code == 200 ? ("已发送剪贴板（" + text.length() + " 字）") : ("失败：对面返回 " + code));
                c.disconnect();
            } catch (Exception e) {
                say("失败：" + e.getMessage());
            }
        }).start();
    }

    private static void getClipboard() {
        new Thread(() -> {
            try {
                HttpURLConnection c = (HttpURLConnection)
                        new URL("http://" + pendIp + ":" + pendPort + "/api/clipboard").openConnection();
                c.setConnectTimeout(8000);
                c.setReadTimeout(15000);
                if (!pendTok.isEmpty()) c.setRequestProperty("X-API-Token", pendTok);
                int code = c.getResponseCode();
                if (code != 200) {
                    say("失败：对面返回 " + code);
                    return;
                }
                StringBuilder sb = new StringBuilder();
                try (InputStream in = c.getInputStream()) {
                    byte[] buf = new byte[8192];
                    int n;
                    while ((n = in.read(buf)) > 0) sb.append(new String(buf, 0, n, "UTF-8"));
                }
                c.disconnect();
                String json = sb.toString();
                String text = extractJsonString(json, "text");
                if (activityRef == null) return;
                ClipboardManager cm = (ClipboardManager) activityRef.getSystemService(Context.CLIPBOARD_SERVICE);
                final String t = text;
                UI.post(() -> {
                    try {
                        cm.setPrimaryClip(ClipData.newPlainText("XFlow", t));
                        say("已取到并复制（" + t.length() + " 字）");
                    } catch (Exception e) {
                        say("取到了，但复制失败：" + e.getMessage());
                    }
                });
            } catch (Exception e) {
                say("失败：" + e.getMessage());
            }
        }).start();
    }

    // ---------------------------------------------------------- 小工具

    private static SharedPreferences prefs(Context c) {
        return c.getSharedPreferences("xflow_share", Context.MODE_PRIVATE);
    }

    private static void say(String s) {
        UI.post(() -> {
            if (statusRef != null) statusRef.setText(s);
        });
        UI.post(() -> {
            if (activityRef != null) Toast.makeText(activityRef, s, Toast.LENGTH_SHORT).show();
        });
    }

    private static Button mkButton(Activity act, String label) {
        Button b = new Button(act);
        b.setText(label);
        b.setAllCaps(false);
        b.setTextSize(14);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(act, 8);
        b.setLayoutParams(lp);
        return b;
    }

    private static int dp(Activity act, int v) {
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v,
                act.getResources().getDisplayMetrics());
    }

    private static String queryName(Activity act, Uri uri) {
        try (Cursor c = act.getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (i >= 0) {
                    String s = c.getString(i);
                    if (s != null && !s.isEmpty()) return s;
                }
            }
        } catch (Exception ignored) {
        }
        String p = uri.getLastPathSegment();
        return p == null ? "xflow-upload" : p;
    }

    private static long querySize(Activity act, Uri uri) {
        try (Cursor c = act.getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.SIZE);
                if (i >= 0 && !c.isNull(i)) return c.getLong(i);
            }
        } catch (Exception ignored) {
        }
        return -1;
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

    /** 从扁平 JSON 里抠一个字符串字段（不引 gson）。 */
    private static String extractJsonString(String json, String key) {
        String pat = "\"" + key + "\":\"";
        int i = json.indexOf(pat);
        if (i < 0) return "";
        i += pat.length();
        StringBuilder b = new StringBuilder();
        while (i < json.length()) {
            char ch = json.charAt(i);
            if (ch == '\\' && i + 1 < json.length()) {
                char nx = json.charAt(i + 1);
                switch (nx) {
                    case 'n':
                        b.append('\n');
                        break;
                    case 'r':
                        b.append('\r');
                        break;
                    case 't':
                        b.append('\t');
                        break;
                    default:
                        b.append(nx);
                }
                i += 2;
                continue;
            }
            if (ch == '"') break;
            b.append(ch);
            i++;
        }
        return b.toString();
    }
}
