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
    private LocalSendServer ls;
    private Beacon beacon;
    private SharePanel sharePanel;
    private int curPage = 0;
    private boolean shareTabOn = true;
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
        // 令牌：第一次生成后存起来，重启不变；电脑那边存一次就能一直用
        String tok = prefs.getString("api_token", "");
        if (tok.isEmpty()) {
            StringBuilder sb = new StringBuilder();
            java.util.Random r = new java.util.Random();
            for (int i = 0; i < 24; i++) {
                sb.append("0123456789abcdef".charAt(r.nextInt(16)));
            }
            tok = sb.toString();
            prefs.edit().putString("api_token", tok).apply();
        }
        api.setToken(tok);
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
        // LocalSend v2 兼容层（53317）：装了 LocalSend 的设备能直接发文件给这台手机。
        // 端口感 53317 被真 LocalSend 占了就自己安静退出（见 LocalSendServer.start）。
        try {
            ls = new LocalSendServer(this);
            ls.start();
        } catch (Exception ignored) {
        }
        paintApiChip(apiChip, apiOn);
        // 互传开关（跟 API 一样能关）
        final TextView shareChip = findViewById(R.id.shareChip);
        boolean shareOn = prefs.getBoolean("share_enabled", true);
        api.setShareEnabled(shareOn);
        paintShareChip(shareChip, shareOn);
        applyShareTab(shareOn);
        if (shareChip != null) {
            shareChip.setOnClickListener(v -> {
                boolean now = !prefs.getBoolean("share_enabled", true);
                prefs.edit().putBoolean("share_enabled", now).apply();
                api.setShareEnabled(now);
                paintShareChip(shareChip, now);
                applyShareTab(now);
                Toast.makeText(this, now ? "互传已开启" : "互传已关闭（别人传不进来）",
                        Toast.LENGTH_SHORT).show();
                if (!now) animateToPage(0);
            });
        }

        // 两页：下载 / 互传（点页签或左右滑）
        TextView tabDownload = findViewById(R.id.tabDownload);
        TextView tabShare = findViewById(R.id.tabShare);
        if (tabDownload != null) tabDownload.setOnClickListener(v -> showPage(0));
        if (tabShare != null) tabShare.setOnClickListener(v -> showPage(1));
        initPager();
        // 键盘遮挡兜底：Android 15+ 的 edge-to-edge 会让 adjustResize 失效，
        // 这里自己按 IME 高度给内容区加底部内边距（键盘收起就还回去）
        {
            final View content = findViewById(android.R.id.content);
            if (content != null) {
                content.setOnApplyWindowInsetsListener((v, insets) -> {
                    int ime = insets.getSystemWindowInsetBottom();
                    int pad = ime > dp(120) ? ime : 0;
                    if (v.getPaddingBottom() != pad) {
                        v.setPadding(v.getPaddingLeft(), v.getPaddingTop(), v.getPaddingRight(), pad);
                    }
                    return insets;
                });
            }
        }
        // 互传面板挂上（两页都在同一棵树里，先把 listener 装好）
        {
            View panel = findViewById(R.id.sharePanel);
            if (panel != null) sharePanel = new SharePanel(this, panel);
        }
        apiChip.setOnClickListener(v -> {
            if (!api.isRunning()) {
                api.start(ApiServer.DEFAULT_PORT);
                if (!api.isRunning()) {
                    Toast.makeText(this, "API 起不来（端口被占？）", Toast.LENGTH_SHORT).show();
                    paintApiChip(apiChip, false);
                    return;
                }
                prefs.edit().putBoolean("api_enabled", true).apply();
                paintApiChip(apiChip, true);
                Toast.makeText(this, "API 已开：http://" + localIP() + ":" + ApiServer.DEFAULT_PORT,
                        Toast.LENGTH_SHORT).show();
            } else {
                showApiDoc();
            }
        });

        // 长按 API：切换开关
        if (apiChip != null) {
            apiChip.setOnLongClickListener(v -> {
                boolean now = !api.isRunning();
                if (now) {
                    api.start(ApiServer.DEFAULT_PORT);
                    if (!api.isRunning()) {
                        Toast.makeText(this, "API 起不来（端口被占？）", Toast.LENGTH_SHORT).show();
                        paintApiChip(apiChip, false);
                        return true;
                    }
                } else {
                    api.stop();
                }
                prefs.edit().putBoolean("api_enabled", now).apply();
                paintApiChip(apiChip, now);
                Toast.makeText(this, now ? "API 已开" : "API 已关", Toast.LENGTH_SHORT).show();
                return true;
            });
        }

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
        String mv = "";
        try {
            mv = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception ignored) {
        }
        final String myVer = mv;
        // 启动 4 秒后自动查一次更新（静默：只在有新版本时把版本号变成可点的提示）
        ui.postDelayed(() -> { if (verText != null) checkUpdate(myVer, true); }, 4000);
        if (verText != null) {
            verText.setText("v" + myVer);
            verText.setOnClickListener(v -> checkUpdate(myVer, false));
        }

        // B 站登录小圆片：登录后变「B站✓」，点它换号/退出
        TextView biliChip = findViewById(R.id.biliChip);
        if (biliChip != null) {
            refreshBiliChip(biliChip);
            biliChip.setOnClickListener(v -> BiliLogin.showLogin(this, () -> refreshBiliChip(biliChip)));
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

    /** API 用法说明：自己搭布局，跟 App 的深色风格对齐（系统弹窗是灰底） */
    private void showApiDoc() {
        String ip = localIP();
        String ip6 = localIP6();
        int port = ApiServer.DEFAULT_PORT;
        boolean on = api != null && api.isRunning();
        String base = "http://" + ip + ":" + port;
        String tok = api != null ? api.getToken() : "";

        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF12151c);
        box.setPadding(dp(20), dp(18), dp(20), dp(10));

        TextView title = new TextView(this);
        title.setText("手机端 API");
        title.setTextColor(0xFFE8EBF2);
        title.setTextSize(17);
        title.setTypeface(null, android.graphics.Typeface.BOLD);
        box.addView(title);

        TextView state = new TextView(this);
        state.setText(on ? "已开启" : "未开启 · 点一下 API 按钮打开");
        state.setTextColor(on ? 0xFF3DDC84 : 0xFF8B93A7);
        state.setTextSize(12);
        state.setPadding(0, dp(4), 0, dp(12));
        box.addView(state);

        // 地址一行，可点复制（有 IPv6 就一起显示）
        TextView addr = new TextView(this);
        addr.setText(ip6.isEmpty() ? base : base + "\n[" + ip6 + "]:" + port);
        addr.setTextColor(0xFF5CC9FF);
        addr.setTextSize(13.5f);
        addr.setTypeface(android.graphics.Typeface.MONOSPACE);
        addr.setBackgroundResource(R.drawable.chip_bg);
        addr.setPadding(dp(10), dp(9), dp(10), dp(9));
        addr.setOnClickListener(v -> {
            android.content.ClipboardManager cm =
                    (android.content.ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            cm.setPrimaryClip(android.content.ClipData.newPlainText("XFlow", base));
            Toast.makeText(this, "已复制地址", Toast.LENGTH_SHORT).show();
        });
        box.addView(addr, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));

        TextView body = new TextView(this);
        body.setTextColor(0xFFC9D2E3);
        body.setTextSize(11.5f);
        body.setTypeface(android.graphics.Typeface.MONOSPACE);
        body.setTextIsSelectable(true);
        body.setPadding(0, dp(14), 0, dp(6));
        body.setText(
                "令牌（每个请求都要带）\n  " + tok + "\n\n" +
                "GET  /api/version\n" +
                "GET  /api/jobs\n" +
                "POST /api/add?url=<链接>&name=<名字>\n" +
                "POST /api/clear\n" +
                "POST /api/job/<id>?action=pause|resume|delete\n\n" +
                "curl -H \"X-API-Token: " + tok + "\" " + base + "/api/jobs\n" +
                "curl -X POST -H \"X-API-Token: " + tok + "\" \\\n" +
                "     \"" + base + "/api/add?url=...\"\n\n" +
                "也可以把令牌放进 URL：?token=" + tok.substring(0, 6) + "...\n" +
                "局域网内其余设备没有令牌就调不动。");
        android.widget.ScrollView sv = new android.widget.ScrollView(this);
        sv.addView(body);
        LinearLayout.LayoutParams slp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f);
        box.addView(sv, slp);

        Button ok = new Button(this);
        ok.setText("知道了");
        ok.setTextColor(0xFF04121C);
        ok.setTextSize(14);
        ok.setBackgroundResource(R.drawable.btn_bg);
        ok.setStateListAnimator(null);
        LinearLayout.LayoutParams blp = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(46));
        blp.topMargin = dp(10);
        box.addView(ok, blp);

        final android.app.Dialog d = new android.app.Dialog(this);
        d.requestWindowFeature(android.view.Window.FEATURE_NO_TITLE);
        d.setContentView(box);
        android.view.Window w = d.getWindow();
        if (w != null) {
            w.setBackgroundDrawableResource(android.R.color.transparent);
            android.view.WindowManager.LayoutParams lp = w.getAttributes();
            lp.width = (int) (getResources().getDisplayMetrics().widthPixels * 0.88);
            lp.height = android.view.WindowManager.LayoutParams.WRAP_CONTENT;
            w.setAttributes(lp);
        }
        ok.setOnClickListener(v -> d.dismiss());
        d.show();
    }

    private void paintApiChip(TextView chip, boolean on) {
        if (chip == null) return;
        chip.setText(on ? "API 开" : "API");
        chip.setTextColor(on ? 0xFF3DDC84 : 0xFF5B6478);
        chip.setBackgroundResource(R.drawable.chip_bg);
    }

    /** 取本机第一个全局 IPv6（240e/2408 这类公网段，排除 fe80 链路本地） */
    private String localIP6() {
        try {
            java.util.Enumeration<java.net.NetworkInterface> en =
                    java.net.NetworkInterface.getNetworkInterfaces();
            while (en.hasMoreElements()) {
                java.net.NetworkInterface ni = en.nextElement();
                if (ni.isLoopback() || !ni.isUp()) continue;
                java.util.Enumeration<java.net.InetAddress> addrs = ni.getInetAddresses();
                while (addrs.hasMoreElements()) {
                    java.net.InetAddress a = addrs.nextElement();
                    if (a instanceof java.net.Inet6Address && !a.isLoopbackAddress()) {
                        String h = a.getHostAddress();
                        if (h.startsWith("fe80")) continue;   // 链路本地没用
                        int p = h.indexOf('%');
                        return p > 0 ? h.substring(0, p) : h;
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return "";
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

    @Override
    protected void onResume() {
        super.onResume();
        refreshBiliChip(findViewById(R.id.biliChip));
    }

    private void refreshBiliChip(TextView chip) {
        if (chip == null) return;
        if (BiliLogin.loggedIn(this)) {
            chip.setText("B站✓");
            chip.setTextColor(0xFF4CD964);
        } else {
            chip.setText("B站登录");
            chip.setTextColor(0xFF5b6478);
        }
    }

    private void checkUpdate(String myVer, boolean silent) {
        if (!silent) Toast.makeText(this, "检查中…", Toast.LENGTH_SHORT).show();
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
                    if (!silent) Toast.makeText(this, "查不到更新（" + (er == null ? "网络" : er) + "）", Toast.LENGTH_SHORT).show();
                    return;
                }
                if (!newerThan(t, myVer)) {
                    if (!silent) Toast.makeText(this, "已经是最新版本 v" + myVer, Toast.LENGTH_SHORT).show();
                    return;
                }
                if (silent) {
                    // 自动检查：不弹框，只把版本号变成「↑ v新版本」，点一下再问
                    final String tt = t;
                    TextView vt = findViewById(R.id.verText);
                    if (vt != null) {
                        vt.setText("↑ v" + tt);
                        vt.setTextColor(0xFF4CC2FF);
                        vt.setOnClickListener(v -> showUpdateDialog(myVer, tt, h));
                    }
                    return;
                }
                showUpdateDialog(myVer, t, h);
            });
        }).start();
    }

    /** remote 是否真的比 local 新（按点分数字比，别拿字符串相等当判断）。 */
    private static boolean newerThan(String remote, String local) {
        String[] a = String.valueOf(remote).split("\\.");
        String[] b = String.valueOf(local).split("\\.");
        for (int i = 0; i < Math.max(a.length, b.length); i++) {
            int x = i < a.length ? num(a[i]) : 0;
            int y = i < b.length ? num(b[i]) : 0;
            if (x != y) return x > y;
        }
        return false;
    }

    private static int num(String s) {
        try {
            return Integer.parseInt(String.valueOf(s).replaceAll("[^0-9]", ""));
        } catch (Exception e) {
            return 0;
        }
    }

    private void showUpdateDialog(String myVer, String t, String h) {
        new AlertDialog.Builder(this, R.style.DarkDialog)
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

        new AlertDialog.Builder(this, R.style.DarkDialog)
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
        if (req != REQ_TORRENT) {
            if (sharePanel != null) sharePanel.onPickResult(req, res, data); // 互传选完文件
            return;
        }
        if (res != RESULT_OK || data == null || data.getData() == null) return;
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
            Uri uri;
            String name = j.name;
            if (j.uri != null && j.uri.startsWith("content://")) {
                // 已经在系统「下载/XFlow」里（MediaStore），直接用它的 uri
                uri = Uri.parse(j.uri);
            } else {
                File f = new File(j.path);
                if (!f.exists()) {
                    Toast.makeText(this, "文件还没落地", Toast.LENGTH_SHORT).show();
                    return;
                }
                name = f.getName();
                uri = Uri.parse("content://" + getPackageName() + ".fileprovider" + f.getAbsolutePath());
            }
            Intent i = new Intent(Intent.ACTION_VIEW);
            i.setDataAndType(uri, guessMime(name));
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

    // ---------------- 两页滑动（跟手） ----------------
    private float pageProgress = 0f;   // 0 = 下载页，1 = 互传页
    private float downX, downY, startProgress;
    private boolean dragging;
    private int pagerW;
    private android.animation.ValueAnimator pageAnim;

    private void initPager() {
        final View pd = findViewById(R.id.pageDownload);
        if (pd == null) return;
        pd.post(() -> {
            View parent = (View) pd.getParent();
            pagerW = parent != null ? parent.getWidth() : pd.getWidth();
            pageProgress = 0f;
            applyProgress(0f);
            View ps = findViewById(R.id.pageShare);
            if (ps != null) ps.setVisibility(View.GONE);
        });
    }

    /** 把两页按进度摆位置：进度 0 时下载页占满、互传页在右边外面；1 时反过来。 */
    private void applyProgress(float p) {
        View pd = findViewById(R.id.pageDownload);
        View ps = findViewById(R.id.pageShare);
        if (pd == null || ps == null) return;
        if (pagerW == 0) {
            View parent = (View) pd.getParent();
            if (parent != null && parent.getWidth() > 0) pagerW = parent.getWidth();
            if (pagerW == 0) return;
        }
        pd.setVisibility(View.VISIBLE);
        ps.setVisibility(View.VISIBLE);
        pd.setTranslationX(-p * pagerW);
        ps.setTranslationX((1f - p) * pagerW);
        pd.setAlpha(1f - 0.2f * p);
        ps.setAlpha(0.8f + 0.2f * p);
    }

    /** 松手/点页签后吸附到某一页。 */
    private void animateToPage(final int page) {
        final int want = (page == 1 && !shareTabOn) ? 0 : page;
        final float target = want == 1 ? 1f : 0f;
        setTabStyle(want);
        if (pageAnim != null) pageAnim.cancel();
        pageAnim = android.animation.ValueAnimator.ofFloat(pageProgress, target);
        pageAnim.setDuration((long) (150 + 140 * Math.abs(target - pageProgress)));
        pageAnim.setInterpolator(new android.view.animation.DecelerateInterpolator());
        pageAnim.addUpdateListener(a -> {
            pageProgress = (float) a.getAnimatedValue();
            applyProgress(pageProgress);
        });
        pageAnim.addListener(new android.animation.AnimatorListenerAdapter() {
            @Override
            public void onAnimationEnd(android.animation.Animator a) {
                pageProgress = target;
                View pd = findViewById(R.id.pageDownload);
                View ps = findViewById(R.id.pageShare);
                if (target == 1f) {
                    if (pd != null) {
                        pd.setVisibility(View.GONE);
                        pd.setTranslationX(0f);
                        pd.setAlpha(1f);
                    }
                    if (ps != null) {
                        ps.setTranslationX(0f);
                        ps.setAlpha(1f);
                    }
                } else {
                    if (ps != null) {
                        ps.setVisibility(View.GONE);
                        ps.setTranslationX(0f);
                        ps.setAlpha(1f);
                    }
                    if (pd != null) {
                        pd.setTranslationX(0f);
                        pd.setAlpha(1f);
                    }
                }
            }
        });
        pageAnim.start();
    }

    private void showPage(int idx) {
        animateToPage(idx);
    }

    private void setTabStyle(int idx) {
        TextView td = findViewById(R.id.tabDownload);
        TextView ts = findViewById(R.id.tabShare);
        if (td != null) {
            td.setTextColor(idx == 0 ? 0xFFe8ebf2 : 0xFF6b7385);
            td.setTypeface(null, idx == 0 ? android.graphics.Typeface.BOLD : android.graphics.Typeface.NORMAL);
        }
        if (ts != null) {
            ts.setTextColor(idx == 1 ? 0xFFe8ebf2 : 0xFF6b7385);
            ts.setTypeface(null, idx == 1 ? android.graphics.Typeface.BOLD : android.graphics.Typeface.NORMAL);
        }
    }

    @Override
    public boolean dispatchTouchEvent(android.view.MotionEvent ev) {
        switch (ev.getActionMasked()) {
            case android.view.MotionEvent.ACTION_DOWN:
                downX = ev.getX();
                downY = ev.getY();
                startProgress = pageProgress;
                dragging = false;
                if (pageAnim != null && pageAnim.isRunning()) pageAnim.cancel();
                break;
            case android.view.MotionEvent.ACTION_MOVE: {
                float dx = ev.getX() - downX;
                float dy = ev.getY() - downY;
                if (!dragging) {
                    // 横向位移明显大于纵向才当翻页，别抢竖向滚动
                    if (Math.abs(dx) > dp(14) && Math.abs(dx) > Math.abs(dy) * 1.3f) {
                        dragging = true;
                    } else {
                        break;
                    }
                }
                if (pagerW == 0) break;
                float p = startProgress - dx / (float) pagerW;
                p = Math.max(0f, Math.min(1f, p));
                if (!shareTabOn) p = 0f;  // 互传关了：纹丝不动，一点都别露出来
                pageProgress = p;
                applyProgress(pageProgress);
                return true; // 跟手阶段把事件吃掉
            }
            case android.view.MotionEvent.ACTION_UP:
            case android.view.MotionEvent.ACTION_CANCEL: {
                if (!dragging) break;
                dragging = false;
                float dx = ev.getX() - downX;
                float p = pagerW > 0 ? (startProgress - dx / (float) pagerW) : startProgress;
                // 轻扫也算：位移超过 1/6 屏就往那页走
                boolean goShare = p > 0.5f - 0.17f && dx < 0 || p > 0.83f;
                boolean goDown = p < 0.5f + 0.17f && dx > 0 || p < 0.17f;
                int target = (p >= 0.5f) ? 1 : 0;
                if (dx < 0 && p > 0.34f) target = 1;
                if (dx > 0 && p < 0.66f) target = 0;
                if (!shareTabOn) target = 0;   // 互传关着就不许停在那一页
                animateToPage(target);
                return true;
            }
        }
        return super.dispatchTouchEvent(ev);
    }

    /** 互传 chip 的开/关配色，跟 API chip 一套。 */
    private void paintShareChip(TextView chip, boolean on) {
        if (chip == null) return;
        chip.setText(on ? "互传开" : "互传");
        chip.setTextColor(on ? 0xFF3ddc84 : 0xFF5b6478);
    }

    /** 关掉互传时把互传页签藏起来。 */
    private void applyShareTab(boolean on) {
        shareTabOn = on;
        TextView tab = findViewById(R.id.tabShare);
        if (tab != null) tab.setVisibility(on ? View.VISIBLE : View.GONE);
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
