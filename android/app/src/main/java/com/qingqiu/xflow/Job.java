package com.qingqiu.xflow;

import java.util.ArrayList;
import java.util.List;

public class Job {
    public String id;
    public String url;
    public String name;
    public long total;
    public volatile long done;
    public String state = "queued"; // queued / downloading / paused / done / error
    public String err = "";
    public String path = "";      // 最终文件
    public String partPath = "";  // 未完成的 .part
    public String metaPath = "";  // 断点信息
    public int segN = 8;          // 分块数
    public long speed;            // 字节/秒
    public int workers;           // 当前活跃分块线程

    /** 用户要求停（暂停或取消），分块线程看到就退出 */
    public volatile boolean canceled;
    /** 用户要求彻底删掉（连 .part 一起） */
    public volatile boolean removed;

    public final List<Segment> segs = new ArrayList<>();

    public Job(String id, String url, String name) {
        this.id = id;
        this.url = url;
        this.name = name;
    }

    public int percent() {
        if (total <= 0) return 0;
        return (int) Math.min(100, done * 100 / total);
    }

    public synchronized void resetSegments() {
        segs.clear();
    }
}
