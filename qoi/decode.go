package qoi

import (
	"bytes"
	"fmt"
	"io"
)

// Unmarshal decodes a complete QOI file from data.
//
// The returned Image owns a newly allocated pixel buffer. Unmarshal accepts
// trailing bytes after the end marker.
func Unmarshal(data []byte) (*Image, error) {
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	return decodeChunks(h, data[HeaderSize:])
}

// DecodeImage reads a QOI file from r and returns its raw pixels.
func DecodeImage(r io.Reader) (*Image, error) {
	h, err := ReadHeader(r)
	if err != nil {
		return nil, err
	}

	// A valid stream cannot be larger than this, so limiting the read
	// avoids unbounded allocations for malformed or hostile input.
	limit := maxEncodedSize(h) - HeaderSize
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("qoi: reading image data: %w", err)
	}
	return decodeChunks(h, data)
}

func decodeChunks(h Header, data []byte) (*Image, error) {
	pixels := int(h.Width) * int(h.Height)
	channels := int(h.Channels)

	if len(data) < EndMarkerSize {
		return nil, fmt.Errorf("%w: %d data bytes", ErrUnexpectedEOF, len(data))
	}
	// A run chunk covers at most 62 pixels, so a valid stream needs at
	// least this many bytes. Checking before allocating protects against
	// tiny inputs with enormous declared dimensions.
	if minChunks := (pixels + 61) / 62; len(data)-EndMarkerSize < minChunks {
		return nil, fmt.Errorf("%w: %d data bytes cannot encode %d pixels", ErrUnexpectedEOF, len(data), pixels)
	}

	pix := make([]byte, pixels*channels)

	var index [64]pixel
	px := pixel{a: 255}
	run := 0
	p := 0

	for i := 0; i < pixels; i++ {
		if run > 0 {
			run--
		} else {
			if p >= len(data) {
				return nil, fmt.Errorf("%w: at pixel %d of %d", ErrUnexpectedEOF, i, pixels)
			}
			b1 := data[p]
			p++

			switch b1 {
			case opRGB:
				if p+3 > len(data) {
					return nil, fmt.Errorf("%w: truncated RGB chunk at pixel %d", ErrUnexpectedEOF, i)
				}
				px.r = data[p+0]
				px.g = data[p+1]
				px.b = data[p+2]
				p += 3
			case opRGBA:
				if p+4 > len(data) {
					return nil, fmt.Errorf("%w: truncated RGBA chunk at pixel %d", ErrUnexpectedEOF, i)
				}
				px.r = data[p+0]
				px.g = data[p+1]
				px.b = data[p+2]
				px.a = data[p+3]
				p += 4
			default:
				switch b1 & mask2 {
				case opIndex:
					px = index[b1&0x3f]
				case opDiff:
					px.r += ((b1 >> 4) & 0x03) - 2
					px.g += ((b1 >> 2) & 0x03) - 2
					px.b += (b1 & 0x03) - 2
				case opLuma:
					if p >= len(data) {
						return nil, fmt.Errorf("%w: truncated luma chunk at pixel %d", ErrUnexpectedEOF, i)
					}
					b2 := data[p]
					p++
					vg := int(b1&0x3f) - 32
					px.r += uint8(vg - 8 + int((b2>>4)&0x0f))
					px.g += uint8(vg)
					px.b += uint8(vg - 8 + int(b2&0x0f))
				case opRun:
					run = int(b1 & 0x3f)
				}
			}

			index[px.hash()] = px
		}

		off := i * channels
		pix[off+0] = px.r
		pix[off+1] = px.g
		pix[off+2] = px.b
		if channels == 4 {
			pix[off+3] = px.a
		}
	}

	if p+EndMarkerSize > len(data) {
		return nil, fmt.Errorf("%w: missing end marker", ErrInvalidEndMarker)
	}
	if !bytes.Equal(data[p:p+EndMarkerSize], endMarker[:]) {
		return nil, fmt.Errorf("%w: got % x", ErrInvalidEndMarker, data[p:p+EndMarkerSize])
	}

	return &Image{
		Width:      int(h.Width),
		Height:     int(h.Height),
		Channels:   channels,
		Colorspace: h.Colorspace,
		Pix:        pix,
	}, nil
}
