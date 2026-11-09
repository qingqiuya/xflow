package com.qingqiu.xflow;

import android.app.Activity;
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

import java.io.File;
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

        Button add = findViewById(R.id.addBtn);
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
        box.setBackgroundColor(0xFF171A21);
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

        // 操作按钮
        LinearLayout ops = new LinearLayout(this);
        ops.setOrientation(LinearLayout.HORIZONTAL);
        ops.setPadding(0, dp(8), 0, 0);
        Button pause = smallButton("暂停");
        Button cancel = smallButton("取消");
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
        });

        open.setOnClickListener(v -> openFile(j));

        box.setTag(new Object[]{meta, pb, pause, open});
        updateRow(box, j);
        return box;
    }

    private Button smallButton(String text) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextSize(12);
        b.setAllCaps(false);
        b.setBackgroundColor(0xFF22262F);
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
        if (j.segs.size() > 1) sb.append(" · ").append(j.segs.size()).append(" 段");
        if (!j.err.isEmpty()) sb.append(" · ").append(j.err);
        meta.setText(sb.toString());

        if ("done".equals(j.state)) {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF3DDC84));
        } else if ("error".equals(j.state)) {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFFE5534B));
        } else {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF4CC2FF));
        }

        pause.setText("downloading".equals(j.state) || "queued".equals(j.state) ? "暂停" : "继续");
        pause.setEnabled(!"done".equals(j.state));
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
