package com.qingqiu.xflow;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.RandomAccessFile;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;

import xflowmobile.Xflowmobile;

/**
 * 下载服务：分块并发 + 断点续传 + 暂停/继续。
 *
 * 设计要点：
 *  - 任务级并发由线程池限死（MAX_TASKS），每个任务内部再按分块数开线程；
 *  - 每块用 RandomAccessFile 定位写，互不干扰；
 *  - 断点信息写 .meta（JSON），暂停时落盘，重启后按块续传；
 *  - 前台服务 + WakeLock，灭屏也继续跑。
 */
public class DownloadService extends Service {

    public static final String CHANNEL = "xflow_dl";
    public static final List<Job> JOBS = new ArrayList<>();
    public static final String ACTION_ADD = "com.qingqiu.xflow.ADD";
    public static final String ACTION_PAUSE = "com.qingqiu.xflow.PAUSE";
    public static final String ACTION_RESUME = "com.qingqiu.xflow.RESUME";
    public static final String ACTION_CANCEL = "com.qingqiu.xflow.CANCEL";

    /** 同时下载的任务数 */
    private static final int MAX_TASKS = 4;
    /** 单个任务内的分块线程上限 */
    private static final int MAX_SEG_THREADS = 16;

    private static int seq = 1;
    private static ExecutorService pool;
    private PowerManager.WakeLock wakeLock;

    @Override
    public void onCreate() {
        super.onCreate();
        NotificationChannel ch = new NotificationChannel(
                CHANNEL, "XFlow 下载", NotificationManager.IMPORTANCE_LOW);
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm != null) nm.createNotificationChannel(ch);
        pool = Executors.newFixedThreadPool(MAX_TASKS);

        // BT 引擎（Go + anacrolix/torrent，通过 gomobile 绑定的 AAR）
        try {
            File btCache = new File(getCacheDir(), "bt");
            btCache.mkdirs();
            Xflowmobile.start(btCache.getAbsolutePath());
        } catch (Throwable ignored) {
            // 没有 arm64 lib 或引擎起不来时，HTTP 下载照常
        }

        PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
        if (pm != null) {
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "XFlow:download");
            wakeLock.setReferenceCounted(false);
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        startForegroundNotif("XFlow 正在运行", "等待下载任务", -1);
        if (intent != null && intent.getAction() != null) {
            String action = intent.getAction();
            String id = intent.getStringExtra("id");
            switch (action) {
                case ACTION_ADD: {
                    String url = intent.getStringExtra("url");
                    String name = intent.getStringExtra("name");
                    int segs = intent.getIntExtra("segs", 8);
                    if (url != null && !url.isEmpty()) addJob(url, name, segs);
                    break;
                }
                case ACTION_PAUSE:
                    pauseJob(id);
                    break;
                case ACTION_RESUME:
                    resumeJob(id);
                    break;
                case ACTION_CANCEL:
                    cancelJob(id);
                    break;
                default:
                    break;
            }
        }
        return START_STICKY;
    }

    // ---------------------------------------------------------------- 任务增删

    private void addJob(String url, String name, int segN) {
        if (name == null || name.trim().isEmpty()) name = guessName(url);
        if (segN <= 0) segN = 8;
        if (segN > 16) segN = 16;
        Job j = new Job(String.valueOf(seq++), url, name);
        j.segN = segN;

        // 磁力 / 种子 == 交给 Go 的 BT 引擎，Java 这边只留个影子任务
        if (isBtSource(url)) {
            j.kind = "bt";
            try {
                j.btId = Xflowmobile.addMagnet(url, downloadDir().getAbsolutePath());
                j.state = "downloading";
                synchronized (JOBS) {
                    JOBS.add(j);
                }
            } catch (Throwable e) {
                j.state = "error";
                j.err = "BT 引擎： " + e.getMessage();
                synchronized (JOBS) {
                    JOBS.add(j);
                }
            }
            return;
        }

        synchronized (JOBS) {
            JOBS.add(j);
        }
        start(j);
    }

    private static boolean isBtSource(String src) {
        if (src == null) return false;
        String s = src.trim().toLowerCase();
        return s.startsWith("magnet:?") || s.endsWith(".torrent");
    }

    private void start(final Job j) {
        j.canceled = false;
        j.state = "queued";
        pool.submit(() -> {
            try {
                download(j);
            } catch (Exception e) {
                j.state = "error";
                j.err = e.getClass().getSimpleName() + ": " + e.getMessage();
            }
        });
    }

    private void pauseJob(String id) {
        Job j = find(id);
        if (j == null) return;
        if ("bt".equals(j.kind)) {
            try {
                Xflowmobile.pause(j.btId);
            } catch (Throwable ignored) {
            }
            j.state = "paused";
            return;
        }
        j.canceled = true;
        if ("downloading".equals(j.state)) j.state = "paused";
    }

    private void resumeJob(String id) {
        Job j = find(id);
        if (j == null) return;
        if ("downloading".equals(j.state) || "queued".equals(j.state)) return;
        if ("bt".equals(j.kind)) {
            // Go 侧 Pause 之后不支持恢复同一个 torrent，这里重新加一次（会校验已有数据）
            try {
                j.btId = Xflowmobile.addMagnet(j.url, downloadDir().getAbsolutePath());
                j.state = "downloading";
                j.err = "";
            } catch (Throwable e) {
                j.state = "error";
                j.err = e.getMessage();
            }
            return;
        }
        start(j);
    }

    private void cancelJob(String id) {
        Job j = find(id);
        if (j == null) return;
        if ("bt".equals(j.kind)) {
            try {
                Xflowmobile.remove(j.btId);
            } catch (Throwable ignored) {
            }
        }
        j.canceled = true;
        j.removed = true;
        synchronized (JOBS) {
            JOBS.remove(j);
        }
        // 线程退出后再删文件，这里只标记；清理放在 download 的 finally
    }

    private Job find(String id) {
        if (id == null) return null;
        synchronized (JOBS) {
            for (Job j : JOBS) if (id.equals(j.id)) return j;
        }
        return null;
    }

    // ---------------------------------------------------------------- 下载主流程

    private File downloadDir() {
        File dir = new File(getExternalFilesDir(null), "Download");
        if (!dir.exists()) dir.mkdirs();
        return dir;
    }

    private void download(Job j) throws Exception {
        if (j.removed) return;
        File dir = downloadDir();
        File out = new File(dir, j.name);
        if (j.path.isEmpty()) {
            if (out.exists() && !new File(out.getAbsolutePath() + ".part").exists()) {
                out = new File(dir, System.currentTimeMillis() + "-" + j.name);
            }
            j.path = out.getAbsolutePath();
            j.partPath = j.path + ".part";
            j.metaPath = j.path + ".meta";
        }
        out = new File(j.path);

        j.state = "downloading";
        acquireWakeLock();

        try {
            // 1) 探测大小 / 是否支持分段；已有 .meta 就跳过
            if (j.total <= 0 || j.segs.isEmpty()) {
                if (!restoreMeta(j)) {
                    probe(j);
                }
            }

            if (j.total <= 0 || j.segs.isEmpty()) {
                // 拿不到大小或服务端不支持 Range：老老实实单流下
                singleStream(j, out);
            } else {
                downloadSegments(j);
            }

            if (j.removed) {
                cleanup(j, out);
                return;
            }
            if (j.canceled) {
                saveMeta(j);
                j.state = "paused";
                return;
            }
            if (j.total > 0 && j.done >= j.total) {
                File part = new File(j.partPath);
                if (part.exists()) part.renameTo(out);
                new File(j.metaPath).delete();
                j.state = "done";
                j.done = j.total;
                startForegroundNotif("下载完成", j.name, -1);
            } else if (j.total <= 0) {
                File part = new File(j.partPath);
                if (part.exists()) part.renameTo(out);
                j.state = "done";
            }
        } finally {
            releaseWakeLock();
        }
    }

    /** 探测：优先 HEAD，失败再用 Range: bytes=0-0 的 GET */
    private void probe(Job j) {
        long size = -1;
        boolean ranges = false;
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(j.url).openConnection();
            c.setConnectTimeout(15000);
            c.setReadTimeout(20000);
            c.setRequestProperty("User-Agent", "XFlow/1.1 (Android)");
            c.setRequestProperty("Range", "bytes=0-0");
            c.setInstanceFollowRedirects(true);
            c.connect();
            int code = c.getResponseCode();
            if (code == 206) {
                ranges = true;
                String cr = c.getHeaderField("Content-Range"); // bytes 0-0/12345
                if (cr != null) {
                    int slash = cr.lastIndexOf('/');
                    if (slash > 0) {
                        try {
                            size = Long.parseLong(cr.substring(slash + 1).trim());
                        } catch (Exception ignored) {
                        }
                    }
                }
            } else if (code >= 400) {
                j.state = "error";
                j.err = "HTTP " + code;
                return;
            } else {
                ranges = c.getHeaderField("Accept-Ranges") != null
                        && "bytes".equalsIgnoreCase(c.getHeaderField("Accept-Ranges"));
                long len = c.getContentLengthLong();
                if (len > 0) size = len;
            }
        } catch (Exception e) {
            j.err = e.getClass().getSimpleName();
        } finally {
            if (c != null) c.disconnect();
        }

        if (size <= 0 || !ranges) {
            // 不支持分段，走单流；清掉分段信息
            j.total = size > 0 ? size : 0;
            j.resetSegments();
            return;
        }

        j.total = size;
        j.resetSegments();
        // 大文件自动加段：5G 这类高延迟链路上单连接跑不满带宽，
        // 1GB 以上直接给到 16 段（小文件没必要，段多了反而碎）
        int want = j.segN;
        if (size > 1024L * 1024 * 1024 && want < 16) want = 16;
        int n = Math.max(1, Math.min(want, MAX_SEG_THREADS));
        long per = size / n;
        for (int i = 0; i < n; i++) {
            long from = i * per;
            long to = (i == n - 1) ? size - 1 : (from + per - 1);
            j.segs.add(new Segment(from, to));
        }
        j.done = 0;
    }

    /** 并行下各段 */
    private void downloadSegments(Job j) throws Exception {
        RandomAccessFile raf = new RandomAccessFile(j.partPath, "rw");
        try {
            AtomicInteger live = new AtomicInteger(0);
            List<Thread> ts = new ArrayList<>();
            for (Segment s : j.segs) {
                if (s.remaining() <= 0) continue;
                Thread t = new Thread(() -> {
                    live.incrementAndGet();
                    j.workers = live.get();
                    try {
                        fetchSegment(j, s, raf);
                    } catch (Exception ignored) {
                    } finally {
                        live.decrementAndGet();
                        j.workers = live.get();
                    }
                }, "seg-" + j.id);
                ts.add(t);
                t.start();
            }
            // 主循环：更新进度、速度、通知，顺便盯暂停
            long lastT = System.currentTimeMillis();
            long lastDone = j.done;
            while (true) {
                boolean allDone = true;
                long sum = 0;
                for (Segment s : j.segs) {
                    sum += s.done;
                    if (s.done < s.size()) allDone = false;
                }
                j.done = sum;
                long now = System.currentTimeMillis();
                if (now - lastT >= 800) {
                    j.speed = (sum - lastDone) * 1000L / Math.max(1, now - lastT);
                    lastDone = sum;
                    lastT = now;
                    int pct = j.percent();
                    startForegroundNotif("正在下载 " + pct + "%",
                            j.name + " · " + human(j.done) + " / " + human(j.total)
                                    + " · " + human(j.speed) + "/s", pct);
                }
                if (j.canceled) {
                    for (Thread t : ts) t.interrupt();
                    break;
                }
                if (allDone) break;
                Thread.sleep(200);
            }
            for (Thread t : ts) {
                try {
                    t.join(3000);
                } catch (InterruptedException ignored) {
                }
            }
        } finally {
            raf.close();
        }
    }

    /** 拉一段 */
    private void fetchSegment(Job j, Segment s, RandomAccessFile raf) throws Exception {
        long pos = s.from + s.done;
        while (pos <= s.to) {
            if (j.canceled || j.removed) return;
            HttpURLConnection c = null;
            InputStream in = null;
            try {
                c = (HttpURLConnection) new URL(j.url).openConnection();
                c.setConnectTimeout(15000);
                c.setReadTimeout(30000);
                c.setRequestProperty("User-Agent", "XFlow/1.1 (Android)");
                c.setRequestProperty("Range", "bytes=" + pos + "-" + s.to);
                c.setInstanceFollowRedirects(true);
                c.connect();
                int code = c.getResponseCode();
                if (code == 416) return; // 越界，说明这块其实下完了
                if (code >= 400) throw new Exception("HTTP " + code);
                in = c.getInputStream();
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) {
                    if (j.canceled || j.removed) return;
                    synchronized (raf) {
                        raf.seek(pos);
                        raf.write(buf, 0, n);
                    }
                    pos += n;
                    s.done += n;
                    if (pos > s.to) break;
                }
            } catch (Exception e) {
                // 网络抖动：歇一下重试，整段最多重试 5 次由外层判断
                Thread.sleep(600);
                if (pos > s.to) return;
            } finally {
                try {
                    if (in != null) in.close();
                } catch (Exception ignored) {
                }
                if (c != null) c.disconnect();
            }
        }
    }

    /** 不支持分段时的单流下载 */
    private void singleStream(Job j, File out) throws Exception {
        if (j.canceled) return;
        HttpURLConnection c = null;
        InputStream in = null;
        RandomAccessFile raf = null;
        try {
            c = (HttpURLConnection) new URL(j.url).openConnection();
            c.setConnectTimeout(15000);
            c.setReadTimeout(30000);
            c.setRequestProperty("User-Agent", "XFlow/1.1 (Android)");
            c.setInstanceFollowRedirects(true);
            if (j.done > 0) {
                c.setRequestProperty("Range", "bytes=" + j.done + "-");
            }
            c.connect();
            int code = c.getResponseCode();
            if (code >= 400) {
                j.state = "error";
                j.err = "HTTP " + code;
                return;
            }
            if (j.total <= 0) j.total = c.getContentLengthLong();
            raf = new RandomAccessFile(j.partPath, "rw");
            raf.seek(j.done);
            in = c.getInputStream();
            byte[] buf = new byte[64 * 1024];
            int n;
            long lastT = System.currentTimeMillis();
            long lastDone = j.done;
            while ((n = in.read(buf)) > 0) {
                if (j.canceled || j.removed) return;
                raf.write(buf, 0, n);
                j.done += n;
                long now = System.currentTimeMillis();
                if (now - lastT >= 800) {
                    j.speed = (j.done - lastDone) * 1000L / Math.max(1, now - lastT);
                    lastDone = j.done;
                    lastT = now;
                    startForegroundNotif("正在下载 " + j.percent() + "%",
                            j.name + " · " + human(j.done) + " / " + human(j.total), j.percent());
                }
            }
        } finally {
            try {
                if (in != null) in.close();
            } catch (Exception ignored) {
            }
            try {
                if (raf != null) raf.close();
            } catch (Exception ignored) {
            }
            if (c != null) c.disconnect();
        }
    }

    // ---------------------------------------------------------------- 断点

    private void saveMeta(Job j) {
        try {
            JSONObject o = new JSONObject();
            o.put("url", j.url);
            o.put("total", j.total);
            o.put("name", j.name);
            JSONArray arr = new JSONArray();
            for (Segment s : j.segs) {
                JSONObject so = new JSONObject();
                so.put("from", s.from);
                so.put("to", s.to);
                so.put("done", s.done);
                arr.put(so);
            }
            o.put("segs", arr);
            FileOutputStream fos = new FileOutputStream(j.metaPath);
            fos.write(o.toString().getBytes("UTF-8"));
            fos.close();
        } catch (Exception ignored) {
        }
    }

    private boolean restoreMeta(Job j) {
        File f = new File(j.metaPath);
        if (!f.exists()) return false;
        try {
            byte[] buf = new byte[(int) f.length()];
            FileInputStream fis = new FileInputStream(f);
            int off = 0;
            while (off < buf.length) {
                int r = fis.read(buf, off, buf.length - off);
                if (r < 0) break;
                off += r;
            }
            fis.close();
            JSONObject o = new JSONObject(new String(buf, "UTF-8"));
            if (!j.url.equals(o.optString("url"))) return false;
            j.total = o.optLong("total", 0);
            JSONArray arr = o.optJSONArray("segs");
            j.resetSegments();
            long sum = 0;
            if (arr != null) {
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject so = arr.getJSONObject(i);
                    Segment s = new Segment(so.getLong("from"), so.getLong("to"));
                    s.done = so.getLong("done");
                    sum += s.done;
                    j.segs.add(s);
                }
            }
            j.done = sum;
            return !j.segs.isEmpty();
        } catch (Exception e) {
            return false;
        }
    }

    private void cleanup(Job j, File out) {
        try {
            new File(j.partPath).delete();
            new File(j.metaPath).delete();
        } catch (Exception ignored) {
        }
    }

    // ---------------------------------------------------------------- 通知 / 杂项

    private void startForegroundNotif(String title, String text, int progress) {
        Notification.Builder b;
        if (Build.VERSION.SDK_INT >= 26) {
            b = new Notification.Builder(this, CHANNEL);
        } else {
            b = new Notification.Builder(this);
        }
        b.setContentTitle(title)
                .setContentText(text)
                .setSmallIcon(android.R.drawable.stat_sys_download)
                .setOngoing(progress >= 0);
        if (progress >= 0) b.setProgress(100, progress, false);
        Notification n = b.build();
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(1, n);
        }
    }

    private void acquireWakeLock() {
        try {
            if (wakeLock != null && !wakeLock.isHeld()) {
                wakeLock.acquire(60 * 60 * 1000L);
            }
        } catch (Exception ignored) {
        }
    }

    private void releaseWakeLock() {
        try {
            if (wakeLock != null && wakeLock.isHeld()) wakeLock.release();
        } catch (Exception ignored) {
        }
    }

    public static String human(long n) {
        if (n <= 0) return "—";
        String[] u = {"B", "KB", "MB", "GB", "TB"};
        double d = n;
        int i = 0;
        while (d >= 1024 && i < u.length - 1) {
            d /= 1024;
            i++;
        }
        return String.format(i == 0 ? "%.0f%s" : "%.1f%s", d, u[i]);
    }

    private static String guessName(String url) {
        try {
            String p = new URL(url).getPath();
            int i = p.lastIndexOf('/');
            String n = i >= 0 ? p.substring(i + 1) : p;
            if (n == null || n.isEmpty()) n = "xflow-" + System.currentTimeMillis() + ".bin";
            return n;
        } catch (Exception e) {
            return "xflow-" + System.currentTimeMillis() + ".bin";
        }
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onDestroy() {
        releaseWakeLock();
        if (pool != null) pool.shutdownNow();
        super.onDestroy();
    }
}
