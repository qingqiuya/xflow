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
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.Paths;
import java.nio.file.StandardOpenOption;
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
    /** 同一段连续失败几次就放弃（read 超时 30s，5 次约 2 分半） */
    private static final int MAX_SEG_FAILS = 5;
    /** 自适应并发的节奏：跟桌面版 XFlow 1.6.0 一样，每 3 秒主动试一批新连接 */
    private static final long GROW_INTERVAL_MS = 3000;

    /** BT 引擎的状态/错误，界面会显示出来，方便排查「磁力下不了」 */
    public static volatile String btEngineState = "未初始化";

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
            btEngineState = "就绪";
        } catch (Throwable e) {
            // 没有 arm64 lib 或引擎起不来时，HTTP 下载照常，但要把原因说出来
            btEngineState = "失败：" + e.getClass().getSimpleName() + " " + e.getMessage();
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
        boolean bt = isBtSource(url);
        if (!bt && url.startsWith("/") && new File(url).isFile()) {
            // 本地文件不看后缀：能解析成种子的就当 BT 任务（桌面版就是这么干的）
            try {
                Xflowmobile.validate(url);
                bt = true;
            } catch (Throwable ignored) {
                bt = false;
            }
        }
        if (bt) {
            j.kind = "bt";
            // 本地种子先按内容校验一遍（跟桌面版一样不看后缀）：
            // 改过名的合法种子能进，文本改后缀的假种子直接拒。
            if (url.startsWith("/")) {
                try {
                    String real = Xflowmobile.validate(url);
                    if (real != null && !real.isEmpty()) j.name = real;
                } catch (Throwable e) {
                    j.state = "error";
                    j.err = "种子校验失败：" + e.getMessage();
                    synchronized (JOBS) {
                        JOBS.add(j);
                    }
                    return;
                }
            }
            try {
                j.btId = Xflowmobile.addMagnet(url, downloadDir().getAbsolutePath());
                j.state = "downloading";
                synchronized (JOBS) {
                    JOBS.add(j);
                }
            } catch (Throwable e) {
                String msg = String.valueOf(e.getMessage());
                // 链接叫 xxx.torrent、但内容根本不是种子的情况（或拿不到种子）：
                // 降级当普通直链下载，用户至少能把那个 .torrent 文件拿下来，
                // 之后再用「选种子文件」加载。
                if (msg.contains("不是可用的种子来源")
                        && (url.startsWith("http://") || url.startsWith("https://"))) {
                    j.kind = "http";
                    j.state = "queued";
                    j.err = "";
                    synchronized (JOBS) {
                        JOBS.add(j);
                    }
                    start(j);
                    return;
                }
                j.state = "error";
                j.err = "BT 引擎：" + e.getClass().getSimpleName() + " " + e.getMessage()
                        + "（引擎状态 " + btEngineState + "）";
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
        j.abortError = null;   // 重试/续传时把上次的失败原因清掉
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
        boolean running = "downloading".equals(j.state) || "queued".equals(j.state);
        if ("bt".equals(j.kind)) {
            try {
                Xflowmobile.remove(j.btId);
            } catch (Throwable ignored) {
            }
            // Go 的 remove 只把种子从客户端里拿掉，已经落盘的数据不删，这里补上：
            // 没下完的 BT 任务点删除就把它的文件/目录一起清了（下完的不碰）。
            if (j.done < j.total && j.name != null && !j.name.isEmpty()) {
                final File victim = new File(downloadDir(), j.name);
                new Thread(() -> {
                    try {
                        Thread.sleep(1200); // 等引擎把文件句柄放掉
                    } catch (InterruptedException ignored) {
                    }
                    deleteTree(victim);
                }).start();
            }
        }
        j.canceled = true;
        j.removed = true;
        synchronized (JOBS) {
            JOBS.remove(j);
        }
        // 还在跑的任务由 download() 的 finally 负责清；其余状态（暂停/出错/没启动过）
        // 不会再有代码走到那里，只能在这里删，否则 .part 永远躺在目录里。
        if (!running) cleanup(j, new File(j.path == null ? "" : j.path));
    }

    /** 递归删文件/目录（BT 的多文件任务会落成一个目录） */
    private void deleteTree(File f) {
        if (f == null || !f.exists()) return;
        if (f.isDirectory()) {
            File[] kids = f.listFiles();
            if (kids != null) for (File k : kids) deleteTree(k);
        }
        //noinspection ResultOfMethodCallIgnored
        f.delete();
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
            if (j.abortError != null) {
                // 不是用户暂停，是某段实在拿不到数据：标成 error，保留 .part 可续传
                saveMeta(j);
                j.state = "error";
                j.err = j.abortError;
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
            c.setRequestProperty("User-Agent", "XFlow/1.6.1 (Android)");
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

    /**
     * 并行下各段：worker 池 + 自适应增长（跟桌面版 XFlow 1.6.0 的 TCP 慢启动一致）。
     *
     * 段先按 16 份切好，worker 从队列里领：领一段、下完再领下一段。
     * 起始只开 segN 个 worker，然后每 3 秒主动试一批新的 —— 速度涨了继续加，
     * 连着两轮加了没效果就认定带宽吃满，停手（上限 16）。
     * 老逻辑是「有几个段就一次全开」，慢链路上一上来就把连接数拉满，反而互相抢。
     */
    private void downloadSegments(Job j) throws Exception {
        // 用 FileChannel 的定位写（pwrite 语义）：多个线程写同一个文件的不同区间
        // 不需要加锁。之前用 RandomAccessFile + synchronized 一把大锁，8 个线程
        // 每 64KB 抢一次锁，并发根本跑不上去 —— 这是手机端只有人家一半速度的主因。
        FileChannel ch = FileChannel.open(Paths.get(j.partPath),
                StandardOpenOption.CREATE, StandardOpenOption.READ, StandardOpenOption.WRITE);
        try {
            final AtomicInteger live = new AtomicInteger(0);
            final AtomicInteger nextSeg = new AtomicInteger(0);
            final List<Thread> ts = new ArrayList<>();
            final Object lock = new Object();

            // 一个 worker：领一段、下完再领下一段；领不到就退出
            final Runnable spawn = () -> {
                final int seq = ts.size();
                Thread t = new Thread(() -> {
                    live.incrementAndGet();
                    j.workers = live.get();
                    try {
                        while (!j.canceled && !j.removed) {
                            int i = nextSeg.getAndIncrement();
                            if (i >= j.segs.size()) break;
                            Segment s = j.segs.get(i);
                            if (s.remaining() <= 0) continue;
                            try {
                                fetchSegment(j, s, ch);
                            } catch (Exception ignored) {
                            }
                        }
                    } finally {
                        live.decrementAndGet();
                        j.workers = live.get();
                    }
                }, "seg-" + j.id + "-" + seq);
                synchronized (lock) {
                    ts.add(t);
                }
                t.start();
            };

            // 起始并发取任务给的段数；自适应会自己往上摸
            int start = Math.max(1, Math.min(j.segN, MAX_SEG_THREADS));
            for (int i = 0; i < start; i++) spawn.run();

            // 主循环：更新进度、速度、通知，顺便盯暂停 + 自适应
            long lastT = System.currentTimeMillis();
            long lastDone = j.done;
            long growAt = lastT + GROW_INTERVAL_MS;
            long prevSpeed = 0;
            int added = 0;
            int stall = 0;
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

                // 自适应：上一批加过就验收，速度涨 5% 以上算有效，连着两轮无效就收手
                if (now >= growAt) {
                    if (added > 0) {
                        boolean grew = prevSpeed > 0 && j.speed > prevSpeed * 1.05;
                        if (grew) stall = 0;
                        else stall++;
                    }
                    int size;
                    synchronized (lock) {
                        size = ts.size();
                    }
                    if (stall < 2 && size < MAX_SEG_THREADS && !allDone) {
                        int addN = Math.max(1, added);
                        for (int i = 0; i < addN && size < MAX_SEG_THREADS; i++, size++) {
                            spawn.run();
                            added++;
                        }
                        prevSpeed = j.speed;
                    }
                    growAt = now + GROW_INTERVAL_MS;
                }

                if (j.canceled) {
                    synchronized (lock) {
                        for (Thread t : ts) t.interrupt();
                    }
                    break;
                }
                if (allDone && live.get() == 0) break;
                // 线程都退了却还有段没下完（领到段就异常退出）：补一个，别卡死
                if (live.get() == 0 && !allDone) spawn.run();
                Thread.sleep(200);
            }
            synchronized (lock) {
                for (Thread t : ts) {
                    try {
                        t.join(3000);
                    } catch (InterruptedException ignored) {
                    }
                }
            }
        } finally {
            ch.close();
        }
    }

    /** 拉一段 */
    private void fetchSegment(Job j, Segment s, FileChannel ch) throws Exception {
        long pos = s.from + s.done;
        while (pos <= s.to) {
            if (j.canceled || j.removed) return;
            HttpURLConnection c = null;
            InputStream in = null;
            try {
                c = (HttpURLConnection) new URL(j.url).openConnection();
                c.setConnectTimeout(15000);
                c.setReadTimeout(30000);
                c.setRequestProperty("User-Agent", "XFlow/1.6.1 (Android)");
                c.setRequestProperty("Range", "bytes=" + pos + "-" + s.to);
                c.setInstanceFollowRedirects(true);
                c.connect();
                int code = c.getResponseCode();
                if (code == 416) return; // 越界，说明这块其实下完了
                if (code >= 400) throw new Exception("HTTP " + code);
                in = c.getInputStream();
                byte[] buf = new byte[128 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) {
                    if (j.canceled || j.removed) return;
                    ByteBuffer bb = ByteBuffer.wrap(buf, 0, n);
                    long wp = pos;
                    while (bb.hasRemaining()) {
                        int w = ch.write(bb, wp);
                        if (w <= 0) break;
                        wp += w;
                    }
                    pos += n;
                    s.done += n;
                    if (pos > s.to) break;
                }
            } catch (Exception e) {
                // 网络抖动：歇一下重试。但必须有上限——read 超时是 30 秒，
                // 没有上限的话一个死段会让任务永远停在 99%（其他段都完了，它还在转）。
                s.fails++;
                if (s.fails >= MAX_SEG_FAILS) {
                    j.abortError = "有一段连续 " + MAX_SEG_FAILS + " 次拿不到数据（"
                            + e.getClass().getSimpleName() + " " + e.getMessage()
                            + "），已停下。.part 保留，重试可续传";
                    j.canceled = true;
                    return;
                }
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
            byte[] buf = new byte[128 * 1024];
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
