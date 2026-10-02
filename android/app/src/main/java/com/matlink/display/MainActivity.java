package com.matlink.display;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.graphics.Color;
import android.media.MediaCodec;
import android.media.MediaFormat;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.DisplayMetrics;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.Surface;
import android.view.SurfaceHolder;
import android.view.SurfaceView;
import android.view.View;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.TextView;

import org.json.JSONObject;

import java.io.BufferedInputStream;
import java.io.DataInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.ByteBuffer;

public class MainActivity extends Activity implements SurfaceHolder.Callback {
    private final Handler ui = new Handler(Looper.getMainLooper());
    private Prefs prefs;
    private FrameLayout root;
    private SurfaceView surfaceView;
    private TextView status;
    private TextView stats;
    private Button settingsBtn;
    private volatile Surface surface;
    private Thread worker;
    private volatile boolean running;
    private volatile Socket socket;
    private int shownW, shownH;
    private final Runnable hideButton = () -> {
        if (shownW > 0) settingsBtn.setVisibility(View.GONE);
    };

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        prefs = new Prefs(this);
        root = new FrameLayout(this);
        root.setBackgroundColor(Color.BLACK);

        surfaceView = new SurfaceView(this);
        surfaceView.getHolder().addCallback(this);
        root.addView(surfaceView, new FrameLayout.LayoutParams(-1, -1, Gravity.CENTER));

        status = new TextView(this);
        status.setTextColor(Color.WHITE);
        status.setTextSize(22);
        status.setGravity(Gravity.CENTER);
        root.addView(status, new FrameLayout.LayoutParams(-1, -2, Gravity.CENTER));

        stats = new TextView(this);
        stats.setTextColor(Color.GREEN);
        stats.setTextSize(12);
        stats.setPadding(16, 8, 16, 8);
        stats.setBackgroundColor(0x88000000);
        root.addView(stats, new FrameLayout.LayoutParams(-2, -2, Gravity.TOP | Gravity.START));

        settingsBtn = new Button(this);
        settingsBtn.setText("Settings");
        settingsBtn.setOnClickListener(v -> startActivity(new Intent(this, SettingsActivity.class)));
        FrameLayout.LayoutParams lp = new FrameLayout.LayoutParams(-2, -2, Gravity.BOTTOM | Gravity.END);
        lp.setMargins(0, 0, 32, 32);
        root.addView(settingsBtn, lp);

