package httptransport

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// These geometric badges are drawn from project-owned primitives. No portrait,
// font, third-party icon, external image, or model output is used as an input.
func renderWorkforceAvatar(name string) ([]byte, error) {
	styles := map[string]struct {
		accent color.RGBA
		glyph  int
	}{
		"website-knowledge-curator.png":     {color.RGBA{40, 126, 115, 255}, 0},
		"website-receptionist-public.png":   {color.RGBA{51, 117, 185, 255}, 1},
		"website-receptionist-customer.png": {color.RGBA{146, 86, 170, 255}, 2},
		"qa-check-agent.png":                {color.RGBA{61, 128, 78, 255}, 3},
		"marlon-brando.png":                 {color.RGBA{177, 99, 53, 255}, 4},
		"coordinator-agent.png":             {color.RGBA{86, 99, 174, 255}, 5},
		"qa-plan-agent.png":                 {color.RGBA{145, 112, 44, 255}, 6},
		"lead-agent.png":                    {color.RGBA{162, 72, 100, 255}, 7},
	}
	style, ok := styles[name]
	if !ok {
		return nil, fmt.Errorf("unknown generated workforce avatar %q", name)
	}
	canvas := workforceAvatarCanvas{image.NewRGBA(image.Rect(0, 0, 512, 512))}
	ink := color.RGBA{35, 46, 67, 255}
	white := color.RGBA{255, 255, 255, 255}
	paper := color.RGBA{248, 247, 242, 255}
	tint := color.RGBA{(style.accent.R / 4) + 183, (style.accent.G / 4) + 183, (style.accent.B / 4) + 183, 255}
	canvas.rect(0, 0, 128, 128, paper)
	canvas.circle(64, 64, 60, tint)
	canvas.circle(64, 64, 51, paper)
	canvas.circle(18, 44, 5, style.accent)
	canvas.circle(108, 32, 3, style.accent)
	canvas.circle(30, 111, 3, style.accent)
	canvas.line(64, 25, 64, 36, 4, ink)
	canvas.circle(64, 22, 5, style.accent)
	canvas.roundRect(23, 52, 82, 20, 6, ink)
	canvas.roundRect(28, 34, 72, 57, 17, ink)
	canvas.roundRect(32, 38, 64, 49, 13, white)
	canvas.roundRect(39, 48, 50, 25, 9, style.accent)
	canvas.circle(51, 60, 4, white)
	canvas.circle(77, 60, 4, white)
	canvas.line(57, 80, 71, 80, 2, ink)
	canvas.roundRect(42, 94, 44, 13, 6, ink)
	canvas.circle(100, 101, 21, white)
	canvas.circle(100, 101, 18, style.accent)
	line := func(x1, y1, x2, y2 int) { canvas.line(x1, y1, x2, y2, 2, white) }
	switch style.glyph {
	case 0: // Open book.
		line(90, 94, 100, 97)
		line(100, 97, 110, 94)
		line(90, 94, 90, 106)
		line(110, 94, 110, 106)
		line(90, 106, 100, 109)
		line(100, 109, 110, 106)
		line(100, 97, 100, 109)
	case 1: // Speech bubble.
		canvas.roundRect(89, 93, 22, 14, 4, white)
		line(94, 105, 93, 111)
		line(93, 111, 100, 106)
	case 2: // Two people.
		canvas.circle(94, 96, 4, white)
		canvas.circle(106, 96, 4, white)
		canvas.roundRect(89, 103, 10, 7, 3, white)
		canvas.roundRect(101, 103, 10, 7, 3, white)
	case 3: // Check.
		line(90, 101, 97, 108)
		line(97, 108, 110, 94)
	case 4: // Play.
		line(95, 92, 95, 110)
		line(95, 92, 109, 101)
		line(109, 101, 95, 110)
	case 5: // Connected nodes.
		line(91, 106, 100, 94)
		line(100, 94, 109, 106)
		line(91, 106, 109, 106)
		for _, p := range [][2]int{{91, 106}, {100, 94}, {109, 106}} {
			canvas.circle(p[0], p[1], 3, white)
		}
	case 6: // Plan.
		canvas.roundRect(91, 91, 18, 21, 3, white)
		for _, y := range []int{97, 102, 107} {
			canvas.line(96, y, 104, y, 1, style.accent)
		}
	case 7: // Direction marker.
		line(100, 91, 91, 109)
		line(91, 109, 100, 104)
		line(100, 104, 109, 109)
		line(109, 109, 100, 91)
	}
	var content bytes.Buffer
	if err := png.Encode(&content, canvas.image); err != nil {
		return nil, err
	}
	return content.Bytes(), nil
}

// Coordinates use a 128-unit square, rasterized at four pixels per unit.
type workforceAvatarCanvas struct{ image *image.RGBA }

func (c workforceAvatarCanvas) rect(x, y, width, height int, fill color.RGBA) {
	draw.Draw(c.image, image.Rect(x*4, y*4, (x+width)*4, (y+height)*4), &image.Uniform{C: fill}, image.Point{}, draw.Src)
}

func (c workforceAvatarCanvas) circle(x, y, radius int, fill color.RGBA) {
	cx, cy, r := x*4, y*4, radius*4
	for py := cy - r; py < cy+r; py++ {
		for px := cx - r; px < cx+r; px++ {
			dx, dy := 2*(px-cx)+1, 2*(py-cy)+1
			if dx*dx+dy*dy <= 4*r*r {
				c.image.SetRGBA(px, py, fill)
			}
		}
	}
}

func (c workforceAvatarCanvas) roundRect(x, y, width, height, radius int, fill color.RGBA) {
	c.rect(x+radius, y, width-2*radius, height, fill)
	c.rect(x, y+radius, width, height-2*radius, fill)
	for _, p := range [][2]int{{x + radius, y + radius}, {x + width - radius, y + radius}, {x + radius, y + height - radius}, {x + width - radius, y + height - radius}} {
		c.circle(p[0], p[1], radius, fill)
	}
}

func (c workforceAvatarCanvas) line(x1, y1, x2, y2, radius int, fill color.RGBA) {
	steps := max(absWorkforceAvatarCoordinate(x2-x1), absWorkforceAvatarCoordinate(y2-y1)) * 4
	for i := 0; i <= steps; i++ {
		c.circle(x1+(x2-x1)*i/max(steps, 1), y1+(y2-y1)*i/max(steps, 1), radius, fill)
	}
}

func absWorkforceAvatarCoordinate(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
