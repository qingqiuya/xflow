package top.qingqiuya.xflow;

import android.content.ContentValues;
import android.content.Context;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.MediaStore;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;

import java.util.ArrayList;
import java.util.List;

/**
 * 手机端 B 站视频下载：view → playurl → 多候选探测 → 双流下载 → ffmpeg 合并。
 *
 * 跟桌面版（bilibili.go）同一套逻辑：
 * B 站 playurl 会给 baseUrl + 若干 backupUrl，挨个探一遍挑最快的，
 * 这样就不会卡在 mcdn 那个 P2P 节点上。
 */
public class BiliVideo {

    public interface Cb {
        void onTitle(String title);

        void onProgress(long done, long total);

        void onState(String state); // downloading / merging / done

        void onError(String msg);
    }

    private static final String UA =
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36";
    private static final String REFER = "https://www.bilibili.com/";
    private static final String TAG = "xflow-bili";

    /** 当前任务用的 Cookie 头（登录后带 SESSDATA，能拿 720P/1080P）。 */
    static volatile String COOKIE = null;

    public static boolean isBiliUrl(String s) {
        if (s == null) return false;
        String low = s.trim().toLowerCase();
        if (low.contains("bilibili.com/video/") || low.contains("b23.tv/")) return true;
        // 直接贴 BV 号也认（BV1GJ411x7h7 / bv1gj411x7h7 都行）
        return low.matches("bv[0-9a-z]{10}");
    }

    public static void run(Context ctx, String url, File dir, Cb cb) {
        try {
            COOKIE = BiliLogin.cookieHeader(ctx);
            String bvid = resolveBvid(url);
            if (bvid == null || bvid.isEmpty()) {
                cb.onError("认不出 BV 号");
                return;
            }
            JSONObject view = new JSONObject(getJson(
                    "https://api.bilibili.com/x/web-interface/view?bvid=" + bvid));
            if (view.optInt("code", -1) != 0) {
                cb.onError("B 站返回 " + view.optInt("code") + "：" + view.optString("message"));
                return;
            }
            JSONObject vd = view.optJSONObject("data");
            if (vd == null) {
                cb.onError("取不到视频信息");
                return;
            }
            String title = vd.optString("title", bvid).trim();
            long cid = vd.optLong("cid", 0);
            if (cid == 0) {
                cb.onError("取不到 cid");
                return;
            }
            cb.onTitle(title);

            // 没登录只能拿 480P（qn=32），登录后请求 1080P（qn=112）
            int qn = (COOKIE != null && !COOKIE.isEmpty()) ? 112 : 32;
            String pu = "https://api.bilibili.com/x/player/playurl?bvid=" + bvid
                    + "&cid=" + cid + "&qn=" + qn + "&fnval=16&fourk=1";
            JSONObject play = new JSONObject(getJson(pu));
            if (play.optInt("code", -1) != 0) {
                cb.onError("playurl 返回 " + play.optInt("code") + "：" + play.optString("message"));
                return;
            }
            JSONObject dash = play.optJSONObject("data") == null ? null
                    : play.optJSONObject("data").optJSONObject("dash");
            if (dash == null) {
                cb.onError("拿不到 dash 流（可能要登录/Cookie）");
                return;
            }
            JSONObject v = pickVideo(dash.optJSONArray("video"));
            JSONObject a = pickAudio(dash.optJSONArray("audio"));
            if (v == null || a == null) {
                cb.onError("dash 里没有可用的音视频流");
                return;
            }

            if (dir == null || !dir.exists()) dir = ctx.getExternalFilesDir(null);
            if (dir == null) dir = ctx.getFilesDir();
            File tmpDir = new File(ctx.getCacheDir(), "bili");
            tmpDir.mkdirs();
            String safe = sanitize(title);
            File vFile = new File(tmpDir, safe + ".video.m4s");
            File aFile = new File(tmpDir, safe + ".audio.m4s");
            File merged = new File(tmpDir, safe + ".mp4");

            String vUrl = probeFastest(candidates(v));
            String aUrl = probeFastest(candidates(a));
            if (vUrl == null || aUrl == null) {
                cb.onError("所有 CDN 候选都探不通");
                return;
            }

            long vLen = contentLength(vUrl);
            long aLen = contentLength(aUrl);
            final long[] prog = new long[]{0, (vLen > 0 && aLen > 0) ? vLen + aLen : 0};
            cb.onState("downloading");
            cb.onProgress(0, prog[1]);

            download(vUrl, vFile, prog, cb);
            download(aUrl, aFile, prog, cb);

            cb.onState("merging");
            File ff = ensureFfmpeg(ctx);
            if (ff == null) {
                cb.onError("解压 ffmpeg 失败");
                return;
            }
            if (!merge(ff, vFile, aFile, merged)) {
                cb.onError("合并失败");
                return;
            }
            // 落到系统「下载/XFlow」里，文件管理器能直接看到（私有目录太深）
            String pub = publishToDownloads(ctx, merged, safe + ".mp4");
            vFile.delete();
            aFile.delete();
            if (pub == null) {
                // 公共目录写不进去就退回私有目录，至少别把这个视频丢了
                File out = new File(dir, safe + ".mp4");
                if (out.exists()) out.delete();
                if (!merged.renameTo(out)) {
                    copyFile(merged, out);
                    merged.delete();
                }
                cb.onProgress(out.length(), out.length());
                cb.onState("done:" + out.getAbsolutePath());
                return;
            }
            long sz = merged.length();   // 先取大小，删掉之后再取就是 0 了
            merged.delete();
            cb.onProgress(sz, sz);
            cb.onState("done:" + pub);
        } catch (Exception e) {
            Log.w(TAG, "下载出错", e);
            cb.onError(String.valueOf(e.getMessage()));
        }
    }

