package top.qingqiuya.xflow;

import android.content.Context;
import android.util.Log;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.security.KeyFactory;
import java.security.PublicKey;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;

import javax.crypto.Cipher;

/**
 * B 站账号登录（原生表单，不显示 B 站页面）。
 *
 * 流程：
 *   1. GET /x/passport-login/captcha  → token + 极验 gt/challenge
 *   2. 极验 WebView 滑一下             → validate + seccode
 *   3. 密码登录：GET /web/key 拿 RSA 公钥，加密 hash+密码
 *      短信登录：POST /web/sms/send 发码 → POST /web/login/sms 校验
 *   4. 成功后 Set-Cookie 里就是 SESSDATA
 */
public class BiliAccount {

    private static final String TAG = "xflow-bili";
    private static final String UA =
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36";
    private static final String BASE = "https://passport.bilibili.com";

    /** 极验参数（一次验证用一次） */
    public static class Captcha {
        public String token = "";
        public String gt = "";
        public String challenge = "";
    }

    /** 登录结果 */
    public static class Result {
        public boolean ok;
        public String err = "";
        public String sessdata = "";
        public String biliJct = "";
        public String uid = "";
        public String name = "";
    }

    // ------------------------------------------------------------ 步骤 1

    public static Captcha getCaptcha() throws Exception {
        String body = httpGet(BASE + "/x/passport-login/captcha?source=main_web");
        JSONObject o = new JSONObject(body);
        JSONObject d = o.optJSONObject("data");
        if (d == null) throw new Exception("拿不到验证参数");
        Captcha c = new Captcha();
        c.token = d.optString("token", "");
        JSONObject gt = d.optJSONObject("geetest");
        if (gt != null) {
            c.gt = gt.optString("gt", "");
            c.challenge = gt.optString("challenge", "");
        }
        if (c.gt.isEmpty() || c.challenge.isEmpty()) throw new Exception("极验参数缺失");
        return c;
    }

    // ------------------------------------------------------------ 步骤 3a：短信

    /** 发短信验证码。 */
    public static Result sendSms(String tel, int cid, Captcha cap,
                                 String validate, String seccode, String challenge) {
        Result r = new Result();
        try {
            String body = httpPost(BASE + "/x/passport-login/web/sms/send", form(
                    "cid", String.valueOf(cid),
                    "tel", tel,
                    "source", "main_web",
                    "token", cap.token,
                    "challenge", challenge,
                    "validate", validate,
                    "seccode", seccode));
            JSONObject o = new JSONObject(body);
            r.ok = o.optInt("code", -1) == 0;
            if (!r.ok) r.err = o.optString("message", "发送失败");
            return r;
        } catch (Exception e) {
            r.err = String.valueOf(e.getMessage());
            return r;
        }
    }

    /** 短信登录。 */
    public static Result loginBySms(String tel, int cid, String code, String captchaKey) {
        Result r = new Result();
        try {
            String body = httpPost(BASE + "/x/passport-login/web/login/sms", form(
                    "cid", String.valueOf(cid),
                    "tel", tel,
                    "code", code,
                    "captcha_key", captchaKey,
                    "source", "main_web"));
            r = parseLogin(body);
            return r;
        } catch (Exception e) {
            r.err = String.valueOf(e.getMessage());
            return r;
        }
    }

    // ------------------------------------------------------------ 步骤 3b：密码

    public static Result loginByPassword(String user, String pass, Captcha cap,
                                         String validate, String seccode, String challenge) {
        Result r = new Result();
        try {
            String kb = httpGet(BASE + "/x/passport-login/web/key");
            JSONObject ko = new JSONObject(kb);
            JSONObject kd = ko.optJSONObject("data");
            if (kd == null) throw new Exception("拿不到加密公钥");
            String hash = kd.optString("hash", "");
            String pem = kd.optString("key", "");
            String enc = rsaEncrypt(hash + pass, pem);

            String body = httpPost(BASE + "/x/passport-login/web/login", form(
                    "username", user,
                    "password", enc,
                    "token", cap.token,
                    "challenge", challenge,
                    "validate", validate,
                    "seccode", seccode,
                    "source", "main_web"));
            return parseLogin(body);
        } catch (Exception e) {
            r.err = String.valueOf(e.getMessage());
            return r;
        }
    }

