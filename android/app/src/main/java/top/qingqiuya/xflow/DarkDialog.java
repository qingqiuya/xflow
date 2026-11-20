package top.qingqiuya.xflow;

import android.app.Activity;
import android.app.Dialog;
import android.graphics.Color;
import android.graphics.drawable.GradientDrawable;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.view.WindowManager;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * 深色确认弹窗。
 *
 * 为什么不直接 AlertDialog：系统弹窗是浅灰底 + Material 圆角，跟 App 的深蓝紫主题
 * 完全两个世界（2026-11-20 清秋：「你这删除弹窗是不是有点太复古了」）。
 * 这里自己搭，配色跟 showApiDoc / 互传面板一致。
 */
public class DarkDialog {

    public static void confirm(Activity act, String title, String message, String okText, Runnable onOk) {
        if (act == null || act.isFinishing()) return;
        final Dialog d = new Dialog(act);
        d.requestWindowFeature(Window.FEATURE_NO_TITLE);

        float den = act.getResources().getDisplayMetrics().density;
        int dp = (int) (den * 1 + 0.5f);

        LinearLayout box = new LinearLayout(act);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(20 * dp, 18 * dp, 20 * dp, 10 * dp);
        GradientDrawable bg = new GradientDrawable();
        bg.setColor(0xFF141821);
        bg.setCornerRadius(16 * dp);
        bg.setStroke(dp, 0xFF2A2E36);
        box.setBackground(bg);

        TextView t = new TextView(act);
        t.setText(title == null ? "" : title);
        t.setTextColor(0xFFE8ECF2);
        t.setTextSize(16.5f);
        t.setTypeface(null, android.graphics.Typeface.BOLD);
        box.addView(t);

        TextView m = new TextView(act);
        m.setText(message == null ? "" : message);
        m.setTextColor(0xFF9AA5B8);
        m.setTextSize(13f);
        m.setLineSpacing(0, 1.35f);
        LinearLayout.LayoutParams mlp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        mlp.topMargin = 10 * dp;
        box.addView(m, mlp);

        LinearLayout btns = new LinearLayout(act);
        btns.setOrientation(LinearLayout.HORIZONTAL);
        btns.setGravity(Gravity.END);
        LinearLayout.LayoutParams blp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        blp.topMargin = 18 * dp;
        btns.setLayoutParams(blp);

        TextView cancel = new TextView(act);
        cancel.setText("算了");
        cancel.setTextColor(0xFF8B93A7);
        cancel.setTextSize(14f);
        cancel.setPadding(18 * dp, 10 * dp, 18 * dp, 10 * dp);
        cancel.setOnClickListener(v -> d.dismiss());
        btns.addView(cancel);

        TextView ok = new TextView(act);
        ok.setText(okText == null ? "确定" : okText);
        ok.setTextColor(0xFF04121C);
        ok.setTextSize(14f);
        ok.setTypeface(null, android.graphics.Typeface.BOLD);
        ok.setPadding(24 * dp, 10 * dp, 24 * dp, 10 * dp);
        GradientDrawable okBg = new GradientDrawable();
        okBg.setColor(0xFF6FD3FF);
        okBg.setCornerRadius(10 * dp);
        ok.setBackground(okBg);
        LinearLayout.LayoutParams okLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        okLp.leftMargin = 10 * dp;
        ok.setLayoutParams(okLp);
        ok.setOnClickListener(v -> {
            d.dismiss();
            if (onOk != null) onOk.run();
        });
        btns.addView(ok);

        box.addView(btns);

        d.setContentView(box);
        Window w = d.getWindow();
        if (w != null) {
            w.setBackgroundDrawableResource(android.R.color.transparent);
            // 离屏幕边缘留点空，别贴边
            w.setLayout((int) (act.getResources().getDisplayMetrics().widthPixels * 0.86f),
                    ViewGroup.LayoutParams.WRAP_CONTENT);
            w.addFlags(WindowManager.LayoutParams.FLAG_DIM_BEHIND);
            WindowManager.LayoutParams lp = w.getAttributes();
            lp.dimAmount = 0.6f;
            w.setAttributes(lp);
        }
        d.show();
    }

