package top.qingqiuya.xflow;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Build;
import android.util.Log;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * 局域网发现（mDNS / Bonjour）。
 *
 * 一开始用 UDP 组播（跟 XPort 一样），但在这台小米 15 上发组播和发广播
 * 都被系统以 EPERM 拒掉（单播正常，说明是广播/组播被策略禁了）。
 * 改用 Android 自带的 NsdManager：走系统服务，不受这个限制，
 * 而且 mDNS 本来就是干这个的，Mac/Linux 上 avahi、Windows 上 Bonjour 都认。
 *
 * 注册名 "XFlow@<型号>"，服务类型 _xflow._tcp。
 */
public class Beacon {

    public static final String SERVICE_TYPE = "_xflow._tcp.";
    private static final long TTL_MS = 15000;

    public static class Peer {
        public String name = "";
        public String ip = "";
        public int port;
        public String ver = "";
        public String platform = "android";
        public long lastSeen;
        public String key = "";      // mDNS 里的服务名，用来去重
    }

    /** ip -> Peer */
    public static final Map<String, Peer> PEERS = new LinkedHashMap<>();

    private final Context ctx;
    private NsdManager nsd;
    private NsdManager.RegistrationListener regListener;
    private NsdManager.DiscoveryListener discListener;
    private volatile boolean running;
    private String selfName = "android";
    private int selfPort = 8080;
    private String registeredName = "";

    public Beacon(Context ctx) {
        this.ctx = ctx.getApplicationContext();
    }

    public void setSelfName(String n) {
        if (n != null && !n.trim().isEmpty()) selfName = n.trim().replace(' ', '_');
    }

    public boolean isRunning() {
        return running;
    }

    public synchronized void start(int apiPort) {
        if (running) return;
        nsd = (NsdManager) ctx.getSystemService(Context.NSD_SERVICE);
        if (nsd == null) {
            Log.w("xflow-beacon", "没有 NsdManager");
            return;
        }
        selfPort = apiPort;
        running = true;
        register();
        discover();
    }

    private void register() {
        NsdServiceInfo info = new NsdServiceInfo();
        String model = Build.MODEL == null ? "android" : Build.MODEL;
        info.setServiceName("XFlow@" + model.replace(' ', '_'));
        info.setServiceType(SERVICE_TYPE);
        info.setPort(selfPort);

        regListener = new NsdManager.RegistrationListener() {
            @Override
            public void onServiceRegistered(NsdServiceInfo i) {
                registeredName = i.getServiceName();
                Log.i("xflow-beacon", "mDNS 注册成功: " + registeredName + " port=" + selfPort);
            }

            @Override
            public void onRegistrationFailed(NsdServiceInfo i, int code) {
                Log.w("xflow-beacon", "mDNS 注册失败 code=" + code);
            }

            @Override
            public void onServiceUnregistered(NsdServiceInfo i) {
                Log.i("xflow-beacon", "mDNS 已注销");
            }

            @Override
            public void onUnregistrationFailed(NsdServiceInfo i, int code) {
            }
        };
        try {
            nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, regListener);
        } catch (Exception e) {
            Log.w("xflow-beacon", "registerService 异常: " + e);
        }
    }

    private void discover() {
        discListener = new NsdManager.DiscoveryListener() {
            @Override
            public void onDiscoveryStarted(String type) {
                Log.i("xflow-beacon", "开始搜寻 " + type);
            }

            @Override
            public void onServiceFound(NsdServiceInfo info) {
                // 自己的服务跳过
                if (registeredName != null && info.getServiceName().equals(registeredName)) return;
                resolve(info);
            }

            @Override
            public void onServiceLost(NsdServiceInfo info) {
                synchronized (PEERS) {
                    PEERS.values().removeIf(p -> info.getServiceName().equals(p.key));
                }
            }

            @Override
            public void onDiscoveryStopped(String type) {
            }

            @Override
            public void onStartDiscoveryFailed(String type, int code) {
                Log.w("xflow-beacon", "搜寻启动失败 code=" + code);
            }

            @Override
            public void onStopDiscoveryFailed(String type, int code) {
            }
        };
        try {
            nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, discListener);
        } catch (Exception e) {
            Log.w("xflow-beacon", "discoverServices 异常: " + e);
        }
    }

    private void resolve(NsdServiceInfo info) {
        try {
            nsd.resolveService(info, new NsdManager.ResolveListener() {
                @Override
                public void onResolveFailed(NsdServiceInfo i, int code) {
                    Log.d("xflow-beacon", "解析失败 " + i.getServiceName() + " code=" + code);
                }

                @Override
                public void onServiceResolved(NsdServiceInfo i) {
                    if (i.getHost() == null) return;
                    Peer p = new Peer();
                    p.key = i.getServiceName();
                    p.name = i.getServiceName().replaceFirst("^XFlow@", "");
                    p.ip = i.getHost().getHostAddress();
                    p.port = i.getPort();
                    p.lastSeen = System.currentTimeMillis();
                    if (p.ip.startsWith("/")) p.ip = p.ip.substring(1);
                    synchronized (PEERS) {
                        PEERS.put(p.ip, p);
                    }
                    Log.i("xflow-beacon", "发现设备 " + p.name + " " + p.ip + ":" + p.port);
                }
            });
        } catch (Exception e) {
            Log.w("xflow-beacon", "resolveService 异常: " + e);
        }
    }

    public synchronized void stop() {
        running = false;
        try {
            if (nsd != null && discListener != null) nsd.stopServiceDiscovery(discListener);
        } catch (Exception ignored) {
        }
        try {
            if (nsd != null && regListener != null) nsd.unregisterService(regListener);
        } catch (Exception ignored) {
        }
    }

    /** 最近 15 秒内解析到的设备 */
    public static List<Peer> alive() {
        long now = System.currentTimeMillis();
        List<Peer> out = new ArrayList<>();
        synchronized (PEERS) {
            List<String> dead = new ArrayList<>();
            for (Map.Entry<String, Peer> e : PEERS.entrySet()) {
                if (now - e.getValue().lastSeen > TTL_MS) dead.add(e.getKey());
                else out.add(e.getValue());
            }
            for (String k : dead) PEERS.remove(k);
        }
        return out;
    }
}