    // ------------------------------------------------------------ 工具

    private static Result parseLogin(String body) throws Exception {
        Result r = new Result();
        JSONObject o = new JSONObject(body);
        JSONObject d = o.optJSONObject("data");
        int code = o.optInt("code", -1);
        // 登录成功时 code 可能是 0（有时 success 在 data.status）
        int status = d == null ? -1 : d.optInt("status", -1);
        if (code != 0) {
            r.err = o.optString("message", "登录失败 code=" + code);
            JSONObject dd = o.optJSONObject("data");
            if (dd != null && dd.has("message")) r.err = dd.optString("message", r.err);
            return r;
        }
        if (status != 0 && d != null && d.has("status")) {
            r.err = "登录未完成（status=" + status + "）";
            return r;
        }
        if (d != null) {
            // 密码登录成功时，SESSDATA 在 data.url / data 的回跳参数里
            String back = d.optString("url", "");
            String sess = qs(back, "SESSDATA");
            if (sess.isEmpty()) sess = d.optString("sessdata", "");
            String jct = qs(back, "bili_jct");
            String uid = qs(back, "DedeUserID");
            r.sessdata = sess;
            r.biliJct = jct;
            r.uid = uid;
        }
        r.ok = r.sessdata != null && !r.sessdata.isEmpty();
        if (!r.ok) r.err = "没有拿到 SESSDATA";
        return r;
    }

    static String rsaEncrypt(String text, String pem) throws Exception {
        String body = pem.replace("-----BEGIN PUBLIC KEY-----", "")
                .replace("-----END PUBLIC KEY-----", "")
                .replaceAll("\\s", "");
        byte[] der = Base64.getDecoder().decode(body);
        PublicKey key = KeyFactory.getInstance("RSA")
                .generatePublic(new X509EncodedKeySpec(der));
        Cipher cipher = Cipher.getInstance("RSA/ECB/PKCS1Padding");
        cipher.init(Cipher.ENCRYPT_MODE, key);
        byte[] out = cipher.doFinal(text.getBytes("UTF-8"));
        return Base64.getEncoder().encodeToString(out);
    }

    private static String form(String... kv) throws Exception {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i + 1 < kv.length; i += 2) {
            if (sb.length() > 0) sb.append('&');
            sb.append(URLEncoder.encode(kv[i], "UTF-8")).append('=')
              .append(URLEncoder.encode(kv[i + 1] == null ? "" : kv[i + 1], "UTF-8"));
        }
        return sb.toString();
    }

    private static String httpGet(String url) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setRequestProperty("User-Agent", UA);
        c.setRequestProperty("Referer", "https://www.bilibili.com/");
        c.setConnectTimeout(12000);
        c.setReadTimeout(15000);
        return read(c, "GET", null);
    }

    private static String httpPost(String url, String body) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setRequestProperty("User-Agent", UA);
        c.setRequestProperty("Referer", "https://www.bilibili.com/");
        c.setRequestProperty("Content-Type", "application/x-www-form-urlencoded");
        c.setConnectTimeout(12000);
        c.setReadTimeout(15000);
        return read(c, "POST", body);
    }

    private static String read(HttpURLConnection c, String method, String body) throws Exception {
        if ("POST".equals(method) && body != null) {
            try (OutputStream os = c.getOutputStream()) {
                os.write(body.getBytes("UTF-8"));
            }
        }
        int code = c.getResponseCode();
        StringBuilder sb = new StringBuilder();
        try (BufferedReader br = new BufferedReader(new InputStreamReader(
                code >= 400 ? c.getErrorStream() : c.getInputStream(), "UTF-8"))) {
            String line;
            while ((line = br.readLine()) != null) sb.append(line);
        }
        c.disconnect();
        return sb.toString();
    }

    private static String qs(String url, String key) {
        try {
            int i = url.indexOf('?');
            if (i < 0) i = -1;
            String q = i >= 0 ? url.substring(i + 1) : url;
            for (String part : q.split("&")) {
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
}
