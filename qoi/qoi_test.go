package qoi

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"math/rand"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	dims := [][2]int{{1, 1}, {1, 7}, {7, 1}, {2, 2}, {3, 5}, {13, 17}, {64, 32}}
	for _, d := range dims {
		for _, channels := range []int{3, 4} {
			for _, cs := range []Colorspace{SRGB, Linear} {
				w, h := d[0], d[1]
				pix := make([]byte, w*h*channels)
				for i := range pix {
					pix[i] = byte(rng.Intn(256))
				}
				img := &Image{
					Width:      w,
					Height:     h,
					Channels:   channels,
					Colorspace: cs,
					Pix:        pix,
				}
				data, err := Marshal(img)
				if err != nil {
					t.Fatalf("Marshal(%dx%d/%d): %v", w, h, channels, err)
				}
				got, err := Unmarshal(data)
				if err != nil {
					t.Fatalf("Unmarshal(%dx%d/%d): %v", w, h, channels, err)
				}
				if got.Width != w || got.Height != h || got.Channels != channels || got.Colorspace != cs {
					t.Fatalf("header = %+v, want %dx%d/%d/%v", got, w, h, channels, cs)
				}
				if !bytes.Equal(got.Pix, pix) {
					t.Fatalf("pixels differ after round trip")
				}
			}
		}
	}
}

func TestLongRuns(t *testing.T) {
	for _, channels := range []int{3, 4} {
		for _, count := range []int{1, 2, 61, 62, 63, 124, 1000} {
			pix := make([]byte, count*channels)
			for i := 0; i < count; i++ {
				pix[i*channels+0] = 12
				pix[i*channels+1] = 34
				pix[i*channels+2] = 56
				if channels == 4 {
					pix[i*channels+3] = 78
				}
			}
			img := &Image{Width: count, Height: 1, Channels: channels, Pix: pix}
			data, err := Marshal(img)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Unmarshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Pix, pix) {
				t.Fatalf("channels=%d count=%d: pixels differ", channels, count)
			}
		}
	}
}

func TestDecodeRGBWithRGBAChunks(t *testing.T) {
	// The channels field is informative; a decoder must still accept an
	// RGBA chunk in a three-channel file and discard the alpha value.
	data := []byte{
		'q', 'o', 'i', 'f',
		0, 0, 0, 1,
		0, 0, 0, 1,
		3, 0,
		opRGBA, 10, 20, 30, 40,
		0, 0, 0, 0, 0, 0, 0, 1,
	}
	img, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Channels != 3 {
		t.Fatalf("channels = %d, want 3", img.Channels)
	}
	want := []byte{10, 20, 30}
	if !bytes.Equal(img.Pix, want) {
		t.Fatalf("pixels = %v, want %v", img.Pix, want)
	}
}

func TestUnmarshalAllowsTrailingData(t *testing.T) {
	img := &Image{Width: 1, Height: 1, Channels: 4, Pix: []byte{1, 2, 3, 4}}
	data, err := Marshal(img)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, 0xde, 0xad, 0xbe, 0xef)
	if _, err := Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal with trailing data: %v", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	valid, err := Marshal(&Image{Width: 1, Height: 1, Channels: 4, Pix: []byte{1, 2, 3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func([]byte)) []byte {
		b := append([]byte(nil), valid...)
		fn(b)
		return b
	}

	tests := []struct {
		name string
		data []byte
		is   error
	}{
		{"empty", nil, ErrUnexpectedEOF},
		{"short header", []byte("qoif"), ErrUnexpectedEOF},
		{"bad magic", mutate(func(b []byte) { b[0] = 'x' }), ErrInvalidMagic},
		{"zero width", mutate(func(b []byte) { b[4], b[5], b[6], b[7] = 0, 0, 0, 0 }), ErrEmptyImage},
		{"zero height", mutate(func(b []byte) { b[8], b[9], b[10], b[11] = 0, 0, 0, 0 }), ErrEmptyImage},
		{"channels 2", mutate(func(b []byte) { b[12] = 2 }), ErrInvalidChannels},
		{"channels 5", mutate(func(b []byte) { b[12] = 5 }), ErrInvalidChannels},
		{"colorspace 2", mutate(func(b []byte) { b[13] = 2 }), ErrInvalidColorspace},
		{"too large", mutate(func(b []byte) {
			b[4], b[5], b[6], b[7] = 0, 0, 0x4e, 0x20   // 20000
			b[8], b[9], b[10], b[11] = 0, 0, 0x4e, 0x21 // 20001
		}), ErrTooLarge},
		{"truncated before marker", valid[:len(valid)-1], ErrInvalidEndMarker},
		{"bad marker", mutate(func(b []byte) { b[len(b)-1] = 2 }), ErrInvalidEndMarker},
		{"missing marker", valid[:len(valid)-EndMarkerSize], ErrUnexpectedEOF},
		{"truncated chunk", valid[:HeaderSize+1], ErrUnexpectedEOF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Unmarshal(tt.data)
			if err == nil {
				t.Fatal("Unmarshal succeeded, want error")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.is)
			}
		})
	}
}

