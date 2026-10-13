package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

type kOutput struct {
	Name     string                      `json:"name"`
	Enabled  bool                        `json:"enabled"`
	Priority int                         `json:"priority"`
	CurMode  string                      `json:"currentModeId"`
	Pos      struct{ X, Y int }          `json:"pos"`
	Size     struct{ Width, Height int } `json:"size"`
	Scale    float64                     `json:"scale"`
	Modes    []struct {
		ID      string                      `json:"id"`
		Size    struct{ Width, Height int } `json:"size"`
		Refresh float64                     `json:"refreshRate"`
	} `json:"modes"`
}

func kscreenOutputs() ([]kOutput, error) {
	out, err := exec.Command("kscreen-doctor", "-j").Output()
	if err != nil {
		return nil, err
	}
	var d struct {
		Outputs []kOutput `json:"outputs"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, err
	}
	return d.Outputs, nil
}

func kscreen(args ...string) error {
	out, err := exec.Command("kscreen-doctor", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kscreen-doctor %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ConfigureVirtualOutput sets mode, scale and position of the freshly created virtual output.
func ConfigureVirtualOutput(w, h, fps int, scale float64, position string) (string, error) {
	var virt *kOutput
	var others []kOutput
	for i := 0; i < 20; i++ {
		outs, err := kscreenOutputs()
		if err != nil {
			return "", err
		}
		virt, others = nil, nil
		for j := range outs {
			if strings.HasPrefix(outs[j].Name, "Virtual-") {
				virt = &outs[j]
			} else if outs[j].Enabled {
				others = append(others, outs[j])
			}
		}
		if virt != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if virt == nil {
		return "", fmt.Errorf("virtual output did not appear")
	}
	name := virt.Name
	// pickMode finds the mode of the output matching the size (kscreen may round the width by up to 8)
	// and the refresh rate closest to fps.
	pickMode := func(o *kOutput) (id string, mw, mh int, close bool) {
		best := 1e9
		for _, m := range o.Modes {
			dw := m.Size.Width - w
			if m.Size.Height != h || dw > 8 || dw < -8 {
				continue
			}
			if d := math.Abs(m.Refresh - float64(fps)); d < best {
				best, id, mw, mh = d, m.ID, m.Size.Width, m.Size.Height
			}
		}
		return id, mw, mh, best <= 2
	}
	_, _, _, ok := pickMode(virt)
	if !ok {
		if err := kscreen(fmt.Sprintf("output.%s.addCustomMode.%d.%d.%d.reduced", name, w, h, fps*1000)); err != nil {
			return name, err
		}
	}
	// apply mode, scale and position in one call
	outs, _ := kscreenOutputs()
	modeID := ""
	for i := range outs {
		if outs[i].Name == name {
			modeID, w, h, _ = pickMode(&outs[i])
		}
	}
	if modeID == "" {
		return name, fmt.Errorf("mode %dx%d not available", w, h)
	}
	logicalW, logicalH := int(float64(w)/scale+0.5), int(float64(h)/scale+0.5)
	minX, minY, maxX, maxY := 1<<30, 1<<30, -(1 << 30), -(1 << 30)
	for _, o := range others {
		ow, oh := o.Size.Width, o.Size.Height
		if o.Scale > 0 {
			ow, oh = int(float64(ow)/o.Scale+0.5), int(float64(oh)/o.Scale+0.5)
		}
		minX, minY = min(minX, o.Pos.X), min(minY, o.Pos.Y)
		maxX, maxY = max(maxX, o.Pos.X+ow), max(maxY, o.Pos.Y+oh)
	}
	if len(others) == 0 {
		minX, minY, maxX, maxY = 0, 0, 0, 0
	}
	x, y := maxX, minY
	switch position {
	case "left":
		x, y = minX-logicalW, minY
	case "above":
		x, y = minX, minY-logicalH
	case "below":
		x, y = minX, maxY
	}
	return name, kscreen(
		fmt.Sprintf("output.%s.mode.%s", name, modeID),
		fmt.Sprintf("output.%s.scale.%g", name, scale),
		fmt.Sprintf("output.%s.position.%d,%d", name, x, y),
	)
}
