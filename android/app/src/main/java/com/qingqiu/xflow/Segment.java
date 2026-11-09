package com.qingqiu.xflow;

/** 一段字节区间。from/to 是闭区间。 */
public class Segment {
    public long from;
    public long to;
    public volatile long done;

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