func TestReadHeader(t *testing.T) {
	img := &Image{Width: 7, Height: 9, Channels: 3, Colorspace: Linear, Pix: make([]byte, 7*9*3)}
	data, err := Marshal(img)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := Header{Width: 7, Height: 9, Channels: 3, Colorspace: Linear}
	if h != want {
		t.Fatalf("header = %+v, want %+v", h, want)
	}
	if _, err := ReadHeader(bytes.NewReader(data[:HeaderSize-1])); !errors.Is(err, ErrUnexpectedEOF) {
		t.Fatalf("short header error = %v, want ErrUnexpectedEOF", err)
	}
}

func TestMarshalErrors(t *testing.T) {
	valid := &Image{Width: 1, Height: 1, Channels: 4, Pix: []byte{1, 2, 3, 4}}
	tests := []struct {
		name string
		img  *Image
		is   error
	}{
		{"nil", nil, ErrNilImage},
		{"zero width", &Image{Width: 0, Height: 1, Channels: 3}, ErrEmptyImage},
		{"zero height", &Image{Width: 1, Height: 0, Channels: 3}, ErrEmptyImage},
		{"negative width", &Image{Width: -1, Height: 1, Channels: 3}, ErrEmptyImage},
		{"channels 2", &Image{Width: 1, Height: 1, Channels: 2}, ErrInvalidChannels},
		{"channels 5", &Image{Width: 1, Height: 1, Channels: 5}, ErrInvalidChannels},
		{"colorspace 2", &Image{Width: 1, Height: 1, Channels: 3, Colorspace: 2}, ErrInvalidColorspace},
		{"short pix", &Image{Width: 2, Height: 2, Channels: 4, Pix: make([]byte, 15)}, ErrInvalidPixLen},
		{"long pix", &Image{Width: 2, Height: 2, Channels: 4, Pix: make([]byte, 17)}, ErrInvalidPixLen},
		{"too large", &Image{Width: 20000, Height: 20001, Channels: 3}, ErrTooLarge},
		{"width over uint32", &Image{Width: int(^uint(0) >> 1), Height: 2, Channels: 3}, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Marshal(tt.img)
			if err == nil {
				t.Fatal("Marshal succeeded, want error")
			}
			if !errors.Is(err, tt.is) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.is)
			}
		})
	}

	if err := EncodeImage(&shortWriter{}, valid); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("EncodeImage short write error = %v, want io.ErrShortWrite", err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestOpaque(t *testing.T) {
	if !(&Image{Channels: 3, Pix: []byte{1, 2, 3}}).Opaque() {
		t.Fatal("three-channel image should be opaque")
	}
	if !(&Image{Channels: 4, Pix: []byte{1, 2, 3, 255}}).Opaque() {
		t.Fatal("image with alpha 255 should be opaque")
	}
	if (&Image{Channels: 4, Pix: []byte{1, 2, 3, 254}}).Opaque() {
		t.Fatal("image with alpha 254 should not be opaque")
	}
	if (*Image)(nil).Opaque() {
		t.Fatal("nil image should not be opaque")
	}
	if (&Image{Channels: 0, Pix: []byte{1, 2, 3}}).Opaque() {
		t.Fatal("image with invalid channel count should not be opaque")
	}
}

func TestImageIntegration(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	colors := []color.NRGBA{
		{0, 0, 0, 0},
		{255, 0, 0, 255},
		{0, 255, 0, 128},
		{0, 0, 255, 64},
		{12, 34, 56, 78},
		{255, 255, 255, 255},
	}
	for i, c := range colors {
		src.SetNRGBA(i%3, i/3, c)
	}

	var buf bytes.Buffer
	if err := Encode(&buf, src); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	data := buf.Bytes()

	h, err := ReadHeader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if h.Channels != 4 {
		t.Fatalf("channels = %d, want 4 for an image with transparency", h.Channels)
	}

	decoded, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Bounds() != src.Bounds() {
		t.Fatalf("bounds = %v, want %v", decoded.Bounds(), src.Bounds())
	}
	for i, want := range colors {
		got := color.NRGBAModel.Convert(decoded.At(i%3, i/3)).(color.NRGBA)
		if got != want {
			t.Fatalf("pixel %d = %v, want %v", i, got, want)
		}
	}

	// image.Decode should find the registered format.
	regImg, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("image.Decode: %v", err)
	}
	if format != "qoi" {
		t.Fatalf("format = %q, want qoi", format)
	}
	if regImg.Bounds() != src.Bounds() {
		t.Fatalf("registered decode bounds = %v, want %v", regImg.Bounds(), src.Bounds())
	}

	cfg, err := DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width != 3 || cfg.Height != 2 || cfg.ColorModel != color.NRGBAModel {
		t.Fatalf("config = %+v", cfg)
	}
	regCfg, regFormat, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("image.DecodeConfig: %v", err)
	}
	if regFormat != "qoi" || regCfg.Width != 3 || regCfg.Height != 2 {
		t.Fatalf("registered config = %+v, format %q", regCfg, regFormat)
	}
}

