package qoi

import (
	"fmt"
	"image"
	"image/color"
	"io"
)

func init() {
	image.RegisterFormat("qoi", Magic, Decode, DecodeConfig)
}

// Decode reads a QOI image from r and returns it as an image.Image.
//
// The returned image is an *image.NRGBA. QOI stores non-premultiplied color
// channels, which matches image.NRGBA.
func Decode(r io.Reader) (image.Image, error) {
	img, err := DecodeImage(r)
	if err != nil {
		return nil, err
	}
	return img.ToImage(), nil
}

// DecodeConfig reads only the QOI header from r and returns the image
// configuration.
func DecodeConfig(r io.Reader) (image.Config, error) {
	h, err := ReadHeader(r)
	if err != nil {
		return image.Config{}, err
	}
	return image.Config{
		ColorModel: color.NRGBAModel,
		Width:      int(h.Width),
		Height:     int(h.Height),
	}, nil
}

// Encode writes m to w in QOI format.
//
// The image is converted to non-premultiplied 8-bit color. If every pixel is
// opaque, Encode writes a three-channel (RGB) file; otherwise it writes a
// four-channel (RGBA) file. The colorspace is set to SRGB.
func Encode(w io.Writer, m image.Image) error {
	img, err := FromImage(m)
	if err != nil {
		return err
	}
	return EncodeImage(w, img)
}

// FromImage converts a Go image to a raw QOI Image. Like Encode, it uses
// three channels when the source is fully opaque and four otherwise, and
// records the SRGB colorspace.
func FromImage(m image.Image) (*Image, error) {
	if m == nil {
		return nil, ErrNilImage
	}

	b := m.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return nil, ErrEmptyImage
	}
	if uint64(width)*uint64(height) > MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrTooLarge, width, height, MaxPixels)
	}

	pix := make([]byte, width*height*4)
	opaque := true

	if src, ok := m.(*image.NRGBA); ok {
		rowBytes := width * 4
		for y := 0; y < height; y++ {
			srcRow := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			dstRow := pix[y*rowBytes : (y+1)*rowBytes]
			copy(dstRow, srcRow[:rowBytes])
			for x := 3; x < rowBytes; x += 4 {
				if dstRow[x] != 0xff {
					opaque = false
				}
			}
		}
	} else {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				c := color.NRGBAModel.Convert(m.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				i := (y*width + x) * 4
				pix[i+0] = c.R
				pix[i+1] = c.G
				pix[i+2] = c.B
				pix[i+3] = c.A
				if c.A != 0xff {
					opaque = false
				}
			}
		}
	}

	channels := 4
	if opaque {
		// Compact the RGB channels in place. The destination index is
		// always smaller than the source index, so this is safe.
		channels = 3
		for i, j := 0, 0; i < len(pix); i, j = i+4, j+3 {
			pix[j+0] = pix[i+0]
			pix[j+1] = pix[i+1]
			pix[j+2] = pix[i+2]
		}
		pix = pix[:width*height*3]
	}

	return &Image{
		Width:      width,
		Height:     height,
		Channels:   channels,
		Colorspace: SRGB,
		Pix:        pix,
	}, nil
}

// ToImage converts a raw QOI Image to an *image.NRGBA.
//
// The returned image shares the image's pixel buffer when the image already
// has four channels. Otherwise a new four-channel buffer is allocated.
// ToImage returns nil for a nil or invalid image.
func (img *Image) ToImage() image.Image {
	if err := img.Validate(); err != nil {
		return nil
	}
	rect := image.Rect(0, 0, img.Width, img.Height)

	switch img.Channels {
	case 4:
		return &image.NRGBA{
			Pix:    img.Pix,
			Stride: img.Width * 4,
			Rect:   rect,
		}
	case 3:
		n := img.Width * img.Height
		pix := make([]byte, n*4)
		for i, j := 0, 0; i < n*3; i, j = i+3, j+4 {
			pix[j+0] = img.Pix[i+0]
			pix[j+1] = img.Pix[i+1]
			pix[j+2] = img.Pix[i+2]
			pix[j+3] = 0xff
		}
		return &image.NRGBA{
			Pix:    pix,
			Stride: img.Width * 4,
			Rect:   rect,
		}
	default:
		return nil
	}
}
