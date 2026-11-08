package codec

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"testing"
)

func inflateIDAT(t *testing.T, b []byte) []byte {
	t.Helper()
	_, _, idat, err := splitPNG(b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// TestFilterRoundTrip runs real encoder output, which mixes all five filter
// types, through unfilter and refilter for each PNG pixel format.
func TestFilterRoundTrip(t *testing.T) {
	src := photo(97, 61, 9)
	nrgba := image.NewNRGBA(src.Bounds())
	gray16 := image.NewGray16(src.Bounds())
	pal2 := image.NewPaletted(src.Bounds(), color.Palette{color.Black, color.White})
	pal16 := image.NewPaletted(src.Bounds(), color.Palette(make([]color.Color, 16)))
	for i := range pal16.Palette {
		pal16.Palette[i] = color.Gray{uint8(i * 16)}
	}
	for y := range 61 {
		for x := range 97 {
			c := src.RGBAAt(x, y)
			nrgba.Set(x, y, color.NRGBA{c.R, c.G, c.B, uint8(x * 2)})
			gray16.SetGray16(x, y, color.Gray16{uint16(x*600 + y*7)})
			pal2.SetColorIndex(x, y, uint8((x+y)%2))
			pal16.SetColorIndex(x, y, uint8((x*y)%16))
		}
	}
	cases := []struct {
		name string
		img  image.Image
		bpp  int
	}{
		{"rgb", src, 3},
		{"rgba", nrgba, 4},
		{"gray", image.NewGray(src.Bounds()), 1},
		{"gray16", gray16, 2},
		{"1-bit palette", pal2, 1},
		{"4-bit palette", pal16, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := encodePNG(t, tc.img, png.DefaultCompression)
			g, ok := readGeometry(b)
			if !ok || g.bpp != tc.bpp || g.rows != 61 {
				t.Fatalf("geometry %+v ok=%v, want bpp %d", g, ok, tc.bpp)
			}
			plain := inflateIDAT(t, b)
			u, ok := unfilterRows(plain, g)
			if !ok {
				t.Fatal("unfilter rejected encoder output")
			}
			if tc.name == "rgba" {
				for y := range g.rows {
					want := nrgba.Pix[y*nrgba.Stride:][:g.stride]
					if !bytes.Equal(u[g.rows+y*g.stride:][:g.stride], want) {
						t.Fatalf("row %d unfiltered pixels differ from the image", y)
					}
				}
			}
			back, err := refilterRows(u, g)
			if err != nil || !bytes.Equal(back, plain) {
				t.Fatalf("refilter: err=%v equal=%v", err, bytes.Equal(back, plain))
			}
		})
	}
}

// TestFilterRandom checks the inverse on arbitrary bytes and filter types,
// including strides shorter than one pixel.
func TestFilterRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(10, 11))
	for _, g := range []geometry{{1, 1, 1}, {3, 2, 4}, {7, 13, 3}, {5, 64, 8}, {9, 33, 6}} {
		plain := make([]byte, g.size())
		for i := range plain {
			plain[i] = byte(r.Uint32())
		}
		for y := range g.rows {
			plain[y*(g.stride+1)] = byte(r.IntN(5))
		}
		u, ok := unfilterRows(plain, g)
		if !ok {
			t.Fatalf("%+v: unfilter rejected valid data", g)
		}
		back, err := refilterRows(u, g)
		if err != nil || !bytes.Equal(back, plain) {
			t.Fatalf("%+v: refilter err=%v equal=%v", g, err, bytes.Equal(back, plain))
		}
		plain[0] = 5
		if _, ok := unfilterRows(plain, g); ok {
			t.Fatalf("%+v: filter type 5 accepted", g)
		}
		u[0] = 5
		if _, err := refilterRows(u, g); err == nil {
			t.Fatalf("%+v: refilter accepted filter type 5", g)
		}
		if _, err := refilterRows(u[1:], g); err == nil {
			t.Fatalf("%+v: refilter accepted a short buffer", g)
		}
	}
}

func TestReadGeometryRejects(t *testing.T) {
	b := encodePNG(t, photo(8, 8, 1), png.DefaultCompression)
	mut := func(i int, v byte) []byte {
		c := bytes.Clone(b)
		c[i] = v
		return c
	}
	for name, c := range map[string][]byte{
		"interlaced":   mut(28, 1),
		"bad color":    mut(25, 5),
		"bad depth":    mut(24, 3),
		"zero width":   append(append(bytes.Clone(b[:16]), 0, 0, 0, 0), b[20:]...),
		"short":        b[:20],
		"no IHDR type": mut(12, 'X'),
	} {
		if _, ok := readGeometry(c); ok {
			t.Errorf("%s: geometry accepted", name)
		}
	}
}
