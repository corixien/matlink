// Generates the Matlink icon (assets/icon.png and Android launcher icons).
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
)

func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	inRound := func(x, y, x0, y0, x1, y1, r float64) bool {
		if x < x0 || x > x1 || y < y0 || y > y1 {
			return false
		}
		cx := math.Max(x0+r, math.Min(x, x1-r))
		cy := math.Max(y0+r, math.Min(y, y1-r))
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
	}
	for yi := 0; yi < size; yi++ {
		for xi := 0; xi < size; xi++ {
			x, y := float64(xi)+0.5, float64(yi)+0.5
			var c color.RGBA
			if inRound(x, y, 0, 0, s, s, s*0.22) {
				t := y / s
				c = color.RGBA{uint8(30 + 20*t), uint8(90 + 40*t), uint8(230 - 30*t), 255}
				// monitor
				if inRound(x, y, s*0.14, s*0.20, s*0.66, s*0.60, s*0.04) {
					c = color.RGBA{255, 255, 255, 255}
					if inRound(x, y, s*0.18, s*0.24, s*0.62, s*0.56, s*0.02) {
						c = color.RGBA{20, 40, 90, 255}
					}
				}
				if x > s*0.37 && x < s*0.43 && y > s*0.60 && y < s*0.70 || inRound(x, y, s*0.28, s*0.68, s*0.52, s*0.72, s*0.02) {
					c = color.RGBA{255, 255, 255, 255}
				}
				// tablet
				if inRound(x, y, s*0.52, s*0.42, s*0.88, s*0.82, s*0.05) {
					c = color.RGBA{255, 255, 255, 255}
					if inRound(x, y, s*0.55, s*0.46, s*0.85, s*0.78, s*0.02) {
						c = color.RGBA{60, 160, 255, 255}
					}
				}
			}
			img.SetRGBA(xi, yi, c)
		}
	}
	return img
}

func scale(src *image.RGBA, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), src, image.Point{}, draw.Src)
	return dst
}

func save(path string, img image.Image) {
	f, _ := os.Create(path)
	defer f.Close()
	png.Encode(f, img)
}

func main() {
	big := render(1024)
	down := func(n int) *image.RGBA {
		dst := image.NewRGBA(image.Rect(0, 0, n, n))
		f := 1024 / n
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				var r, g, b, a int
				for dy := 0; dy < f; dy++ {
					for dx := 0; dx < f; dx++ {
						c := big.RGBAAt(x*f+dx, y*f+dy)
						r, g, b, a = r+int(c.R), g+int(c.G), b+int(c.B), a+int(c.A)
					}
				}
				k := f * f
				dst.SetRGBA(x, y, color.RGBA{uint8(r / k), uint8(g / k), uint8(b / k), uint8(a / k)})
			}
		}
		return dst
	}
	_ = scale
	save(os.Args[1], down(256))
	for i := 2; i+1 < len(os.Args); i += 2 {
		n := 0
		for _, ch := range os.Args[i+1] {
			n = n*10 + int(ch-'0')
		}
		save(os.Args[i], down(n))
	}
}
