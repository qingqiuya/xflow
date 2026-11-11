package top.qingqiuya.xflow;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.TextUtils;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.util.ArrayList;
import java.util.List;

import org.json.JSONObject;

import xflowmobile.Xflowmobile;

public class MainActivity extends Activity {

    private EditText urlEdit, nameEdit;
    private LinearLayout jobList;
    private TextView statText;
    private final Handler ui = new Handler(Looper.getMainLooper());

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        setContentView(R.layout.activity_main);
        urlEdit = findViewById(R.id.urlEdit);
        nameEdit = findViewById(R.id.nameEdit);
        jobList = findViewById(R.id.jobList);
        statText = findViewById(R.id.statText);

        // 副标题后面跟版本号：直接从包信息里读，不另外硬编码
        TextView sub = findViewById(R.id.subText);
        if (sub != null) {
            try {
                String v = getPackageManager()
                        .getPackageInfo(getPackageName(), 0).versionName;
                sub.setText("汐流 · 把文件带下来 · v" + v);
            } catch (Exception ignored) {
            }
            // 点副标题看开源许可（APK 里没有 ActionBar，不另开菜单）
            sub.setOnClickListener(v -> showLicenses());
        }

        Button add = findViewById(R.id.addBtn);
        Button torrentBtn = findViewById(R.id.torrentBtn);
        torrentBtn.setOnClickListener(v -> pickTorrent());
        add.setOnClickListener(v -> {
            String url = urlEdit.getText().toString().trim();
            if (TextUtils.isEmpty(url)) {
                urlEdit.requestFocus();
                return;
            }
            String name = nameEdit.getText().toString().trim();
            Intent i = new Intent(this, DownloadService.class);
            i.setAction(DownloadService.ACTION_ADD);
            i.putExtra("url", url);
            i.putExtra("name", name);
            startForegroundService(i);
            urlEdit.setText("");
            nameEdit.setText("");
        });

