package top.qingqiuya.xflow;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ContentValues;
import android.content.Context;
import android.content.SharedPreferences;
import android.graphics.Bitmap;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.MediaStore;
import android.util.Log;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import com.google.zxing.BarcodeFormat;
import com.google.zxing.EncodeHintType;
import com.google.zxing.common.BitMatrix;
import com.google.zxing.qrcode.QRCodeWriter;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;
import java.util.HashMap;
import java.util.Map;

/**
 * B 站登录：拿 SESSDATA。
 *
 * 两条路：
 *   1. 内置网页登录（短信/密码，登录完从 CookieManager 直接取）
 *   2. 二维码登录（生成码 → 手机 B 站 App 扫 → 轮询拿 Cookie）
 *      —— 手机自己扫自己不方便，所以顺手支持「存相册」再去 App 里选图扫。
 *
 * Cookie 存 SharedPreferences（App 私有），只在请求 B 站接口时作为 Cookie 头带上。
 */
public class BiliLogin {

    private static final String PREF = "bili_login";
    private static final String K_SESSDATA = "sessdata";
    private static final String K_JCT = "bili_jct";
    private static final String K_UID = "dedeuserid";
    private static final String TAG = "xflow-bili";

    private static final String UA =
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36";

    private static SharedPreferences pref(Context c) {
        return c.getSharedPreferences(PREF, Context.MODE_PRIVATE);
    }

    public static String sessdata(Context c) {
        return pref(c).getString(K_SESSDATA, "");
    }

    public static boolean loggedIn(Context c) {
        String s = sessdata(c);
        return s != null && !s.isEmpty();
    }

    /** 给请求用的 Cookie 头；没登录返回 null。 */
    public static String cookieHeader(Context c) {
        String s = sessdata(c);
        return (s == null || s.isEmpty()) ? null : "SESSDATA=" + s;
    }

    static void save(Context c, String sessdata, String jct, String uid) {
        SharedPreferences.Editor e = pref(c).edit();
        if (sessdata != null && !sessdata.isEmpty()) e.putString(K_SESSDATA, sessdata);
        if (jct != null && !jct.isEmpty()) e.putString(K_JCT, jct);
        if (uid != null && !uid.isEmpty()) e.putString(K_UID, uid);
        e.apply();
    }

    public static void logout(Context c) {
        pref(c).edit().clear().apply();
        try {
            CookieManager.getInstance().removeAllCookies(null);
        } catch (Throwable ignored) {
        }
    }

    // ------------------------------------------------------------ 入口

    /** 弹一个「怎么登录」的选择框。 */
    public static void showLogin(final Activity a, final Runnable onSuccess) {
        String[] items = {
                "网页登录（推荐，短信验证码）",
                "二维码（存相册后用 B 站 App 扫）",
                loggedIn(a) ? "退出登录" : null
        };
        final String[] finalItems = java.util.Arrays.stream(items)
                .filter(x -> x != null).toArray(String[]::new);
        new AlertDialog.Builder(a, R.style.DarkDialog)
                .setTitle(loggedIn(a) ? "B 站帐号（已登录）" : "登录 B 站")
                .setItems(finalItems, (d, which) -> {
                    String pick = finalItems[which];
                    if (pick.startsWith("网页登录")) webLogin(a, onSuccess);
                    else if (pick.startsWith("二维码")) qrLogin(a, onSuccess);
                    else {
                        logout(a);
                        Toast.makeText(a, "已退出", Toast.LENGTH_SHORT).show();
                        onSuccess.run();
                    }
                })
                .setNegativeButton("取消", null)
                .show();
    }

    // ------------------------------------------------------------ 网页登录

    private static void webLogin(final Activity a, final Runnable onSuccess) {
        final WebView wv = new WebView(a);
        WebSettings st = wv.getSettings();
        st.setJavaScriptEnabled(true);
        st.setDomStorageEnabled(true);
        st.setUserAgentString(UA);   // 用桌面 UA，移动版登录页滑块更勤
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(wv, true);

        LinearLayout box = new LinearLayout(a);
        box.setOrientation(LinearLayout.VERTICAL);
        box.addView(wv, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(a, 520)));

        final AlertDialog dlg = new AlertDialog.Builder(a, R.style.DarkDialog)
                .setTitle("登录 B 站")
                .setView(box)
                .setNegativeButton("关闭", null)
                .create();
        dlg.show();
        wv.loadUrl("https://passport.bilibili.com/login");

