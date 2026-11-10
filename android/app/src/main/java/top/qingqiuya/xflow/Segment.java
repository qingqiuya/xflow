package top.qingqiuya.xflow;

/** 一段字节区间。from/to 是闭区间。 */
public class Segment {
    public long from;
    public long to;
    public volatile long done;
    /** 这一段连续失败了几次（重试用尽就放弃，别把任务卡在 99%） */
    public volatile int fails;

    public Segment(long from, long to) {
        this.from = from;
        this.to = to;
    }

    public long size() {
        return to - from + 1;
    }

    public long remaining() {
        return size() - done;
    }
}