        ui.post(render);
    }

    // ---------------------------------------------------------------- 开源许可
    //
    // 分发 APK 时要把 MIT 正文和第三方组件（MPL-2.0 / Apache-2.0 / BSD）的许可
    // 原文一起带上。文本来自仓库根目录，build.sh 会拷进 assets/。

    private void showLicenses() {
        String text;
        try (InputStream in = getAssets().open("THIRD_PARTY_LICENSES.md")) {
            StringBuilder sb = new StringBuilder();
            BufferedReader br = new BufferedReader(new InputStreamReader(in, "UTF-8"));
            String line;
            while ((line = br.readLine()) != null) {
                sb.append(line).append('\n');
            }
            text = sb.toString();
        } catch (Exception e) {
            text = "许可文件没打进 APK：" + e.getMessage();
        }
        TextView tv = new TextView(this);
        int pad = (int) (16 * getResources().getDisplayMetrics().density);
        tv.setPadding(pad, pad, pad, pad);
        tv.setTextSize(11);
        tv.setTextIsSelectable(true);
        tv.setTypeface(android.graphics.Typeface.MONOSPACE);
        tv.setText(text);

        android.widget.ScrollView sv = new android.widget.ScrollView(this);
        sv.addView(tv);

        new AlertDialog.Builder(this)
                .setTitle("开源许可")
                .setView(sv)
                .setPositiveButton("好", null)
                .show();
    }

    // ---------------------------------------------------------------- 种子文件

    private static final int REQ_TORRENT = 4001;

    private void pickTorrent() {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");   // 很多机器上 application/x-bittorrent 不认，就放宽一点
        try {
            startActivityForResult(i, REQ_TORRENT);
        } catch (Exception e) {
            Toast.makeText(this, "没有可用的文件选择器", Toast.LENGTH_SHORT).show();
        }
    }

    @Override
    protected void onActivityResult(int req, int res, Intent data) {
        super.onActivityResult(req, res, data);
        if (req != REQ_TORRENT || res != RESULT_OK || data == null || data.getData() == null) return;
        Uri uri = data.getData();
        try {
            File dir = new File(getCacheDir(), "torrent");
            dir.mkdirs();
            // 复制到 App 私有目录再交给引擎：跨进程读 content:// 不可靠，
            // 而且引擎要的是本地路径。文件名带 .torrent，引擎会按种子解析。
            File out = new File(dir, "picked-" + System.currentTimeMillis() + ".torrent");
            try (InputStream in = getContentResolver().openInputStream(uri);
                 FileOutputStream fo = new FileOutputStream(out)) {
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) fo.write(buf, 0, n);
            }
            // 先按内容校验一遍（桌面版 1.1.6 同款）：改过名的合法种子能进，
            // 文本改后缀的假种子当场拒，不建任务。
            String title;
            try {
                title = Xflowmobile.validate(out.getAbsolutePath());
            } catch (Throwable e) {
                out.delete();
                Toast.makeText(this, e.getMessage() == null ? "这不是合法的种子文件" : e.getMessage(),
                        Toast.LENGTH_LONG).show();
                return;
            }
            Intent svc = new Intent(this, DownloadService.class);
            svc.setAction(DownloadService.ACTION_ADD);
            svc.putExtra("url", out.getAbsolutePath());
            svc.putExtra("name", title);
            startForegroundService(svc);
            Toast.makeText(this, "已加入：" + title, Toast.LENGTH_SHORT).show();
        } catch (Exception e) {
            Toast.makeText(this, "读不到这个文件：" + e.getMessage(), Toast.LENGTH_SHORT).show();
        }
    }

    private final Runnable render = new Runnable() {
        @Override
        public void run() {
            List<Job> jobs;
            synchronized (DownloadService.JOBS) {
                jobs = new ArrayList<>(DownloadService.JOBS);
            }
            // BT 任务的真实进度在 Go 引擎里，拉回来同步到影子任务
            for (Job j : jobs) {
                if (!"bt".equals(j.kind) || j.btId == null) continue;
                try {
                    String raw = Xflowmobile.status(j.btId);
                    if (raw == null || raw.isEmpty()) continue;
                    JSONObject o = new JSONObject(raw);
                    String nm = o.optString("name", "");
                    if (!nm.isEmpty()) j.name = nm;
                    j.total = o.optLong("total", j.total);
                    j.done = o.optLong("done", j.done);
                    j.speed = o.optLong("speed", 0);
                    j.err = o.optString("err", "");
                    String st = o.optString("state", "");
                    if (!st.isEmpty()) j.state = st;
                    if (j.path.isEmpty() && j.total > 0) j.path = "";
                } catch (Throwable ignored) {
                }
            }
            int active = 0;
            long speed = 0;
            for (Job j : jobs) {
                if ("downloading".equals(j.state)) {
                    active++;
                    speed += j.speed;
                }
            }
            if ("失败".length() > 0 && DownloadService.btEngineState.startsWith("失败")) {
                statText.setText(jobs.isEmpty() ? "BT 引擎异常 · " + DownloadService.btEngineState
                                                : DownloadService.btEngineState);
            } else if (jobs.isEmpty()) {
                statText.setText("还没有任务");
            } else {
                String s = jobs.size() + " 个任务";
                if (active > 0) {
                    s += " · " + active + " 个下载中 · " + DownloadService.human(speed) + "/s";
                }
                statText.setText(s);
            }

            // 数量变了就重建，否则原地更新
            if (jobList.getChildCount() != jobs.size()) {
                jobList.removeAllViews();
                for (Job j : jobs) jobList.addView(buildRow(j));
            } else {
                for (int i = 0; i < jobs.size(); i++) {
                    updateRow(jobList.getChildAt(i), jobs.get(i));
                }
            }
            ui.postDelayed(this, 600);
        }
    };

    private View buildRow(Job j) {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundResource(R.drawable.card_bg);
        box.setPadding(dp(12), dp(10), dp(12), dp(10));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(8);
        box.setLayoutParams(lp);

        TextView name = new TextView(this);
        name.setTextColor(0xFFE8EBF2);
        name.setTextSize(14);
        name.setText(j.name);
        name.setSingleLine(true);
        box.addView(name);

        TextView meta = new TextView(this);
        meta.setTextColor(0xFF8B93A7);
        meta.setTextSize(12);
        meta.setPadding(0, dp(3), 0, dp(6));
        box.addView(meta);

        ProgressBar pb = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        pb.setMax(100);
        pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF4CC2FF));
        pb.setProgressBackgroundTintList(android.content.res.ColorStateList.valueOf(0xFF0B0E13));
        box.addView(pb, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(6)));

        // 分段条：每段一格，能看出哪段在跑、哪段卡住（跟桌面版一样分段看进度）
        LinearLayout segBar = new LinearLayout(this);
        segBar.setOrientation(LinearLayout.HORIZONTAL);
        LinearLayout.LayoutParams segLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(5));
        segLp.topMargin = dp(3);
        box.addView(segBar, segLp);

        // 操作按钮
        LinearLayout ops = new LinearLayout(this);
        ops.setOrientation(LinearLayout.HORIZONTAL);
        ops.setPadding(0, dp(8), 0, 0);
        Button pause = smallButton("暂停");
        Button cancel = smallButton("删除");
        Button open = smallButton("打开");
        ops.addView(pause);
        ops.addView(cancel);
        ops.addView(open);
        box.addView(ops);

        pause.setOnClickListener(v -> {
            Intent i = new Intent(this, DownloadService.class);
            i.putExtra("id", j.id);
            i.setAction("downloading".equals(j.state)
                    ? DownloadService.ACTION_PAUSE
                    : DownloadService.ACTION_RESUME);
            startForegroundService(i);
        });

        cancel.setOnClickListener(v -> {
            Intent i = new Intent(this, DownloadService.class);
            i.putExtra("id", j.id);
            i.setAction(DownloadService.ACTION_CANCEL);
            startForegroundService(i);
            if ("done".equals(j.state) || "error".equals(j.state)) {
                Toast.makeText(this, "任务已删除，文件留在 Download 目录", Toast.LENGTH_SHORT).show();
            } else {
                Toast.makeText(this, "已删除，没下完的部分也清掉了", Toast.LENGTH_SHORT).show();
            }
        });

        open.setOnClickListener(v -> openFile(j));

        box.setTag(new Object[]{meta, pb, pause, open, segBar, cancel});
        updateRow(box, j);
        return box;
    }

    private Button smallButton(String text) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextSize(12);
        b.setAllCaps(false);
        b.setBackgroundResource(R.drawable.btn_small_bg);
        b.setTextColor(0xFFC9D2E3);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                0, dp(34), 1f);
        lp.rightMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    private void updateRow(View box, Job j) {
        Object[] tag = (Object[]) box.getTag();
        if (tag == null) return;
        TextView meta = (TextView) tag[0];
        ProgressBar pb = (ProgressBar) tag[1];
        Button pause = (Button) tag[2];
        Button open = (Button) tag[3];
        LinearLayout segBar = tag.length > 4 ? (LinearLayout) tag[4] : null;
        Button cancelBtn = tag.length > 5 ? (Button) tag[5] : null;

        pb.setProgress(j.percent());
        String state = j.state;
        switch (state) {
            case "downloading":
                state = "下载中";
                break;
            case "done":
                state = "已完成";
                break;
            case "error":
                state = "出错";
                break;
            case "paused":
                state = "已暂停";
                break;
            case "queued":
                state = "排队中";
                break;
            default:
                break;
        }
        StringBuilder sb = new StringBuilder(state);
        if (j.total > 0) {
            sb.append(" ").append(j.percent()).append("% · ")
              .append(DownloadService.human(j.done)).append(" / ")
              .append(DownloadService.human(j.total));
        } else if (j.done > 0) {
            sb.append(" · ").append(DownloadService.human(j.done));
        }
        if ("downloading".equals(j.state) && j.speed > 0) {
            sb.append(" · ").append(DownloadService.human(j.speed)).append("/s");
        }
        if ("bt".equals(j.kind)) {
            sb.append(" · BT");
        } else if ("downloading".equals(j.state) && j.workers > 0) {
            // 自适应的时候段数是死的（16 份），真正在变的是并发线程数，就显示它
            sb.append(" · ").append(j.workers).append(" 并发");
        } else if (j.segs.size() > 1) {
            sb.append(" · ").append(j.segs.size()).append(" 段");
        }
        if (!j.err.isEmpty()) sb.append(" · ").append(j.err);
        meta.setText(sb.toString());

        if ("done".equals(j.state)) {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF3DDC84));
        } else if ("error".equals(j.state)) {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFFE5534B));
        } else {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF4CC2FF));
        }

        // 分段条：每段一小格，满了变绿，正在跑的是蓝色
        if (segBar != null) {
            try {
                int n = j.segs.size();
                if (n <= 1) {
                    segBar.setVisibility(View.GONE);
                } else {
                    segBar.setVisibility(View.VISIBLE);
                    if (segBar.getChildCount() != n) {
                        segBar.removeAllViews();
                        for (int i = 0; i < n; i++) {
                            ProgressBar sp = new ProgressBar(this, null,
                                    android.R.attr.progressBarStyleHorizontal);
                            sp.setMax(100);
                            sp.setProgressBackgroundTintList(
                                    android.content.res.ColorStateList.valueOf(0xFF0B0E13));
                            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0, dp(5), 1f);
                            lp.rightMargin = (i == n - 1) ? 0 : dp(2);
                            segBar.addView(sp, lp);
                        }
                    }
                    for (int i = 0; i < n; i++) {
                        Segment sg = j.segs.get(i);
                        ProgressBar sp = (ProgressBar) segBar.getChildAt(i);
                        int pct = sg.size() <= 0 ? 100 : (int) (sg.done * 100 / sg.size());
                        sp.setProgress(Math.max(0, Math.min(100, pct)));
                        sp.setProgressTintList(android.content.res.ColorStateList.valueOf(
                                pct >= 100 ? 0xFF3DDC84 : 0xFF4CC2FF));
                    }
                }
            } catch (IndexOutOfBoundsException ignored) {
                // pause/resume 时段表可能正在重建，下一帧再说
            }
        }

        pause.setText("downloading".equals(j.state) || "queued".equals(j.state) ? "暂停" : "继续");
        pause.setEnabled(!"done".equals(j.state));
        // 统一叫「删除」（清秋 2026-11-10：取消改叫删除）
        if (cancelBtn != null) {
            cancelBtn.setText("删除");
        }
        open.setEnabled("done".equals(j.state) && !j.path.isEmpty());
    }

    private void openFile(Job j) {
        try {
            File f = new File(j.path);
            if (!f.exists()) {
                Toast.makeText(this, "文件还没落地", Toast.LENGTH_SHORT).show();
                return;
            }
            Uri uri = Uri.parse("content://" + getPackageName() + ".fileprovider" + f.getAbsolutePath());
            Intent i = new Intent(Intent.ACTION_VIEW);
            i.setDataAndType(uri, guessMime(f.getName()));
            i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            startActivity(Intent.createChooser(i, "打开方式"));
        } catch (Exception e) {
            Toast.makeText(this, "打不开：" + e.getMessage(), Toast.LENGTH_SHORT).show();
        }
    }

    private String guessMime(String name) {
        String n = name.toLowerCase();
        if (n.endsWith(".mp4") || n.endsWith(".mkv")) return "video/*";
        if (n.endsWith(".mp3") || n.endsWith(".flac")) return "audio/*";
        if (n.endsWith(".jpg") || n.endsWith(".png")) return "image/*";
        if (n.endsWith(".pdf")) return "application/pdf";
        if (n.endsWith(".apk")) return "application/vnd.android.package-archive";
        if (n.endsWith(".zip")) return "application/zip";
        return "*/*";
    }

    private int dp(int v) {
        return (int) (v * getResources().getDisplayMetrics().density);
    }
}