        final Handler h = new Handler(Looper.getMainLooper());
        final boolean[] done = {false};
        final Runnable check = new Runnable() {
            @Override
            public void run() {
                if (done[0]) return;
                String ck = null;
                try {
                    ck = CookieManager.getInstance().getCookie("https://www.bilibili.com");
                    if (ck == null || !ck.contains("SESSDATA=")) {
                        String ck2 = CookieManager.getInstance().getCookie("https://passport.bilibili.com");
                        if (ck2 != null && ck2.contains("SESSDATA=")) ck = ck2;
                    }
                } catch (Throwable ignored) {
                }
                if (ck != null && ck.contains("SESSDATA=")) {
                    done[0] = true;
                    String sess = pick(ck, "SESSDATA");
                    String jct = pick(ck, "bili_jct");
                    String uid = pick(ck, "DedeUserID");
                    save(a, sess, jct, uid);
                    try {
                        dlg.dismiss();
                    } catch (Throwable ignored) {
                    }
                    Toast.makeText(a, "登录成功", Toast.LENGTH_SHORT).show();
                    if (onSuccess != null) onSuccess.run();
                    return;
                }
                h.postDelayed(this, 1200);
            }
        };
        h.postDelayed(check, 1500);
        dlg.setOnDismissListener(d -> {
            done[0] = true;
            h.removeCallbacksAndMessages(null);
            wv.destroy();
        });
    }

    // ------------------------------------------------------------ 二维码登录

    private static void qrLogin(final Activity a, final Runnable onSuccess) {
        final LinearLayout box = new LinearLayout(a);
        box.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(a, 16);
        box.setPadding(pad, pad, pad, pad);

        final TextView tip = new TextView(a);
        tip.setText("正在生成二维码…");
        tip.setTextColor(0xFFDDE3F0);
        box.addView(tip);

        final ImageView iv = new ImageView(a);
        LinearLayout.LayoutParams ip = new LinearLayout.LayoutParams(dp(a, 260), dp(a, 260));
        ip.topMargin = dp(a, 12);
        box.addView(iv, ip);

        final Button save = new Button(a);
        save.setText("存到相册（用 B 站 App 扫相册）");
        save.setEnabled(false);
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        bp.topMargin = dp(a, 12);
        box.addView(save, bp);

        final AlertDialog dlg = new AlertDialog.Builder(a, R.style.DarkDialog)
                .setTitle("扫码/相册码登录")
                .setView(box)
                .setNegativeButton("关闭", null)
                .create();
        dlg.show();

        final Handler h = new Handler(Looper.getMainLooper());
        final boolean[] stop = {false};
        final Bitmap[] bmp = new Bitmap[]{null};
        final String[] key = new String[]{null};

        save.setOnClickListener(v -> {
            if (bmp[0] == null) return;
            String p = saveToGallery(a, bmp[0]);
            Toast.makeText(a, p != null ? "已存到相册：" + p : "存相册失败",
                    Toast.LENGTH_SHORT).show();
        });

        new Thread(() -> {
            try {
                String gen = BiliVideo.httpGet(
                        "https://passport.bilibili.com/x/passport-login/web/qrcode/generate", null);
                org.json.JSONObject o = new org.json.JSONObject(gen);
                final String url = o.optJSONObject("data").optString("url", "");
                key[0] = o.optJSONObject("data").optString("qrcode_key", "");
                if (url.isEmpty() || key[0].isEmpty()) {
                    h.post(() -> tip.setText("生成二维码失败"));
                    return;
                }
                final Bitmap b = qrBitmap(url, 640);
                bmp[0] = b;
                h.post(() -> {
                    iv.setImageBitmap(b);
                    tip.setText("用 B 站 App 扫这张码；同机扫不了就点下面存相册");
                    save.setEnabled(true);
                });
                // 轮询
                while (!stop[0]) {
                    Thread.sleep(2500);
                    String pollUrl = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=" + key[0];
                    String r = BiliVideo.httpGet(pollUrl, null);
                    org.json.JSONObject po = new org.json.JSONObject(r);
                    org.json.JSONObject pd = po.optJSONObject("data");
                    int st = pd == null ? -1 : pd.optInt("code", -1);
                    if (st == 86101) {
                        h.post(() -> tip.setText("等待扫码…"));
                    } else if (st == 86090) {
                        h.post(() -> tip.setText("已扫码，请在手机上确认"));
                    } else if (st == 0) {
                        stop[0] = true;
                        String back = pd.optString("url", "");
                        String sess = qs(back, "SESSDATA");
                        String jct = qs(back, "bili_jct");
                        String uid = qs(back, "DedeUserID");
                        save(a, sess, jct, uid);
                        h.post(() -> {
                            try {
                                dlg.dismiss();
                            } catch (Throwable ignored) {
                            }
                            Toast.makeText(a, "登录成功", Toast.LENGTH_SHORT).show();
                            if (onSuccess != null) onSuccess.run();
                        });
                        return;
                    } else if (st == 86038) {
                        stop[0] = true;
                        h.post(() -> tip.setText("二维码已过期，关掉重开"));
                        return;
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "二维码登录失败", e);
                h.post(() -> tip.setText("出错：" + e.getMessage()));
            }
        }).start();

        dlg.setOnDismissListener(d -> stop[0] = true);
    }

    private static Bitmap qrBitmap(String text, int size) {
        try {
            Map<EncodeHintType, Object> hints = new HashMap<>();
            hints.put(EncodeHintType.MARGIN, 1);
            hints.put(EncodeHintType.CHARACTER_SET, "UTF-8");
            BitMatrix m = new QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, size, size, hints);
            int[] px = new int[m.getWidth() * m.getHeight()];
            for (int y = 0; y < m.getHeight(); y++) {
                for (int x = 0; x < m.getWidth(); x++) {
                    px[y * m.getWidth() + x] = m.get(x, y) ? Color.BLACK : Color.WHITE;
                }
            }
            Bitmap b = Bitmap.createBitmap(m.getWidth(), m.getHeight(), Bitmap.Config.ARGB_8888);
            b.setPixels(px, 0, m.getWidth(), 0, 0, m.getWidth(), m.getHeight());
            return b;
        } catch (Exception e) {
            return null;
        }
    }

    private static String saveToGallery(Context c, Bitmap b) {
        String name = "xflow-bili-qr-" + System.currentTimeMillis() + ".png";
        try {
            if (Build.VERSION.SDK_INT >= 29) {
                ContentValues cv = new ContentValues();
                cv.put(MediaStore.MediaColumns.DISPLAY_NAME, name);
                cv.put(MediaStore.MediaColumns.MIME_TYPE, "image/png");
                cv.put(MediaStore.MediaColumns.RELATIVE_PATH,
                        Environment.DIRECTORY_PICTURES + "/XFlow");
                Uri uri = c.getContentResolver().insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, cv);
                if (uri == null) return null;
                try (OutputStream os = c.getContentResolver().openOutputStream(uri)) {
                    b.compress(Bitmap.CompressFormat.PNG, 100, os);
                }
                return "图片/XFlow/" + name;
            }
            File dir = new File(Environment.getExternalStoragePublicDirectory(
                    Environment.DIRECTORY_PICTURES), "XFlow");
            dir.mkdirs();
            File f = new File(dir, name);
            try (FileOutputStream os = new FileOutputStream(f)) {
                b.compress(Bitmap.CompressFormat.PNG, 100, os);
            }
            return f.getAbsolutePath();
        } catch (Exception e) {
            Log.w(TAG, "存相册失败", e);
            return null;
        }
    }

    // ------------------------------------------------------------ 小工具

    /** 从 "a=1; b=2" 里取值 */
    private static String pick(String cookie, String key) {
        for (String part : cookie.split(";")) {
            String t = part.trim();
            if (t.startsWith(key + "=")) return t.substring(key.length() + 1);
        }
        return "";
    }

    /** 从 URL 查询串里取值（扫码成功后返回的那个 url 带参数） */
    private static String qs(String url, String key) {
        try {
            int i = url.indexOf('?');
            if (i < 0) return "";
            for (String part : url.substring(i + 1).split("&")) {
                int e = part.indexOf('=');
                if (e < 0) continue;
                if (part.substring(0, e).equals(key)) {
                    return java.net.URLDecoder.decode(part.substring(e + 1), "UTF-8");
                }
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    private static int dp(Context c, int v) {
        return (int) (v * c.getResources().getDisplayMetrics().density + 0.5f);
    }
}