func TestEncodeOpaqueChoosesRGB(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetNRGBA(x, y, color.NRGBA{byte(x * 17), byte(y * 17), 42, 255})
		}
	}
	var buf bytes.Buffer
	if err := Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if h.Channels != 3 {
		t.Fatalf("channels = %d, want 3", h.Channels)
	}

	decoded, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			want := src.NRGBAAt(x, y)
			got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestFromImageSubImage(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 5, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			src.SetNRGBA(x, y, color.NRGBA{byte(x * 20), byte(y * 30), byte(x + y), byte(100 + x + y)})
		}
	}
	sub := src.SubImage(image.Rect(1, 1, 4, 3)).(*image.NRGBA)

	img, err := FromImage(sub)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != 3 || img.Height != 2 || img.Channels != 4 {
		t.Fatalf("image = %dx%d/%d", img.Width, img.Height, img.Channels)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			i := (y*3 + x) * 4
			want := sub.NRGBAAt(x+1, y+1)
			got := color.NRGBA{R: img.Pix[i], G: img.Pix[i+1], B: img.Pix[i+2], A: img.Pix[i+3]}
			if got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestFromImagePremultiplied(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.SetRGBA(0, 0, color.RGBA{100, 50, 25, 128})
	src.SetRGBA(1, 0, color.RGBA{0, 0, 0, 0})
	img, err := FromImage(src)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 2; x++ {
		want := color.NRGBAModel.Convert(src.At(x, 0)).(color.NRGBA)
		i := x * 4
		got := color.NRGBA{R: img.Pix[i], G: img.Pix[i+1], B: img.Pix[i+2], A: img.Pix[i+3]}
		if got != want {
			t.Fatalf("pixel %d = %v, want %v", x, got, want)
		}
	}
}

func TestFromImageGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 3, 1))
	src.SetGray(0, 0, color.Gray{0})
	src.SetGray(1, 0, color.Gray{127})
	src.SetGray(2, 0, color.Gray{255})
	img, err := FromImage(src)
	if err != nil {
		t.Fatal(err)
	}
	if img.Channels != 3 {
		t.Fatalf("channels = %d, want 3", img.Channels)
	}
	want := []byte{0, 0, 0, 127, 127, 127, 255, 255, 255}
	if !bytes.Equal(img.Pix, want) {
		t.Fatalf("pixels = %v, want %v", img.Pix, want)
	}
}

