package com.matlink.display;

import android.content.Context;
import android.content.SharedPreferences;

final class Prefs {
    static final String[] ORIENTATION_LABELS = {"Landscape (both)", "Portrait", "Auto-rotate"};
    static final String[] ORIENTATION_VALUES = {"landscape", "portrait", "auto"};
    static final String[] RES_LABELS = {"Native (full tablet resolution)", "1440p cap", "1080p cap", "720p cap"};
    static final int[] RES_VALUES = {0, 1440, 1080, 720};
    static final String[] FPS_LABELS = {"60 fps", "30 fps"};
    static final int[] FPS_VALUES = {60, 30};
    static final String[] QUALITY_LABELS = {"Low", "Medium", "High", "Ultra"};
    static final String[] QUALITY_VALUES = {"low", "medium", "high", "ultra"};

    private final SharedPreferences sp;

    Prefs(Context c) {
        sp = c.getSharedPreferences("matlink", Context.MODE_PRIVATE);
    }

    SharedPreferences.Editor edit() { return sp.edit(); }
    String orientation() { return sp.getString("orientation", "landscape"); }
    int maxRes() { return sp.getInt("maxRes", 0); }
    int fps() { return sp.getInt("fps", 60); }
    String quality() { return sp.getString("quality", "high"); }
    boolean keepAwake() { return sp.getBoolean("keepAwake", true); }
    boolean showStats() { return sp.getBoolean("showStats", false); }
    int port() { return sp.getInt("port", 27183); }

    static int indexOf(int[] a, int v) {
        for (int i = 0; i < a.length; i++) if (a[i] == v) return i;
        return 0;
    }

    static int indexOf(String[] a, String v) {
        for (int i = 0; i < a.length; i++) if (a[i].equals(v)) return i;
        return 0;
    }
}
