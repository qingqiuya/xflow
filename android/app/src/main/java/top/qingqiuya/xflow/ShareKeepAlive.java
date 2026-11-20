package top.qingqiuya.xflow;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;

/**
 * 互传保活用的前台服务。
 *
 * 为什么需要：MIUI 这类系统会把后台 App 直接冻结（freezer），进程一冻，
 * ApiServer(17777) 和 LocalSend(53317) 的监听就跟着停，对面发文件过来只会看到
 * 「网络断了」。前台服务带通知栏图标，系统不会冻它，两个端口就能一直活着。
 *
 * 关掉方式：通知栏里点「关闭互传」或者把互传开关关掉。
 */
public class ShareKeepAlive extends Service {
    public static final String CHANNEL = "xflow_share";
    public static final int NOTI_ID = 1001;

    public static void start(Context ctx) {
        try {
            Intent i = new Intent(ctx, ShareKeepAlive.class);
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i);
            else ctx.startService(i);
        } catch (Exception ignored) {
        }
    }

    public static void stop(Context ctx) {
        try {
            ctx.stopService(new Intent(ctx, ShareKeepAlive.class));
        } catch (Exception ignored) {
        }
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (nm != null && Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, "互传", NotificationManager.IMPORTANCE_MIN);
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }

        Intent open = new Intent(this, MainActivity.class);
        open.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent pi = PendingIntent.getActivity(this, 0, open,
                Build.VERSION.SDK_INT >= 23 ? PendingIntent.FLAG_IMMUTABLE : 0);

        Notification.Builder b = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, CHANNEL)
                : new Notification.Builder(this);
        b.setContentTitle("汐流互传已就绪")
                .setContentText("别的设备可以往这台手机发文件")
                .setSmallIcon(android.R.drawable.stat_sys_download_done)
                .setContentIntent(pi)
                .setOngoing(true);

        startForeground(NOTI_ID, b.build());
        return START_STICKY;
    }
}
