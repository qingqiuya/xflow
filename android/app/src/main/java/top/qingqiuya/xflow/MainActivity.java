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
    private ApiServer api;
    private Beacon beacon;
    private LinearLayout jobList;
    private TextView statText;
    private final Handler ui = new Handler(Looper.getMainLooper());

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        // 状态栏/导航栏都刷成同一个底色
        android.view.Window win = getWindow();
        win.setStatusBarColor(0xFF0b0d12);
        win.setNavigationBarColor(0xFF0b0d12);
        if (android.os.Build.VERSION.SDK_INT >= 29) {
            win.setNavigationBarDividerColor(0xFF0b0d12);
            win.setStatusBarContrastEnforced(false);
            win.setNavigationBarContrastEnforced(false);
        }
        setContentView(R.layout.activity_main);

        // ---- 手机端 API（默认关，点右上角 API 打开）
        final android.content.SharedPreferences prefs =
                getSharedPreferences("xflow", MODE_PRIVATE);
        api = new ApiServer(this);
        beacon = new Beacon(this);
        try {
            beacon.setSelfName(android.os.Build.MODEL == null ? "android" : android.os.Build.MODEL);
        } catch (Exception ignored) {
        }
        final TextView apiChip = findViewById(R.id.apiChip);
        boolean apiOn = prefs.getBoolean("api_enabled", false);
        if (apiOn) api.start(ApiServer.DEFAULT_PORT);
        // Android 16 起「访问局域网」是运行时权限，不请求的话 mDNS 会报
        // SecurityException: Missing local network permission
        if (android.os.Build.VERSION.SDK_INT >= 36) {
            try {
                requestPermissions(new String[]{"android.permission.ACCESS_LOCAL_NETWORK"}, 9001);
            } catch (Exception ignored) {
            }
        }
        // 广播常开：只告诉别人「我在这、叫什么、HTTP 端口多少」，
        // 不加任务不动数据，所以不用等用户点开关
        beacon.start(ApiServer.DEFAULT_PORT);
        paintApiChip(apiChip, apiOn);
        apiChip.setOnClickListener(v -> {
            boolean now = !api.isRunning();
            if (now) {
                api.start(ApiServer.DEFAULT_PORT);
                if (beacon != null && !beacon.isRunning()) beacon.start(ApiServer.DEFAULT_PORT);
                if (!api.isRunning()) {
                    Toast.makeText(this, "API 起不来（端口被占？）", Toast.LENGTH_SHORT).show();
                    paintApiChip(apiChip, false);
                    return;
                }
            } else {
                api.stop();   // 广播留着，只是不接受操作
            }
            prefs.edit().putBoolean("api_enabled", now).apply();
            paintApiChip(apiChip, now);
            Toast.makeText(this, now ? "API 已开：http://" + localIP() + ":" + ApiServer.DEFAULT_PORT
                    : "API 已关", Toast.LENGTH_SHORT).show();
        });

        // ---- 清空历史
        TextView clearBtn = findViewById(R.id.clearBtn);
        clearBtn.setOnClickListener(v -> {
            int n = 0;
            synchronized (DownloadService.JOBS) {
                for (int i = DownloadService.JOBS.size() - 1; i >= 0; i--) {
                    Job j = DownloadService.JOBS.get(i);
                    if ("done".equals(j.state) || "error".equals(j.state)) {
                        DownloadService.JOBS.remove(i);
                        n++;
                    }
                }
            }
            Toast.makeText(this, n > 0 ? "清掉了 " + n + " 条历史" : "没有可清的历史",
                    Toast.LENGTH_SHORT).show();
            ui.post(render);
        });

        // Android 15 上手势导航会强制 edge-to-edge，只设 navigationBarColor 不管用，
        // 底部会留一条黑。这里手动让内容铺到全屏，再用 insets 把控件推回安全区，
        // 这样背景（以后换背景图也一样）就一路铺到底。
        // FLAG_LAYOUT_NO_LIMITS：让窗口真的铺到状态栏/手势条下面。
        // 只靠 setDecorFitsSystemWindows(false) 在 MIUI 上不生效，底部会留一条纯黑。
        win.addFlags(android.view.WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS);
        if (android.os.Build.VERSION.SDK_INT >= 30) {
            win.setDecorFitsSystemWindows(false);
            final android.view.View root = findViewById(android.R.id.content);
            root.setOnApplyWindowInsetsListener((v, ins) -> {
                android.graphics.Insets bars = ins.getInsets(
                        android.view.WindowInsets.Type.systemBars() |
                        android.view.WindowInsets.Type.displayCutout());
                // 底部不缩：让背景图一路铺到手势条底下（不然那里会留一条黑）
                v.setPadding(bars.left, bars.top, bars.right, 0);
                // 可滚动区域自己加上底部安全距离，避免最后一条任务被手势条压住
                android.view.View sv = findViewById(R.id.jobScroll);
                if (sv != null) {
                    sv.setPadding(sv.getPaddingLeft(), sv.getPaddingTop(),
                            sv.getPaddingRight(), bars.bottom + dp(16));
                }
                return ins;
            });
        }
        urlEdit = findViewById(R.id.urlEdit);
        nameEdit = findViewById(R.id.nameEdit);
        jobList = findViewById(R.id.jobList);
        statText = findViewById(R.id.statText);

        // 标题用蓝→绿渐变（跟桌面版 .brand b 一个色）
        TextView title = findViewById(R.id.titleText);
        if (title != null) {
            title.post(() -> {
                float w = title.getPaint().measureText(title.getText().toString());
                android.graphics.Shader sh = new android.graphics.LinearGradient(
                        0, 0, Math.max(w, 1), 0,
                        0xFF5CC9FF, 0xFF3DDC84,
                        android.graphics.Shader.TileMode.CLAMP);
                title.getPaint().setShader(sh);
                title.invalidate();
            });
        }

        // 版本号单独放在右上角 chip 里，点它查更新；副标题点开看许可
        TextView sub = findViewById(R.id.subText);
        if (sub != null) sub.setOnClickListener(v -> showLicenses());
        TextView verText = findViewById(R.id.verText);
        String myVer = "";
        try {
            myVer = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception ignored) {
        }
        if (verText != null) {
            verText.setText("v" + myVer);
            final String vv = myVer;
            verText.setOnClickListener(v -> checkUpdate(vv));
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

    /** 发现的设备：显示名字 + IP，点一下就复制地址 */
    private void renderPeers() {
        LinearLayout box = findViewById(R.id.peerList);
        if (box == null) return;
        java.util.List<Beacon.Peer> list = Beacon.alive();
        if (list.isEmpty()) {
            box.setVisibility(View.GONE);
            return;
        }
        box.setVisibility(View.VISIBLE);
        if (box.getChildCount() != list.size()) {
            box.removeAllViews();
            for (Beacon.Peer p : list) {
                TextView tv = new TextView(this);
                tv.setTextSize(11.5f);
                tv.setTextColor(0xFF8B93A7);
                tv.setPadding(0, dp(2), 0, dp(2));
                tv.setText("· " + (p.name.isEmpty() ? p.ip : p.name) + "  " + p.ip + ":" + p.port);
                final String addr = "http://" + p.ip + ":" + p.port;
                tv.setOnClickListener(v -> {
                    android.content.ClipboardManager cm =
                            (android.content.ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
                    cm.setPrimaryClip(android.content.ClipData.newPlainText("XFlow", addr));
                    Toast.makeText(this, "已复制 " + addr, Toast.LENGTH_SHORT).show();
                });
                box.addView(tv);
            }
        } else {
            for (int i = 0; i < list.size(); i++) {
                Beacon.Peer p = list.get(i);
                ((TextView) box.getChildAt(i)).setText(
                        "· " + (p.name.isEmpty() ? p.ip : p.name) + "  " + p.ip + ":" + p.port);
            }
        }
    }

    private void paintApiChip(TextView chip, boolean on) {
        if (chip == null) return;
        chip.setText(on ? "API 开" : "API");
        chip.setTextColor(on ? 0xFF3DDC84 : 0xFF5B6478);
        chip.setBackgroundResource(R.drawable.chip_bg);
    }

    private String localIP() {
        try {
            java.util.Enumeration<java.net.NetworkInterface> en =
                    java.net.NetworkInterface.getNetworkInterfaces();
            while (en.hasMoreElements()) {
                java.net.NetworkInterface ni = en.nextElement();
                if (ni.isLoopback() || !ni.isUp()) continue;
                java.util.Enumeration<java.net.InetAddress> addrs = ni.getInetAddresses();
                while (addrs.hasMoreElements()) {
                    java.net.InetAddress a = addrs.nextElement();
                    if (a instanceof java.net.Inet4Address && !a.isLoopbackAddress()) {
                        return a.getHostAddress();
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return "本机IP";
    }

    // ---------------------------------------------------------------- 检查更新
    //
    // 查 GitHub 的 latest release，比较 tag 和本地 versionName。
    // 国内网络可能连不上，失败就静默（点一下没反应比弹错误好）。

    private void checkUpdate(String myVer) {
        Toast.makeText(this, "检查中…", Toast.LENGTH_SHORT).show();
        new Thread(() -> {
            String tag = null, html = null, err = null;
            try {
                java.net.HttpURLConnection c = (java.net.HttpURLConnection)
                        new java.net.URL("https://api.github.com/repos/qingqiuya/xflow/releases/latest")
                                .openConnection();
                c.setRequestProperty("User-Agent", "XFlow/" + myVer);
                c.setRequestProperty("Accept", "application/vnd.github+json");
                c.setConnectTimeout(8000);
                c.setReadTimeout(8000);
                if (c.getResponseCode() == 200) {
                    StringBuilder sb = new StringBuilder();
                    try (java.io.BufferedReader br = new java.io.BufferedReader(
                            new java.io.InputStreamReader(c.getInputStream(), "UTF-8"))) {
                        String line;
                        while ((line = br.readLine()) != null) sb.append(line);
                    }
                    org.json.JSONObject o = new org.json.JSONObject(sb.toString());
                    tag = o.optString("tag_name", "").replaceFirst("^v", "");
                    html = o.optString("html_url", "");
                } else {
                    err = "HTTP " + c.getResponseCode();
                }
            } catch (Exception e) {
                err = e.getMessage();
            }
            final String t = tag, h = html, er = err;
            ui.post(() -> {
                if (t == null || t.isEmpty()) {
                    Toast.makeText(this, "查不到更新（" + (er == null ? "网络" : er) + "）", Toast.LENGTH_SHORT).show();
                    return;
                }
                if (t.equals(myVer)) {
                    Toast.makeText(this, "已经是最新版本 v" + myVer, Toast.LENGTH_SHORT).show();
                    return;
                }
                new AlertDialog.Builder(this)
                        .setTitle("发现新版本 v" + t)
                        .setMessage("当前 v" + myVer + " → 最新 v" + t + "\n\n要用浏览器打开下载页吗？")
                        .setPositiveButton("去下载", (d, w) -> {
                            try {
                                startActivity(new Intent(Intent.ACTION_VIEW,
                                        android.net.Uri.parse(h)));
                            } catch (Exception ignored) {
                            }
                        })
                        .setNegativeButton("算了", null)
                        .show();
            });
        }).start();
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

            renderPeers();

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
        box.setPadding(dp(14), dp(12), dp(14), dp(11));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(10);
        box.setLayoutParams(lp);

        // 第一行：文件名 + 状态徽章
        LinearLayout top = new LinearLayout(this);
        top.setOrientation(LinearLayout.HORIZONTAL);
        top.setGravity(android.view.Gravity.CENTER_VERTICAL);

        TextView name = new TextView(this);
        name.setTextColor(0xFFE8EBF2);
        name.setTextSize(14.5f);
        name.setText(j.name);
        name.setSingleLine(true);
        name.setEllipsize(android.text.TextUtils.TruncateAt.MIDDLE);
        top.addView(name, new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f));

        TextView badge = new TextView(this);
        badge.setTextSize(10.5f);
        badge.setPadding(dp(7), dp(2), dp(7), dp(2));
        badge.setBackgroundResource(R.drawable.chip_bg);
        LinearLayout.LayoutParams blp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        blp.leftMargin = dp(8);
        top.addView(badge, blp);
        box.addView(top);

        // 第二行：元信息（速度、进度、并发）
        TextView meta = new TextView(this);
        meta.setTextColor(0xFF8B93A7);
        meta.setTextSize(11.5f);
        meta.setPadding(0, dp(5), 0, dp(9));
        box.addView(meta);

        // 进度条：蓝→绿渐变
        ProgressBar pb = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        pb.setMax(100);
        pb.setProgressDrawable(getResources().getDrawable(R.drawable.progress_fill, null));
        box.addView(pb, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(7)));

        // 分段条
        LinearLayout segBar = new LinearLayout(this);
        segBar.setOrientation(LinearLayout.HORIZONTAL);
        LinearLayout.LayoutParams segLp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(4));
        segLp.topMargin = dp(5);
        box.addView(segBar, segLp);

        // 操作行：右对齐小按钮
        LinearLayout ops = new LinearLayout(this);
        ops.setOrientation(LinearLayout.HORIZONTAL);
        ops.setGravity(android.view.Gravity.END);
        ops.setPadding(0, dp(10), 0, 0);
        Button pause = smallButton("暂停");
        Button cancel = smallButton("删除");
        Button open = smallButton("打开");
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, dp(32));
        bp.leftMargin = dp(7);
        pause.setLayoutParams(new LinearLayout.LayoutParams(bp));
        cancel.setLayoutParams(new LinearLayout.LayoutParams(bp));
        open.setLayoutParams(new LinearLayout.LayoutParams(bp));
        pause.setPadding(dp(16), 0, dp(16), 0);
        cancel.setPadding(dp(16), 0, dp(16), 0);
        open.setPadding(dp(16), 0, dp(16), 0);
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

        box.setTag(new Object[]{meta, pb, pause, open, segBar, cancel, badge});
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
        TextView badge = tag.length > 6 ? (TextView) tag[6] : null;

        pb.setProgress(j.percent());
        String state = j.state;
        // 徽章配色：完成绿、出错红、下载中蓝、暂停灰
        if (badge != null) {
            switch (j.state) {
                case "done":
                    badge.setText("完成");
                    badge.setTextColor(0xFF3DDC84);
                    break;
                case "error":
                    badge.setText("出错");
                    badge.setTextColor(0xFFE5534B);
                    break;
                case "paused":
                    badge.setText("暂停");
                    badge.setTextColor(0xFF8B93A7);
                    break;
                case "queued":
                    badge.setText("排队");
                    badge.setTextColor(0xFFB58BE0);
                    break;
                default:
                    badge.setText("下载中");
                    badge.setTextColor(0xFF4CC2FF);
                    break;
            }
        }
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
        } else if ("paused".equals(j.state)) {
            pb.setProgressTintList(android.content.res.ColorStateList.valueOf(0xFF5B6478));
        } else {
            pb.setProgressTintList(null);   // null = 用 drawable 里的蓝→绿渐变
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

    @Override
    protected void onDestroy() {
        if (isFinishing()) {
            if (api != null) api.stop();
            if (beacon != null) beacon.stop();
        }
        super.onDestroy();
    }
}