        root.setOnTouchListener((v, e) -> {
            if (e.getAction() == MotionEvent.ACTION_DOWN) {
                settingsBtn.setVisibility(View.VISIBLE);
                ui.removeCallbacks(hideButton);
                ui.postDelayed(hideButton, 4000);
            }
            return true;
        });
        setContentView(root);
        setStatus("Matlink\n\nWaiting for the PC...\nConnect USB and start Matlink on the computer.");
    }

    @Override
    protected void onResume() {
        super.onResume();
        applyPrefs();
        hideSystemUi();
        if (surface != null) startWorker();
    }

    @Override
    protected void onPause() {
        super.onPause();
        stopWorker();
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) hideSystemUi();
    }

    @Override
    public void onConfigurationChanged(android.content.res.Configuration c) {
        super.onConfigurationChanged(c);
        // Orientation changed: reconnect so the PC resizes the virtual monitor.
        if (surface != null && running) {
            stopWorker();
            startWorker();
        }
    }

    private void applyPrefs() {
        switch (prefs.orientation()) {
            case "portrait": setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_SENSOR_PORTRAIT); break;
            case "auto": setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_FULL_SENSOR); break;
            default: setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE);
        }
        if (prefs.keepAwake()) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        stats.setVisibility(prefs.showStats() ? View.VISIBLE : View.GONE);
    }

    @SuppressWarnings("deprecation")
    private void hideSystemUi() {
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY);
        if (Build.VERSION.SDK_INT >= 28) {
            getWindow().getAttributes().layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
        }
    }

    // ---- surface ----
    @Override public void surfaceCreated(SurfaceHolder h) { surface = h.getSurface(); if (!running) startWorker(); }
    @Override public void surfaceChanged(SurfaceHolder h, int f, int w, int hh) { }
    @Override public void surfaceDestroyed(SurfaceHolder h) { stopWorker(); surface = null; }

    private void setStatus(String s) {
        ui.post(() -> {
            status.setText(s);
            status.setVisibility(s == null || s.isEmpty() ? View.GONE : View.VISIBLE);
            if (s != null && !s.isEmpty()) {
                settingsBtn.setVisibility(View.VISIBLE);
            }
        });
    }

    private void fit(int w, int h) {
        ui.post(() -> {
            shownW = w;
            shownH = h;
            int cw = root.getWidth(), ch = root.getHeight();
            if (cw == 0 || ch == 0 || w == 0) return;
            float s = Math.min((float) cw / w, (float) ch / h);
            FrameLayout.LayoutParams lp = new FrameLayout.LayoutParams(Math.round(w * s), Math.round(h * s), Gravity.CENTER);
            surfaceView.setLayoutParams(lp);
            ui.postDelayed(hideButton, 3000);
        });
    }

    // ---- connection ----
    private void startWorker() {
        stopWorker();
        running = true;
        final Surface s = surface;
        worker = new Thread(() -> loop(s), "matlink-stream");
        worker.start();
    }

    private void stopWorker() {
        running = false;
        Socket sk = socket;
        if (sk != null) try { sk.close(); } catch (IOException ignored) { }
        Thread t = worker;
        if (t != null && t != Thread.currentThread()) {
            t.interrupt();
            try { t.join(1500); } catch (InterruptedException ignored) { }
        }
        worker = null;
    }

    private void loop(Surface s) {
        while (running) {
            try {
                stream(s);
            } catch (Exception e) {
                android.util.Log.e("matlink", "stream failed", e);
            }
            if (!running) return;
            shownW = 0;
            setStatus("Matlink\n\nWaiting for the PC...\nConnect USB and start Matlink on the computer.");
            try { Thread.sleep(1000); } catch (InterruptedException e) { return; }
        }
    }

    private void stream(Surface s) throws Exception {
        DisplayMetrics dm = new DisplayMetrics();
        getWindowManager().getDefaultDisplay().getRealMetrics(dm);
        int w = dm.widthPixels, h = dm.heightPixels;
        int dpi = dm.densityDpi;
        int[] fit = clampToDecoder(w, h, prefs.fps());
        if (fit[0] != w) {
            dpi = dpi * fit[0] / w;
            w = fit[0];
            h = fit[1];
        }
        Socket sk = new Socket();
        sk.setTcpNoDelay(true);
        sk.connect(new InetSocketAddress("127.0.0.1", prefs.port()), 1500);
        socket = sk;
        try {
            OutputStream out = sk.getOutputStream();
            JSONObject hello = new JSONObject();
            hello.put("v", 1).put("w", w).put("h", h).put("dpi", dpi).put("model", Build.MODEL)
                    .put("maxH", prefs.maxRes()).put("fps", prefs.fps()).put("quality", prefs.quality());
            out.write((hello.toString() + "\n").getBytes("UTF-8"));
            out.flush();
            InputStream in = new BufferedInputStream(sk.getInputStream(), 1 << 20);
            DataInputStream din = new DataInputStream(in);
            JSONObject reply = new JSONObject(readLine(in));
            int vw = reply.getInt("w"), vh = reply.getInt("h");
            setStatus("");
            fit(vw, vh);
            decode(din, s, vw, vh);
        } finally {
            try { sk.close(); } catch (IOException ignored) { }
            socket = null;
        }
    }

    private static String readLine(InputStream in) throws IOException {
        StringBuilder sb = new StringBuilder();
        int c;
        while ((c = in.read()) != '\n') {
            if (c < 0) throw new IOException("closed");
            sb.append((char) c);
        }
        return sb.toString();
    }

    private void decode(DataInputStream in, Surface s, int w, int h) throws Exception {
        int firstLen = in.readInt();
        long firstPts = in.readLong();
        if (firstLen <= 0 || firstLen > (16 << 20)) throw new IOException("bad frame");
        byte[] first = new byte[firstLen];
        in.readFully(first);
        byte[][] csd = extractCsd(first);
        StringBuilder hx = new StringBuilder();
        for (int i = 0; i < Math.min(40, first.length); i++) hx.append(String.format("%02x", first[i]));
        android.util.Log.i("matlink", "first AU len " + firstLen + " head " + hx + " csd " + (csd[0] == null ? -1 : csd[0].length) + "/" + (csd[1] == null ? -1 : csd[1].length));
        MediaCodec codec = null;
        // Try every AVC decoder (hardware first) with gentler configs: some vendor decoders reject
        // realtime priority or low-latency hints.
        java.util.List<String> names = avcDecoders();
        for (String name : names) {
            for (int attempt = 0; attempt < 3 && codec == null; attempt++) {
                MediaFormat fmt = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, w, h);
                fmt.setInteger(MediaFormat.KEY_MAX_WIDTH, w);
                fmt.setInteger(MediaFormat.KEY_MAX_HEIGHT, h);
                if (csd[0] != null) {
                    fmt.setByteBuffer("csd-0", ByteBuffer.wrap(csd[0]));
                    fmt.setByteBuffer("csd-1", ByteBuffer.wrap(csd[1]));
                }
                if (attempt >= 1) fmt.setInteger(MediaFormat.KEY_PRIORITY, 1);
                if (attempt == 0 && Build.VERSION.SDK_INT >= 30) fmt.setInteger(MediaFormat.KEY_LOW_LATENCY, 1);
                MediaCodec c = MediaCodec.createByCodecName(name);
                try {
                    c.configure(fmt, s, null, 0);
                    c.start();
                    codec = c;
                    android.util.Log.i("matlink", "decoder " + name + " attempt " + attempt);
                } catch (Exception e) {
                    android.util.Log.w("matlink", "decoder " + name + " attempt " + attempt + " failed: " + e, e);
                    try { c.release(); } catch (Exception ignored) { }
                }
            }
            if (codec != null) break;
        }
        if (codec == null) throw new IOException("no working H.264 decoder");
        final MediaCodec dec = codec;
        final boolean[] alive = {true};
        Thread drain = new Thread(() -> {
            MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
            long frames = 0, last = System.nanoTime();
            while (alive[0]) {
                try {
                    int idx = dec.dequeueOutputBuffer(info, 20000);
                    if (idx >= 0) {
                        dec.releaseOutputBuffer(idx, true);
                        frames++;
                        long now = System.nanoTime();
                        if (now - last >= 1_000_000_000L) {
                            final String t = w + "x" + h + "  " + frames + " fps  " + (statBytes / 125000) + " Mbit/s";
                            statBytes = 0;
                            frames = 0;
                            last = now;
                            if (prefs.showStats()) ui.post(() -> stats.setText(t));
                        }
                    }
                } catch (Exception e) {
                    return;
                }
            }
        }, "matlink-drain");
        drain.start();
        try {
            byte[] buf = first;
            int len = firstLen;
            long pts = firstPts;
            while (running) {
                statBytes += len;
                int idx;
                while ((idx = dec.dequeueInputBuffer(50000)) < 0) {
                    if (!running) return;
                }
                ByteBuffer ib = dec.getInputBuffer(idx);
                ib.clear();
                ib.put(buf, 0, len);
                dec.queueInputBuffer(idx, 0, len, pts, 0);
                len = in.readInt();
                pts = in.readLong();
                if (len <= 0 || len > (16 << 20)) throw new IOException("bad frame");
                if (len > buf.length) buf = new byte[len];
                in.readFully(buf, 0, len);
            }
        } finally {
            alive[0] = false;
            drain.join(500);
            try { dec.stop(); } catch (Exception ignored) { }
            dec.release();
        }
    }

    private volatile long statBytes;

    /** Shrinks the requested size (keeping aspect) until the hardware AVC decoder accepts it. */
    private static int[] clampToDecoder(int w, int h, int fps) {
        try {
            for (String name : avcDecoders()) {
                android.media.MediaCodecInfo info = null;
                for (android.media.MediaCodecInfo i : new android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS).getCodecInfos()) {
                    if (i.getName().equals(name)) info = i;
                }
                if (info == null) continue;
                android.media.MediaCodecInfo.VideoCapabilities vc = info.getCapabilitiesForType(MediaFormat.MIMETYPE_VIDEO_AVC).getVideoCapabilities();
                int cw = w, ch = h;
                if (!vc.areSizeAndRateSupported(cw, ch, fps)) {
                    // largest 16-aligned width (8-aligned height) with the same aspect that the decoder accepts
                    for (cw = (w - 1) & ~15; cw >= 640; cw -= 16) {
                        ch = Math.round(cw * (float) h / w / 8f) * 8;
                        if (vc.areSizeAndRateSupported(cw, ch, fps)) break;
                    }
                }
                android.util.Log.i("matlink", "decoder " + name + " size " + w + "x" + h + " -> " + cw + "x" + ch);
                return new int[]{cw, ch};
            }
        } catch (Exception e) {
            android.util.Log.w("matlink", "capability query failed", e);
        }
        return new int[]{w, h};
    }

    private static java.util.List<String> avcDecoders() {
        java.util.List<String> hw = new java.util.ArrayList<>(), sw = new java.util.ArrayList<>();
        for (android.media.MediaCodecInfo info : new android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS).getCodecInfos()) {
            if (info.isEncoder()) continue;
            for (String t : info.getSupportedTypes()) {
                if (!t.equalsIgnoreCase(MediaFormat.MIMETYPE_VIDEO_AVC)) continue;
                boolean soft = Build.VERSION.SDK_INT >= 29 ? info.isSoftwareOnly()
                        : info.getName().startsWith("OMX.google.") || info.getName().startsWith("c2.android.");
                (soft ? sw : hw).add(info.getName());
            }
        }
        hw.addAll(sw);
        android.util.Log.i("matlink", "avc decoders: " + hw);
        return hw;
    }

    /** Returns {SPS, PPS} NAL units (with start codes) found in an Annex-B access unit, or {null, null}. */
    private static byte[][] extractCsd(byte[] d) {
        byte[][] r = new byte[2][];
        int i = 0, n = d.length;
        while (i + 3 < n) {
            if (d[i] == 0 && d[i + 1] == 0 && d[i + 2] == 1) {
                int start = i + 3, j = start;
                while (j + 2 < n && !(d[j] == 0 && d[j + 1] == 0 && (d[j + 2] == 1 || (d[j + 2] == 0 && j + 3 < n && d[j + 3] == 1)))) j++;
                if (j + 2 >= n) j = n;
                int type = d[start] & 31;
                if (type == 7 || type == 8) {
                    byte[] nal = new byte[4 + j - start];
                    nal[3] = 1;
                    System.arraycopy(d, start, nal, 4, j - start);
                    r[type == 7 ? 0 : 1] = nal;
                }
                i = j;
            } else {
                i++;
            }
        }
        if (r[0] == null || r[1] == null) return new byte[2][];
        return r;
    }
}
