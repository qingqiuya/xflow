package top.qingqiuya.xflow;

import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.MediaStore;
import android.util.Log;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;

/**
 * 互传：收到的文件落在系统「下载/XFlow」里，别的设备也能从这儿拉回去。
 *
 * 为什么不用 app 私有目录：Android 11+ 文件管理器根本进不去 Android/data，
 * 收到了也找不到。走 MediaStore.Downloads（API 29+）不需要任何存储权限，
 * 文件管理器一眼能看见；API < 29 退回公共下载目录 + WRITE_EXTERNAL_STORAGE。
 */
public class Transfer {

    public static final String SUBDIR = "XFlow"; // Download/XFlow

    public static class Item {
        public String name = "";
        public long size = 0;
        public String uri = "";   // content:// 或 file://
    }

    private static ContentResolver res(Context ctx) {
        return ctx.getContentResolver();
    }

    private static String mimeOf(String name) {
        String n = name.toLowerCase();
        if (n.endsWith(".jpg") || n.endsWith(".jpeg")) return "image/jpeg";
        if (n.endsWith(".png")) return "image/png";
        if (n.endsWith(".gif")) return "image/gif";
        if (n.endsWith(".webp")) return "image/webp";
        if (n.endsWith(".mp4")) return "video/mp4";
        if (n.endsWith(".mkv")) return "video/x-matroska";
        if (n.endsWith(".mp3")) return "audio/mpeg";
        if (n.endsWith(".pdf")) return "application/pdf";
        if (n.endsWith(".zip")) return "application/zip";
        if (n.endsWith(".apk")) return "application/vnd.android.package-archive";
        if (n.endsWith(".txt") || n.endsWith(".log") || n.endsWith(".md")) return "text/plain";
        if (n.endsWith(".json")) return "application/json";
        return "application/octet-stream";
    }

    /** 把流存进「下载/XFlow」，返回落地后的名字（重名会加 (1) 由系统处理）。 */
    public static String saveStream(Context ctx, InputStream in, String name, long len) throws Exception {
        if (name == null || name.isEmpty()) name = "xflow-" + System.currentTimeMillis();
        name = name.replace('/', '_').replace('\\', '_');
        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues cv = new ContentValues();
            cv.put(MediaStore.MediaColumns.DISPLAY_NAME, name);
            cv.put(MediaStore.MediaColumns.MIME_TYPE, mimeOf(name));
            cv.put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/" + SUBDIR);
            Uri uri = res(ctx).insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
            if (uri == null) throw new Exception("MediaStore 插入失败（存储不可用？）");
            OutputStream os = res(ctx).openOutputStream(uri);
            if (os == null) throw new Exception("打不开输出流");
            try {
                copyLimit(in, os, len);
            } finally {
                os.close();
            }
            Log.i("xflow-xfer", "收到 " + name + " -> " + uri);
            return name;
        }
        File dir = new File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), SUBDIR);
        if (!dir.exists() && !dir.mkdirs()) throw new Exception("建目录失败：" + dir);
        File f = new File(dir, name);
        FileOutputStream os = new FileOutputStream(f);
        try {
            copyLimit(in, os, len);
        } finally {
            os.close();
        }
        return f.getName();
    }

    /** 列出「下载/XFlow」里的文件，新的在前。 */
    public static List<Item> list(Context ctx) {
        List<Item> out = new ArrayList<>();
        try {
            if (Build.VERSION.SDK_INT >= 29) {
                Uri base = MediaStore.Downloads.EXTERNAL_CONTENT_URI;
                String[] proj = {MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME,
                        MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_ADDED};
                String sel = MediaStore.MediaColumns.RELATIVE_PATH + " LIKE ?";
                String[] args = {Environment.DIRECTORY_DOWNLOADS + "/" + SUBDIR + "%"};
                try (Cursor c = res(ctx).query(base, proj, sel, args,
                        MediaStore.MediaColumns.DATE_ADDED + " DESC")) {
                    if (c != null) {
                        while (c.moveToNext()) {
                            Item it = new Item();
                            long id = c.getLong(0);
                            it.name = c.getString(1);
                            it.size = c.getLong(2);
                            it.uri = Uri.withAppendedPath(base, String.valueOf(id)).toString();
                            out.add(it);
                        }
                    }
                }
            } else {
                File dir = new File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), SUBDIR);
                File[] fs = dir.listFiles();
                if (fs != null) {
                    java.util.Arrays.sort(fs, (a, b) -> Long.compare(b.lastModified(), a.lastModified()));
                    for (File f : fs) {
                        if (!f.isFile()) continue;
                        Item it = new Item();
                        it.name = f.getName();
                        it.size = f.length();
                        it.uri = Uri.fromFile(f).toString();
                        out.add(it);
                    }
                }
            }
        } catch (Exception e) {
            Log.w("xflow-xfer", "列目录失败: " + e);
        }
        return out;
    }

    public static InputStream open(Context ctx, String uri) throws Exception {
        if (uri == null || uri.isEmpty()) throw new Exception("缺 uri");
        if (uri.startsWith("content://")) {
            return res(ctx).openInputStream(Uri.parse(uri));
        }
        if (uri.startsWith("file://")) {
            return new FileInputStream(new File(Uri.parse(uri).getPath()));
        }
        throw new Exception("不认这个 uri：" + uri);
    }

    /**
     * 只读 limit 个字节 —— socket 上读完 body 不会收到 EOF（HTTP keep-alive），
     * 用无上限的 copy 会一直等下去，客户端超时断开才结束（结果就是文件 0 字节 + 没响应）。
     */
    private static void copyLimit(InputStream in, OutputStream out, long limit) throws Exception {
        byte[] buf = new byte[64 * 1024];
        long remain = limit;
        while (remain > 0) {
            int want = (int) Math.min(buf.length, remain);
            int n = in.read(buf, 0, want);
            if (n <= 0) break;
            out.write(buf, 0, n);
            remain -= n;
        }
        out.flush();
    }

    private static void copy(InputStream in, OutputStream out) throws Exception {
        byte[] buf = new byte[64 * 1024];
        int n;
        while ((n = in.read(buf)) > 0) {
            out.write(buf, 0, n);
        }
        out.flush();
    }

    /** 删掉一个收到的文件。content:// 走 MediaStore，file:// 直接删；返回是否成功。 */
    public static boolean delete(Context ctx, String uriStr) {
        if (uriStr == null || uriStr.isEmpty()) return false;
        try {
            Uri u = Uri.parse(uriStr);
            if ("content".equals(u.getScheme())) {
                // 自己插进 MediaStore 的文件，App 有权直接删（Android 10+ 不需要额外权限）
                return res(ctx).delete(u, null, null) > 0;
            }
            if ("file".equals(u.getScheme())) {
                File f = new File(u.getPath());
                return f.exists() && f.delete();
            }
        } catch (Exception e) {
            Log.w("xflow-xfer", "删除失败：" + e.getMessage());
        }
        return false;
    }
}
