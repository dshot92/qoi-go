package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	qoi "github.com/dshot/qoi-go/qoi"
)

func TestEncodeDecodeFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	qoiPath := filepath.Join(dir, "output.qoi")
	output := filepath.Join(dir, "output.png")

	src := image.NewNRGBA(image.Rect(0, 0, 5, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			src.SetNRGBA(x, y, color.NRGBA{byte(x * 20), byte(y * 30), byte(x * y), byte(255 - x - y)})
		}
	}
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := encodeFile(input, qoiPath); err != nil {
		t.Fatalf("encodeFile: %v", err)
	}

	qf, err := os.Open(qoiPath)
	if err != nil {
		t.Fatal(err)
	}
	h, err := qoi.ReadHeader(qf)
	qf.Close()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if h.Width != 5 || h.Height != 4 || h.Channels != 4 {
		t.Fatalf("header = %+v", h)
	}

	if err := decodeFile(qoiPath, output); err != nil {
		t.Fatalf("decodeFile: %v", err)
	}

	outFile, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	decoded, err := png.Decode(outFile)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			want := src.NRGBAAt(x, y)
			got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestDecodeFileUnsupportedOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.png")
	qoiPath := filepath.Join(dir, "output.qoi")

	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 255})
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := encodeFile(input, qoiPath); err != nil {
		t.Fatal(err)
	}
	if err := decodeFile(qoiPath, filepath.Join(dir, "output.bmp")); err == nil {
		t.Fatal("decodeFile succeeded with an unsupported output extension")
	}
}

func TestDefaultOutput(t *testing.T) {
	tests := []struct {
		input, ext, want string
	}{
		{"image.png", ".qoi", "image.qoi"},
		{"image.qoi", ".png", "image.png"},
		{"image", ".qoi", "image.qoi"},
		{"archive.tar.gz", ".qoi", "archive.tar.qoi"},
		{filepath.Join("some", "dir", "photo.jpeg"), ".qoi", filepath.Join("some", "dir", "photo.qoi")},
	}
	for _, tt := range tests {
		if got := defaultOutput(tt.input, tt.ext); got != tt.want {
			t.Fatalf("defaultOutput(%q, %q) = %q, want %q", tt.input, tt.ext, got, tt.want)
		}
	}
}

func TestEncodeRefusesSameFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "image.png")

	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 255})
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := encodeFile(input, input); err == nil {
		t.Fatal("encodeFile allowed the output to overwrite the input")
	}
}
