package top.qingqiuya.xflow;

import android.app.Activity;
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

    /** 统一深色壳：扫码 / 账号 两个 tab 在一个对话框里切（不加载 B 站页面）。 */
    public static void showLogin(final Activity a, final Runnable onSuccess) {
        final android.app.Dialog dlg = new android.app.Dialog(a, R.style.DarkDialog);
        android.view.View root = android.view.LayoutInflater.from(a)
                .inflate(R.layout.bili_login, null);
        dlg.setContentView(root);

        android.view.Window w = dlg.getWindow();
        if (w != null) {
            w.setBackgroundDrawable(new android.graphics.drawable.ColorDrawable(0));
            int wid = (int) (a.getResources().getDisplayMetrics().widthPixels * 0.92);
            w.setLayout(wid, ViewGroup.LayoutParams.WRAP_CONTENT);
        }

        final TextView tabQr = root.findViewById(R.id.tabQr);
        final TextView tabAcc = root.findViewById(R.id.tabAcc);
        final LinearLayout pageQr = root.findViewById(R.id.pageQr);
        final LinearLayout pageAcc = root.findViewById(R.id.pageAcc);
        final TextView qrTip = root.findViewById(R.id.qrTip);
        final ImageView qrImage = root.findViewById(R.id.qrImage);
        final TextView qrSave = root.findViewById(R.id.qrSave);
        final TextView close = root.findViewById(R.id.biliRowClose);
        final TextView logout = root.findViewById(R.id.biliLogout);

        final android.widget.EditText phone = root.findViewById(R.id.accPhone);
        final android.widget.EditText pwd = root.findViewById(R.id.accPwd);
        final android.widget.EditText code = root.findViewById(R.id.accCode);
        final LinearLayout smsRow = root.findViewById(R.id.smsRow);
        final TextView btnSend = root.findViewById(R.id.btnSendCode);
        final TextView btnLogin = root.findViewById(R.id.btnLogin);
        final TextView accTip = root.findViewById(R.id.accTip);
        final TextView modePwd = root.findViewById(R.id.modePwd);
        final TextView modeSms = root.findViewById(R.id.modeSms);

        if (loggedIn(a)) logout.setVisibility(android.view.View.VISIBLE);
        close.setOnClickListener(v -> dlg.dismiss());
        logout.setOnClickListener(v -> {
            logout(a);
            Toast.makeText(a, "已退出 B 站登录", Toast.LENGTH_SHORT).show();
            dlg.dismiss();
            if (onSuccess != null) onSuccess.run();
        });

        // ---------------- 扫码 tab
        final boolean[] stop = {false};
        final String[] key = {null};
        final Bitmap[] bmp = {null};
        final Runnable loadQr = () -> {
            if (key[0] != null) return;
            new Thread(() -> {
                try {
                    String gen = BiliVideo.httpGet(
                            "https://passport.bilibili.com/x/passport-login/web/qrcode/generate", null);
                    org.json.JSONObject o = new org.json.JSONObject(gen);
                    org.json.JSONObject data = o.optJSONObject("data");
                    final String url = data == null ? "" : data.optString("url", "");
                    key[0] = data == null ? "" : data.optString("qrcode_key", "");
                    if (url.isEmpty() || key[0].isEmpty()) {
                        a.runOnUiThread(() -> qrTip.setText("生成二维码失败，换个网络再试"));
                        return;
                    }
                    final Bitmap b = qrBitmap(url, 640);
                    bmp[0] = b;
                    a.runOnUiThread(() -> {
                        qrImage.setImageBitmap(b);
                        qrTip.setText("等待扫码…");
                        qrSave.setEnabled(true);
                        qrSave.setAlpha(1f);
                    });
                    while (!stop[0]) {
                        Thread.sleep(2500);
                        String pollUrl = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=" + key[0];
                        String r = BiliVideo.httpGet(pollUrl, null);
                        org.json.JSONObject po = new org.json.JSONObject(r);
                        org.json.JSONObject pd = po.optJSONObject("data");
                        int st = pd == null ? -1 : pd.optInt("code", -1);
                        if (st == 86101) {
                            a.runOnUiThread(() -> qrTip.setText("等待扫码…"));
                        } else if (st == 86090) {
                            a.runOnUiThread(() -> qrTip.setText("已扫码，请在手机上确认"));
                        } else if (st == 0) {
                            stop[0] = true;
                            String back = pd.optString("url", "");
                            save(a, qs(back, "SESSDATA"), qs(back, "bili_jct"),
                                    qs(back, "DedeUserID"));
                            a.runOnUiThread(() -> {
                                Toast.makeText(a, "登录成功", Toast.LENGTH_SHORT).show();
                                dlg.dismiss();
                                if (onSuccess != null) onSuccess.run();
                            });
                            return;
                        } else if (st == 86038) {
                            stop[0] = true;
                            a.runOnUiThread(() -> qrTip.setText("二维码过期，重开一次"));
                            return;
                        }
                    }
                } catch (Exception e) {
                    Log.w(TAG, "二维码登录失败", e);
                    a.runOnUiThread(() -> qrTip.setText("出错：" + e.getMessage()));
                }
            }).start();
        };

        qrSave.setOnClickListener(v -> {
            if (bmp[0] == null) return;
            String p = saveToGallery(a, bmp[0]);
            Toast.makeText(a, p != null ? "已存到相册：" + p : "存相册失败",
                    Toast.LENGTH_SHORT).show();
        });

        // ---------------- 账号 tab（自己画的表单）
        final boolean[] smsMode = {false};
        final BiliAccount.Captcha[] cap = {null};      // 一次验证复用：发码 / 登录
        final String[] captchaKey = {""};
        final String[] verified = {null, null, null};  // validate, seccode, challenge

        final Runnable applyMode = () -> {
            boolean sms = smsMode[0];
            pwd.setVisibility(sms ? android.view.View.GONE : android.view.View.VISIBLE);
            smsRow.setVisibility(sms ? android.view.View.VISIBLE : android.view.View.GONE);
            modePwd.setTextColor(sms ? 0xFF6b7488 : 0xFF4CC2FF);
            modeSms.setTextColor(sms ? 0xFF4CC2FF : 0xFF6b7488);
            accTip.setText(sms ? "先过一次极验滑块，才能收到短信" : "密码登录需要过一次极验滑块");
        };
        modePwd.setOnClickListener(v -> {
            smsMode[0] = false;
            applyMode.run();
        });
        modeSms.setOnClickListener(v -> {
            smsMode[0] = true;
            applyMode.run();
        });

        // 极验滑块：只在这时候弹，页面是我们自己的壳 + 极验的验证码，不出现 B 站页面
        final java.util.function.Consumer<Runnable> runCaptcha = (after) -> {
            new Thread(() -> {
                try {
                    BiliAccount.Captcha c = BiliAccount.getCaptcha();
                    cap[0] = c;
                    a.runOnUiThread(() -> showGeetest(a, c, (v, s, ch) -> {
                        verified[0] = v;
                        verified[1] = s;
                        verified[2] = ch;
                        captchaKey[0] = c.token;
                        accTip.setText("验证通过");
                        if (after != null) after.run();
                    }));
                } catch (Exception e) {
                    a.runOnUiThread(() -> accTip.setText("拿验证参数失败：" + e.getMessage()));
                }
            }).start();
        };

        btnSend.setOnClickListener(v -> {
            final String tel = phone.getText().toString().trim();
            if (tel.length() < 6) {
                accTip.setText("先填手机号");
                return;
            }
            accTip.setText("正在获取验证…");
            runCaptcha.accept(() -> new Thread(() -> {
                BiliAccount.Result r = BiliAccount.sendSms(tel, 86, cap[0],
                        verified[0], verified[1], verified[2]);
                a.runOnUiThread(() -> accTip.setText(r.ok ? "验证码已发送" : "发送失败：" + r.err));
            }).start());
        });

        btnLogin.setOnClickListener(v -> {
            final String tel = phone.getText().toString().trim();
            if (tel.isEmpty()) {
                accTip.setText("先填手机号");
                return;
            }
            accTip.setText("正在获取验证…");
            runCaptcha.accept(() -> new Thread(() -> {
                BiliAccount.Result r;
                if (smsMode[0]) {
                    String cd = code.getText().toString().trim();
                    r = BiliAccount.loginBySms(tel, 86, cd, captchaKey[0]);
                } else {
                    String pw = pwd.getText().toString();
                    if (pw.isEmpty()) {
                        a.runOnUiThread(() -> accTip.setText("先填密码"));
                        return;
                    }
                    r = BiliAccount.loginByPassword(tel, pw, cap[0],
                            verified[0], verified[1], verified[2]);
                }
                final BiliAccount.Result rr = r;
                a.runOnUiThread(() -> {
                    if (rr.ok) {
                        save(a, rr.sessdata, rr.biliJct, rr.uid);
                        Toast.makeText(a, "登录成功", Toast.LENGTH_SHORT).show();
                        dlg.dismiss();
                        if (onSuccess != null) onSuccess.run();
                    } else {
                        accTip.setText("失败：" + rr.err);
                    }
                });
            }).start());
        });

        // tab 切换
        tabQr.setOnClickListener(v -> {
            pageQr.setVisibility(android.view.View.VISIBLE);
            pageAcc.setVisibility(android.view.View.GONE);
            tabQr.setTextColor(0xFF4CC2FF);
            tabAcc.setTextColor(0xFF6b7488);
            loadQr.run();
        });
        tabAcc.setOnClickListener(v -> {
            pageQr.setVisibility(android.view.View.GONE);
            pageAcc.setVisibility(android.view.View.VISIBLE);
            tabAcc.setTextColor(0xFF4CC2FF);
            tabQr.setTextColor(0xFF6b7488);
        });

        applyMode.run();
        tabQr.performClick();
        dlg.setOnDismissListener(x -> stop[0] = true);
        dlg.show();
    }

    interface OnGeetest {
        void ok(String validate, String seccode, String challenge);
    }

    /** 弹极验滑块（WebView 加载我们自己的壳页面 + 极验 JS）。 */
    private static void showGeetest(Activity a, BiliAccount.Captcha c, OnGeetest cb) {
        final WebView wv = new WebView(a);
        WebSettings st = wv.getSettings();
        st.setJavaScriptEnabled(true);
        st.setDomStorageEnabled(true);
        st.setUserAgentString(UA);
        wv.setBackgroundColor(0xFF12151C);
        wv.setWebChromeClient(new android.webkit.WebChromeClient() {
            @Override
            public boolean onConsoleMessage(android.webkit.ConsoleMessage m) {
                Log.i(TAG, "geetest console: " + m.message() + " @" + m.lineNumber());
                return true;
            }
        });
        wv.setWebViewClient(new android.webkit.WebViewClient() {
            @Override
            public void onPageFinished(WebView v, String url) {
                Log.i(TAG, "geetest page finished: " + url);
            }

            @Override
            public void onReceivedError(WebView v, android.webkit.WebResourceRequest r,
                                        android.webkit.WebResourceError e) {
                Log.w(TAG, "geetest error: " + r.getUrl() + " " + e.getDescription());
            }
        });

        final android.app.Dialog d = new android.app.Dialog(a, R.style.DarkDialog);
        wv.addJavascriptInterface(new Object() {
            @android.webkit.JavascriptInterface
            public void onValidate(String v, String s, String ch) {
                a.runOnUiThread(() -> {
                    try {
                        d.dismiss();
                    } catch (Throwable ignored) {
                    }
                    cb.ok(v, s, ch);
                });
            }

            @android.webkit.JavascriptInterface
            public void onError(String msg) {
                a.runOnUiThread(() -> Toast.makeText(a, msg, Toast.LENGTH_SHORT).show());
            }
        }, "AndroidBridge");

        int hpx = (int) (a.getResources().getDisplayMetrics().heightPixels * 0.62);
        d.setContentView(wv, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, hpx));
        android.view.Window w = d.getWindow();
        if (w != null) {
            w.setBackgroundDrawable(new android.graphics.drawable.ColorDrawable(0xFF12151C));
            int wid = (int) (a.getResources().getDisplayMetrics().widthPixels * 0.92);
            w.setLayout(wid, ViewGroup.LayoutParams.WRAP_CONTENT);
        }
        // file:// 页面加载不了 https 的极验脚本（null origin + 混合内容），
        // 所以把 HTML 读出来，用 bilibili 域名当 base 走 loadDataWithBaseURL。
        try {
            java.io.InputStream in = a.getAssets().open("geetest.html");
            java.io.ByteArrayOutputStream bo = new java.io.ByteArrayOutputStream();
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) bo.write(buf, 0, n);
            in.close();
            String html = new String(bo.toByteArray(), "UTF-8")
                    .replace("__GT__", c.gt)
                    .replace("__CHALLENGE__", c.challenge);
            wv.getSettings().setMixedContentMode(
                    WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            wv.loadDataWithBaseURL("https://www.bilibili.com/", html,
                    "text/html", "UTF-8", null);
        } catch (Exception e) {
            Log.w(TAG, "读极验页面失败", e);
        }
        d.show();
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