    // ------------------------------------------------------------ 解析

    private static String resolveBvid(String url) {
        String u = url;
        if (!u.matches("(?i).*BV[0-9A-Za-z]{10}.*") && u.toLowerCase().contains("b23.tv/")) {
            try {
                HttpURLConnection c = (HttpURLConnection) new URL(u).openConnection();
                c.setInstanceFollowRedirects(false);
                c.setRequestProperty("User-Agent", UA);
                c.setConnectTimeout(10000);
                c.setReadTimeout(10000);
                String loc = c.getHeaderField("Location");
                c.disconnect();
                if (loc != null) u = loc;
            } catch (Exception ignored) {
            }
        }
        java.util.regex.Matcher m =
                java.util.regex.Pattern.compile("(BV[0-9A-Za-z]{10})").matcher(u);
        return m.find() ? m.group(1) : "";
    }

    private static void setHeaders(HttpURLConnection c) {
        c.setRequestProperty("User-Agent", UA);
        c.setRequestProperty("Referer", REFER);
        String ck = COOKIE;
        if (ck != null && !ck.isEmpty()) c.setRequestProperty("Cookie", ck);
    }

    /** 给 BiliLogin 扫码用：返回 body，同时把 Set-Cookie 头也带出来。 */
    static String[] httpGetWithCookie(String url) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        setHeaders(c);
        c.setConnectTimeout(12000);
        c.setReadTimeout(15000);
        if (c.getResponseCode() != 200) {
            int code = c.getResponseCode();
            c.disconnect();
            throw new Exception("HTTP " + code);
        }
        StringBuilder sb = new StringBuilder();
        try (BufferedReader br = new BufferedReader(new InputStreamReader(c.getInputStream(), "UTF-8"))) {
            String line;
            while ((line = br.readLine()) != null) sb.append(line);
        }
        StringBuilder ck = new StringBuilder();
        java.util.Map<String, java.util.List<String>> hs = c.getHeaderFields();
        for (java.util.Map.Entry<String, java.util.List<String>> e : hs.entrySet()) {
            if (e.getKey() != null && e.getKey().equalsIgnoreCase("Set-Cookie")) {
                for (String v : e.getValue()) {
                    if (v == null) continue;
                    if (ck.length() > 0) ck.append("; ");
                    ck.append(v.split(";")[0]);
                }
            }
        }
        c.disconnect();
        return new String[]{sb.toString(), ck.toString()};
    }

    /** 给 BiliLogin 用的 GET（返回 body 文本）。 */
    static String httpGet(String url, String cookie) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setRequestProperty("User-Agent", UA);
        c.setRequestProperty("Referer", REFER);
        if (cookie != null && !cookie.isEmpty()) c.setRequestProperty("Cookie", cookie);
        c.setConnectTimeout(12000);
        c.setReadTimeout(15000);
        if (c.getResponseCode() != 200) {
            int code = c.getResponseCode();
            c.disconnect();
            throw new Exception("HTTP " + code);
        }
        StringBuilder sb = new StringBuilder();
        try (BufferedReader br = new BufferedReader(new InputStreamReader(c.getInputStream(), "UTF-8"))) {
            String line;
            while ((line = br.readLine()) != null) sb.append(line);
        }
        c.disconnect();
        return sb.toString();
    }

    private static String getJson(String url) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        setHeaders(c);
        c.setConnectTimeout(12000);
        c.setReadTimeout(15000);
        if (c.getResponseCode() != 200) {
            c.disconnect();
            throw new Exception("HTTP " + c.getResponseCode());
        }
        StringBuilder sb = new StringBuilder();
        try (BufferedReader br = new BufferedReader(new InputStreamReader(c.getInputStream(), "UTF-8"))) {
            String line;
            while ((line = br.readLine()) != null) sb.append(line);
        }
        c.disconnect();
        return sb.toString();
    }

    private static JSONObject pickVideo(JSONArray arr) {
        if (arr == null || arr.length() == 0) return null;
        JSONObject best = null;
        int bestRank = 99;
        long bestBw = -1;
        for (int i = 0; i < arr.length(); i++) {
            JSONObject o = arr.optJSONObject(i);
            if (o == null) continue;
            int rank = codecRank(o.optString("codecs", ""));
            long bw = o.optLong("bandwidth", 0);
            if (rank < bestRank || (rank == bestRank && bw > bestBw)) {
                best = o;
                bestRank = rank;
                bestBw = bw;
            }
        }
        return best;
    }

    private static JSONObject pickAudio(JSONArray arr) {
        if (arr == null || arr.length() == 0) return null;
        JSONObject best = null;
        long bestBw = -1;
        for (int i = 0; i < arr.length(); i++) {
            JSONObject o = arr.optJSONObject(i);
            if (o == null) continue;
            long bw = o.optLong("bandwidth", 0);
            if (bw > bestBw) {
                best = o;
                bestBw = bw;
            }
        }
        return best;
    }

    private static int codecRank(String c) {
        c = c == null ? "" : c.toLowerCase();
        if (c.startsWith("avc")) return 0;   // 兼容性最好
        if (c.startsWith("hev") || c.startsWith("hvc")) return 1;
        if (c.startsWith("av01")) return 2;
        return 3;
    }

    private static List<String> candidates(JSONObject stream) {
        List<String> out = new ArrayList<>();
        String base = stream.optString("baseUrl", "");
        if (!base.isEmpty()) out.add(base);
        JSONArray bu = stream.optJSONArray("backupUrl");
        if (bu != null) {
            for (int i = 0; i < bu.length(); i++) {
                String u = bu.optString(i, "");
                if (!u.isEmpty()) out.add(u);
            }
        }
        return out;
    }

    /** 并发探一遍，返回响应最快的；都失败返回第一个。 */
    private static String probeFastest(final List<String> urls) {
        if (urls.isEmpty()) return null;
        if (urls.size() == 1) return urls.get(0);
        final String[] best = new String[]{null};
        final long[] bestMs = new long[]{Long.MAX_VALUE};
        final Object lock = new Object();
        List<Thread> ts = new ArrayList<>();
        for (final String u : urls) {
            Thread t = new Thread(() -> {
                long start = System.currentTimeMillis();
                try {
                    HttpURLConnection c = (HttpURLConnection) new URL(u).openConnection();
                    setHeaders(c);
                    c.setRequestProperty("Range", "bytes=0-1");
                    c.setConnectTimeout(6000);
                    c.setReadTimeout(6000);
                    int code = c.getResponseCode();
                    c.disconnect();
                    long ms = System.currentTimeMillis() - start;
                    if (code == 200 || code == 206) {
                        synchronized (lock) {
                            if (ms < bestMs[0]) {
                                bestMs[0] = ms;
                                best[0] = u;
                            }
                        }
                    }
                } catch (Exception ignored) {
                }
            });
            t.setDaemon(true);
            t.start();
            ts.add(t);
        }
        for (Thread t : ts) {
            try {
                t.join(8000);
            } catch (Exception ignored) {
            }
        }
        return best[0] != null ? best[0] : urls.get(0);
    }

    private static long contentLength(String url) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
            setHeaders(c);
            c.setRequestProperty("Range", "bytes=0-0");
            c.setConnectTimeout(8000);
            c.setReadTimeout(8000);
            int code = c.getResponseCode();
            String cr = c.getHeaderField("Content-Range");
            long n = 0;
            if (cr != null) {
                int i = cr.lastIndexOf('/');
                if (i > 0) {
                    try {
                        n = Long.parseLong(cr.substring(i + 1).trim());
                    } catch (Exception ignored) {
                    }
                }
            }
            if (n == 0) n = c.getContentLength();
            c.disconnect();
            return n;
        } catch (Exception e) {
            return 0;
        }
    }

    private static void download(String url, File dst, long[] prog, Cb cb) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        setHeaders(c);
        c.setConnectTimeout(15000);
        c.setReadTimeout(30000);
        int code = c.getResponseCode();
        if (code != 200 && code != 206) {
            c.disconnect();
            throw new Exception("HTTP " + code);
        }
        try (InputStream in = c.getInputStream(); OutputStream out = new FileOutputStream(dst)) {
            byte[] buf = new byte[256 * 1024];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
                prog[0] += n;
                cb.onProgress(prog[0], prog[1]);
            }
        }
        c.disconnect();
    }

    // ------------------------------------------------------------ ffmpeg

    /**
     * 找到能执行的 ffmpeg。
     *
     * Android 10+ 有 W^X 限制：filesDir 里的二进制 fork+exec 会 EACCES
     * （实测 error=13 Permission denied）。所以二进制必须按 .so 打进
     * APK 的 lib/arm64-v8a/，安装后落在 nativeLibraryDir 里，那里才允许执行。
     */
    static File ensureFfmpeg(Context ctx) {
        File lib = new File(ctx.getApplicationInfo().nativeLibraryDir, "libffmpeg.so");
        if (lib.exists() && lib.length() > 100000) return lib;
        Log.w(TAG, "nativeLibraryDir 里没有 libffmpeg.so：" + lib);
        return null;
    }

    private static boolean merge(File ff, File v, File a, File out) {
        try {
            ProcessBuilder pb = new ProcessBuilder(
                    ff.getAbsolutePath(), "-y", "-hide_banner", "-loglevel", "error",
                    "-i", v.getAbsolutePath(),
                    "-i", a.getAbsolutePath(),
                    "-c", "copy", "-movflags", "+faststart",
                    out.getAbsolutePath());
            pb.redirectErrorStream(true);
            Process p = pb.start();
            StringBuilder log = new StringBuilder();
            try (BufferedReader br = new BufferedReader(new InputStreamReader(p.getInputStream(), "UTF-8"))) {
                String line;
                while ((line = br.readLine()) != null) log.append(line).append('\n');
            }
            int rc = p.waitFor();
            if (rc != 0) {
                Log.w(TAG, "ffmpeg 退出码 " + rc + "：" + log);
                return false;
            }
            return out.exists() && out.length() > 0;
        } catch (Exception e) {
            Log.w(TAG, "调 ffmpeg 出错", e);
            return false;
        }
    }

    private static String sanitize(String s) {
        String out = s.replaceAll("[\\\\/:*?\"<>|\\r\\n]", "_").trim();
        if (out.length() > 80) out = out.substring(0, 80);
        return out.isEmpty() ? "bili-video" : out;
    }

    /**
     * 把成品放进系统公共目录 `下载/XFlow/`。
     * Android 10+ 走 MediaStore（不用任何权限）；更老的直接写公共目录。
     * 返回 content:// uri（新系统）或绝对路径（老系统），失败返回 null。
     */
    static String publishToDownloads(Context ctx, File src, String name) {
        try {
            if (Build.VERSION.SDK_INT >= 29) {
                ContentValues cv = new ContentValues();
                cv.put(MediaStore.MediaColumns.DISPLAY_NAME, name);
                cv.put(MediaStore.MediaColumns.MIME_TYPE, "video/mp4");
                cv.put(MediaStore.MediaColumns.RELATIVE_PATH,
                        Environment.DIRECTORY_DOWNLOADS + "/XFlow");
                cv.put(MediaStore.MediaColumns.IS_PENDING, 1);
                Uri uri = ctx.getContentResolver()
                        .insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
                if (uri == null) return null;
                try (OutputStream os = ctx.getContentResolver().openOutputStream(uri);
                     InputStream in = new java.io.FileInputStream(src)) {
                    if (os == null) return null;
                    byte[] buf = new byte[256 * 1024];
                    int n;
                    while ((n = in.read(buf)) > 0) os.write(buf, 0, n);
                }
                cv.clear();
                cv.put(MediaStore.MediaColumns.IS_PENDING, 0);
                ctx.getContentResolver().update(uri, cv, null, null);
                return uri.toString();
            }
            File dir = new File(
                    Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                    "XFlow");
            if (!dir.exists() && !dir.mkdirs()) return null;
            File out = new File(dir, name);
            copyFile(src, out);
            return out.getAbsolutePath();
        } catch (Exception e) {
            Log.w(TAG, "写公共下载目录失败", e);
            return null;
        }
    }

    private static void copyFile(File src, File dst) {
        try (InputStream in = new java.io.FileInputStream(src);
             OutputStream out = new FileOutputStream(dst)) {
            byte[] buf = new byte[64 * 1024];
            int n;
            while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
        } catch (Exception ignored) {
        }
    }
}
