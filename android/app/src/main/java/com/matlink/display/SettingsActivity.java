package com.matlink.display;

import android.app.Activity;
import android.os.Bundle;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.CheckBox;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;

public class SettingsActivity extends Activity {
    private Prefs prefs;

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        prefs = new Prefs(this);
        int pad = (int) (20 * getResources().getDisplayMetrics().density);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(pad, pad, pad, pad);

        title(root, "Matlink settings");
        hint(root, "Resolution is matched to this tablet automatically. The PC creates a virtual monitor of the same size. Changes apply on the next connection (reconnect happens automatically when you leave this screen).");

        spinner(root, "Orientation", Prefs.ORIENTATION_LABELS, Prefs.indexOf(Prefs.ORIENTATION_VALUES, prefs.orientation()),
                i -> prefs.edit().putString("orientation", Prefs.ORIENTATION_VALUES[i]).apply());
        spinner(root, "Resolution", Prefs.RES_LABELS, Prefs.indexOf(Prefs.RES_VALUES, prefs.maxRes()),
                i -> prefs.edit().putInt("maxRes", Prefs.RES_VALUES[i]).apply());
        spinner(root, "Frame rate", Prefs.FPS_LABELS, Prefs.indexOf(Prefs.FPS_VALUES, prefs.fps()),
                i -> prefs.edit().putInt("fps", Prefs.FPS_VALUES[i]).apply());
        spinner(root, "Quality", Prefs.QUALITY_LABELS, Prefs.indexOf(Prefs.QUALITY_VALUES, prefs.quality()),
                i -> prefs.edit().putString("quality", Prefs.QUALITY_VALUES[i]).apply());
        check(root, "Keep screen on while connected", prefs.keepAwake(), v -> prefs.edit().putBoolean("keepAwake", v).apply());
        check(root, "Show stream stats overlay", prefs.showStats(), v -> prefs.edit().putBoolean("showStats", v).apply());
        hint(root, "PC-side settings (position, scale, resolution override) are in the Matlink tray icon menu on the computer.");

        ScrollView sv = new ScrollView(this);
        sv.addView(root);
        setContentView(sv);
    }

    private interface IntSetter { void set(int i); }
    private interface BoolSetter { void set(boolean v); }

    private void title(LinearLayout p, String t) {
        TextView tv = new TextView(this);
        tv.setText(t);
        tv.setTextSize(24);
        tv.setPadding(0, 0, 0, 16);
        p.addView(tv);
    }

    private void hint(LinearLayout p, String t) {
        TextView tv = new TextView(this);
        tv.setText(t);
        tv.setTextSize(13);
        tv.setAlpha(0.7f);
        tv.setPadding(0, 8, 0, 16);
        p.addView(tv);
    }

    private void spinner(LinearLayout p, String label, String[] items, int sel, IntSetter s) {
        TextView tv = new TextView(this);
        tv.setText(label);
        tv.setTextSize(16);
        tv.setPadding(0, 16, 0, 0);
        p.addView(tv);
        Spinner sp = new Spinner(this);
        sp.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item, items));
        sp.setSelection(sel);
        sp.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            @Override public void onItemSelected(AdapterView<?> a, View v, int pos, long id) { s.set(pos); }
            @Override public void onNothingSelected(AdapterView<?> a) { }
        });
        p.addView(sp);
    }

    private void check(LinearLayout p, String label, boolean on, BoolSetter s) {
        CheckBox cb = new CheckBox(this);
        cb.setText(label);
        cb.setChecked(on);
        cb.setPadding(0, 24, 0, 24);
        cb.setOnCheckedChangeListener((v, c) -> s.set(c));
        p.addView(cb);
    }
}