func TestToImage(t *testing.T) {
	img := &Image{Width: 2, Height: 1, Channels: 3, Pix: []byte{1, 2, 3, 4, 5, 6}}
	m := img.ToImage()
	if m == nil {
		t.Fatal("ToImage returned nil")
	}
	nrgba := m.(*image.NRGBA)
	if nrgba.NRGBAAt(0, 0) != (color.NRGBA{1, 2, 3, 255}) {
		t.Fatalf("pixel 0 = %v", nrgba.NRGBAAt(0, 0))
	}
	if nrgba.NRGBAAt(1, 0) != (color.NRGBA{4, 5, 6, 255}) {
		t.Fatalf("pixel 1 = %v", nrgba.NRGBAAt(1, 0))
	}
	if (*Image)(nil).ToImage() != nil {
		t.Fatal("nil image should produce nil")
	}
}

func TestFromImageErrors(t *testing.T) {
	if _, err := FromImage(nil); !errors.Is(err, ErrNilImage) {
		t.Fatalf("nil image error = %v", err)
	}
	if _, err := FromImage(image.NewNRGBA(image.Rect(0, 0, 0, 0))); !errors.Is(err, ErrEmptyImage) {
		t.Fatalf("empty image error = %v", err)
	}
}

func TestHeaderValidate(t *testing.T) {
	tests := []struct {
		h  Header
		is error
	}{
		{Header{Width: 0, Height: 1, Channels: 3}, ErrEmptyImage},
		{Header{Width: 1, Height: 0, Channels: 3}, ErrEmptyImage},
		{Header{Width: 1, Height: 1, Channels: 0}, ErrInvalidChannels},
		{Header{Width: 1, Height: 1, Channels: 4, Colorspace: 2}, ErrInvalidColorspace},
		{Header{Width: 20000, Height: 20001, Channels: 3}, ErrTooLarge},
	}
	for _, tt := range tests {
		if err := tt.h.Validate(); !errors.Is(err, tt.is) {
			t.Fatalf("Validate(%+v) = %v, want %v", tt.h, err, tt.is)
		}
	}
}

func FuzzUnmarshal(f *testing.F) {
	for _, v := range referenceVectors {
		f.Add(mustHex(f, v.encoded))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		h, err := ReadHeader(bytes.NewReader(data))
		if err != nil {
			return
		}
		// Avoid enormous allocations while fuzzing.
		if uint64(h.Width)*uint64(h.Height) > 1<<20 {
			return
		}
		img, err := Unmarshal(data)
		if err != nil {
			return
		}
		if err := img.Validate(); err != nil {
			t.Fatalf("decoded image is invalid: %v", err)
		}
		reencoded, err := Marshal(img)
		if err != nil {
			t.Fatalf("re-encoding decoded image: %v", err)
		}
		again, err := Unmarshal(reencoded)
		if err != nil {
			t.Fatalf("decoding re-encoded image: %v", err)
		}
		if !bytes.Equal(again.Pix, img.Pix) {
			t.Fatal("pixels changed after re-encoding")
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	const w, h = 256, 256
	pix := make([]byte, w*h*4)
	for i := range pix {
		pix[i] = byte(i*7 + i/13)
	}
	img := &Image{Width: w, Height: h, Channels: 4, Pix: pix}
	b.ReportAllocs()
	b.SetBytes(int64(len(pix)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Marshal(img); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	const w, h = 256, 256
	pix := make([]byte, w*h*4)
	for i := range pix {
		pix[i] = byte(i*7 + i/13)
	}
	data, err := Marshal(&Image{Width: w, Height: h, Channels: 4, Pix: pix})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Unmarshal(data); err != nil {
			b.Fatal(err)
		}
	}
}
