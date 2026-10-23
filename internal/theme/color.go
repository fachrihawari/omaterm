package theme

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// OKLCH is a color in OKLab's polar form: L lightness 0..1, C chroma, H hue
// in degrees. Derivation works in it because moving L keeps a hue, which
// sRGB mixing does not.
type OKLCH struct{ L, C, H float64 }

func srgbToLinear(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) uint8 {
	if c <= 0.0031308 {
		c *= 12.92
	} else {
		c = 1.055*math.Pow(c, 1/2.4) - 0.055
	}
	return uint8(math.Round(math.Max(0, math.Min(1, c)) * 255))
}

// toOKLCH converts an opaque color; alpha is ignored.
func toOKLCH(c ui.Color) OKLCH {
	r, g, b := srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	L := 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	A := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	B := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	h := math.Atan2(B, A) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return OKLCH{L, math.Hypot(A, B), h}
}

// color converts back to sRGB, reducing chroma until the color fits the
// gamut so that a synthesized hue never clips to a different one.
func (o OKLCH) color() ui.Color {
	for i := 0; i < 24; i++ {
		if c, ok := o.rgb(); ok {
			return c
		}
		o.C *= 0.9
	}
	c, _ := o.rgb()
	return c
}

func (o OKLCH) rgb() (ui.Color, bool) {
	h := o.H * math.Pi / 180
	A, B := o.C*math.Cos(h), o.C*math.Sin(h)
	l := o.L + 0.3963377774*A + 0.2158037573*B
	m := o.L - 0.1055613458*A - 0.0638541728*B
	s := o.L - 0.0894841775*A - 1.2914855480*B
	l, m, s = l*l*l, m*m*m, s*s*s
	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	bb := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	ok := r >= -0.001 && r <= 1.001 && g >= -0.001 && g <= 1.001 && bb >= -0.001 && bb <= 1.001
	return ui.Color{R: linearToSRGB(r), G: linearToSRGB(g), B: linearToSRGB(bb), A: 255}, ok
}

// luminance is WCAG's relative luminance.
func luminance(c ui.Color) float64 {
	return 0.2126*srgbToLinear(c.R) + 0.7152*srgbToLinear(c.G) + 0.0722*srgbToLinear(c.B)
}

// Contrast is the WCAG contrast ratio of two opaque colors, 1..21.
func Contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ensureContrast moves c's lightness away from bg until it reaches ratio,
// keeping its hue: the dimmest color that still reads.
func ensureContrast(c, bg ui.Color, ratio float64) ui.Color {
	if Contrast(c, bg) >= ratio {
		return c
	}
	o := toOKLCH(c)
	dir := 1.0
	if luminance(bg) > 0.18 {
		dir = -1
	}
	for i := 0; i < 100 && Contrast(o.color(), bg) < ratio; i++ {
		o.L += dir * 0.01
		if o.L <= 0 || o.L >= 1 {
			break
		}
	}
	return o.color()
}

// opaque flattens c over bg.
func opaque(c, bg ui.Color) ui.Color {
	if c.A == 255 {
		return c
	}
	return c.Over(bg)
}

// hueIn reports whether c has a hue within [lo, hi] degrees (wrapping when
// lo > hi) and enough chroma to tell one.
func hueIn(c ui.Color, lo, hi float64) bool {
	o := toOKLCH(c)
	if o.C < 0.04 {
		return false
	}
	if lo <= hi {
		return o.H >= lo && o.H <= hi
	}
	return o.H >= lo || o.H <= hi
}

// Hex formats an opaque color as #rrggbb.
func Hex(c ui.Color) string {
	const digits = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{c.R, c.G, c.B} {
		b[1+2*i] = digits[v>>4]
		b[2+2*i] = digits[v&15]
	}
	return string(b)
}

var (
	black = ui.Color{A: 255}
	white = ui.Color{R: 255, G: 255, B: 255, A: 255}
)
