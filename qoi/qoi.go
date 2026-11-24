// Package qoi implements the Quite OK Image format (QOI), a fast,
// lossless image compression format.
//
// The package provides both a low-level API that works on raw RGB/RGBA
// pixel buffers (Image, Marshal, Unmarshal, EncodeImage, DecodeImage) and
// integration with Go's standard image package (Encode, Decode,
// DecodeConfig). Importing this package registers the "qoi" format with
// image.RegisterFormat, so image.Decode and image.DecodeConfig can read QOI
// files after the package is imported.
//
// See https://qoiformat.org and the QOI specification for details.
package qoi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// Magic is the four-byte magic value at the start of every QOI file.
	Magic = "qoif"

	// HeaderSize is the size of the QOI file header in bytes.
	HeaderSize = 14

	// EndMarkerSize is the size of the QOI end marker in bytes.
	EndMarkerSize = 8

	// MaxPixels is the maximum number of pixels accepted by this
	// implementation. It matches the limit of the reference C
	// implementation (400 million pixels).
	MaxPixels = 400_000_000
)

// Chunk tags and masks from the QOI specification.
const (
	opIndex = 0x00 // 00xxxxxx
	opDiff  = 0x40 // 01xxxxxx
	opLuma  = 0x80 // 10xxxxxx
	opRun   = 0xc0 // 11xxxxxx
	opRGB   = 0xfe // 11111110
	opRGBA  = 0xff // 11111111
	mask2   = 0xc0 // 11000000
)

var endMarker = [EndMarkerSize]byte{0, 0, 0, 0, 0, 0, 0, 1}

// Errors returned by this package. Errors that carry additional context
// wrap one of these values, so use errors.Is to test for them.
var (
	ErrInvalidMagic      = errors.New("qoi: invalid magic")
	ErrInvalidChannels   = errors.New("qoi: channels must be 3 (RGB) or 4 (RGBA)")
	ErrInvalidColorspace = errors.New("qoi: colorspace must be 0 (sRGB) or 1 (linear)")
	ErrTooLarge          = errors.New("qoi: image is too large")
	ErrInvalidPixLen     = errors.New("qoi: pixel buffer length does not match dimensions")
	ErrUnexpectedEOF     = errors.New("qoi: unexpected end of data")
	ErrInvalidEndMarker  = errors.New("qoi: invalid end marker")
	ErrNilImage          = errors.New("qoi: nil image")
	ErrEmptyImage        = errors.New("qoi: image dimensions must be positive")
)

// Colorspace describes how the color channels of an image are encoded.
// It is purely informative and does not change how chunks are encoded.
type Colorspace uint8

const (
	// SRGB means the color channels are sRGB and alpha is linear.
	SRGB Colorspace = 0
	// Linear means all channels are linear.
	Linear Colorspace = 1
)

// String returns the name of the colorspace.
func (c Colorspace) String() string {
	switch c {
	case SRGB:
		return "sRGB"
	case Linear:
		return "linear"
	default:
		return fmt.Sprintf("Colorspace(%d)", uint8(c))
	}
}

// Header is the fixed 14-byte header of a QOI file.
type Header struct {
	Width      uint32
	Height     uint32
	Channels   uint8
	Colorspace Colorspace
}

// Validate checks that the header describes a valid QOI image.
func (h Header) Validate() error {
	if h.Width == 0 || h.Height == 0 {
		return ErrEmptyImage
	}
	if h.Channels != 3 && h.Channels != 4 {
		return fmt.Errorf("%w: got %d", ErrInvalidChannels, h.Channels)
	}
	if h.Colorspace > Linear {
		return fmt.Errorf("%w: got %d", ErrInvalidColorspace, uint8(h.Colorspace))
	}
	if uint64(h.Width)*uint64(h.Height) > MaxPixels {
		return fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrTooLarge, h.Width, h.Height, MaxPixels)
	}
	return nil
}