    /** 带输入框的深色弹窗：用来改设备名这类「填一个值」的场合。 */
    public static void input(Activity act, String title, String hint, String preset, OnText onOk) {
        if (act == null || act.isFinishing()) return;
        final Dialog d = new Dialog(act);
        d.requestWindowFeature(Window.FEATURE_NO_TITLE);
        float den = act.getResources().getDisplayMetrics().density;
        int dp = (int) (den * 1 + 0.5f);

        LinearLayout box = new LinearLayout(act);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(20 * dp, 18 * dp, 20 * dp, 10 * dp);
        GradientDrawable bg = new GradientDrawable();
        bg.setColor(0xFF141821);
        bg.setCornerRadius(16 * dp);
        bg.setStroke(dp, 0xFF2A2E36);
        box.setBackground(bg);

        TextView t = new TextView(act);
        t.setText(title == null ? "" : title);
        t.setTextColor(0xFFE8ECF2);
        t.setTextSize(16.5f);
        t.setTypeface(null, android.graphics.Typeface.BOLD);
        box.addView(t);

        final android.widget.EditText et = new android.widget.EditText(act);
        et.setText(preset == null ? "" : preset);
        et.setHint(hint == null ? "" : hint);
        et.setSingleLine(true);
        et.setTextColor(0xFFE8ECF2);
        et.setHintTextColor(0xFF6B7488);
        et.setTextSize(14f);
        et.setBackgroundResource(R.drawable.chip_bg);
        et.setPadding(12 * dp, 9 * dp, 12 * dp, 9 * dp);
        LinearLayout.LayoutParams elp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        elp.topMargin = 12 * dp;
        box.addView(et, elp);

        LinearLayout btns = new LinearLayout(act);
        btns.setGravity(Gravity.END);
        LinearLayout.LayoutParams blp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        blp.topMargin = 16 * dp;
        btns.setLayoutParams(blp);

        TextView cancel = new TextView(act);
        cancel.setText("算了");
        cancel.setTextColor(0xFF8B93A7);
        cancel.setTextSize(14f);
        cancel.setPadding(18 * dp, 10 * dp, 18 * dp, 10 * dp);
        cancel.setOnClickListener(v -> d.dismiss());
        btns.addView(cancel);

        TextView ok = new TextView(act);
        ok.setText("保存");
        ok.setTextColor(0xFF04121C);
        ok.setTextSize(14f);
        ok.setTypeface(null, android.graphics.Typeface.BOLD);
        ok.setPadding(24 * dp, 10 * dp, 24 * dp, 10 * dp);
        GradientDrawable okBg = new GradientDrawable();
        okBg.setColor(0xFF6FD3FF);
        okBg.setCornerRadius(10 * dp);
        ok.setBackground(okBg);
        LinearLayout.LayoutParams okLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        okLp.leftMargin = 10 * dp;
        ok.setLayoutParams(okLp);
        ok.setOnClickListener(v -> {
            String val = et.getText().toString().trim();
            d.dismiss();
            if (onOk != null) onOk.onText(val);
        });
        btns.addView(ok);
        box.addView(btns);

        d.setContentView(box);
        Window w = d.getWindow();
        if (w != null) {
            w.setBackgroundDrawableResource(android.R.color.transparent);
            w.setLayout((int) (act.getResources().getDisplayMetrics().widthPixels * 0.86f),
                    ViewGroup.LayoutParams.WRAP_CONTENT);
            w.addFlags(WindowManager.LayoutParams.FLAG_DIM_BEHIND);
            WindowManager.LayoutParams lp = w.getAttributes();
            lp.dimAmount = 0.6f;
            w.setAttributes(lp);
        }
        d.show();
    }

    public interface OnText {
        void onText(String text);
    }
}