// Image is a decoded QOI image in raw, non-premultiplied RGB or RGBA form.
//
// Pix is laid out row by row, left to right, top to bottom, with Channels
// bytes per pixel (3 for RGB, 4 for RGBA). Its length must be
// Width*Height*Channels.
type Image struct {
	Width      int
	Height     int
	Channels   int
	Colorspace Colorspace
	Pix        []byte
}

// Header returns the QOI header describing the image.
func (img *Image) Header() Header {
	if img == nil {
		return Header{}
	}
	return Header{
		Width:      uint32(img.Width),
		Height:     uint32(img.Height),
		Channels:   uint8(img.Channels),
		Colorspace: img.Colorspace,
	}
}

// Validate checks that the image fields are consistent.
func (img *Image) Validate() error {
	if img == nil {
		return ErrNilImage
	}
	h := img.Header()
	if img.Width <= 0 || img.Height <= 0 {
		return ErrEmptyImage
	}
	if uint64(img.Width) > uint64(^uint32(0)) || uint64(img.Height) > uint64(^uint32(0)) {
		return ErrTooLarge
	}
	if img.Channels != 3 && img.Channels != 4 {
		return fmt.Errorf("%w: got %d", ErrInvalidChannels, img.Channels)
	}
	if err := h.Validate(); err != nil {
		return err
	}
	want := uint64(img.Width) * uint64(img.Height) * uint64(img.Channels)
	if uint64(len(img.Pix)) != want {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidPixLen, len(img.Pix), want)
	}
	return nil
}

// Opaque reports whether every pixel in the image has an alpha value of 255.
// Images with three channels are always opaque.
func (img *Image) Opaque() bool {
	if img == nil {
		return false
	}
	if img.Channels == 3 {
		return true
	}
	if img.Channels != 4 {
		return false
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return false
		}
	}
	return true
}

// pixel is the internal representation of a single color.
type pixel struct {
	r, g, b, a uint8
}

// hash returns the position of p in the 64-entry color index.
func (p pixel) hash() int {
	return int((uint32(p.r)*3 + uint32(p.g)*5 + uint32(p.b)*7 + uint32(p.a)*11) & 63)
}

// ReadHeader reads and validates only the 14-byte QOI header from r.
func ReadHeader(r io.Reader) (Header, error) {
	var b [HeaderSize]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Header{}, fmt.Errorf("%w: reading header: %w", ErrUnexpectedEOF, err)
	}
	return parseHeader(b[:])
}

func parseHeader(data []byte) (Header, error) {
	if len(data) < HeaderSize {
		return Header{}, fmt.Errorf("%w: got %d header bytes", ErrUnexpectedEOF, len(data))
	}
	if string(data[:4]) != Magic {
		return Header{}, fmt.Errorf("%w: got %q", ErrInvalidMagic, data[:4])
	}
	h := Header{
		Width:      binary.BigEndian.Uint32(data[4:8]),
		Height:     binary.BigEndian.Uint32(data[8:12]),
		Channels:   data[12],
		Colorspace: Colorspace(data[13]),
	}
	if err := h.Validate(); err != nil {
		return Header{}, err
	}
	return h, nil
}

func putHeader(dst []byte, h Header) {
	copy(dst[0:4], Magic)
	binary.BigEndian.PutUint32(dst[4:8], h.Width)
	binary.BigEndian.PutUint32(dst[8:12], h.Height)
	dst[12] = h.Channels
	dst[13] = byte(h.Colorspace)
}

// maxEncodedSize returns the largest possible encoded size for an image with
// the given header, including the header and end marker. A chunk can use up
// to five bytes per pixel (QOI_OP_RGBA), regardless of the channel count in
// the header.
func maxEncodedSize(h Header) int64 {
	pixels := int64(h.Width) * int64(h.Height)
	return HeaderSize + pixels*5 + EndMarkerSize
}

// maxEncodeSize returns the largest size Marshal can produce for an image
// with the given header.
func maxEncodeSize(h Header) int64 {
	pixels := int64(h.Width) * int64(h.Height)
	return HeaderSize + pixels*int64(h.Channels+1) + EndMarkerSize
}
