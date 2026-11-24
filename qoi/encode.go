package qoi

import (
	"io"
)

// Marshal encodes img and returns the complete QOI file as a byte slice.
func Marshal(img *Image) ([]byte, error) {
	if err := img.Validate(); err != nil {
		return nil, err
	}

	h := img.Header()
	out := make([]byte, HeaderSize, maxEncodeSize(h))
	putHeader(out, h)

	channels := img.Channels
	pxLen := len(img.Pix)
	pxEnd := pxLen - channels

	var index [64]pixel
	prev := pixel{a: 255}
	run := 0

	for pos := 0; pos < pxLen; pos += channels {
		px := pixel{
			r: img.Pix[pos+0],
			g: img.Pix[pos+1],
			b: img.Pix[pos+2],
			a: prev.a,
		}
		if channels == 4 {
			px.a = img.Pix[pos+3]
		}

		if px == prev {
			run++
			if run == 62 || pos == pxEnd {
				out = append(out, opRun|byte(run-1))
				run = 0
			}
		} else {
			if run > 0 {
				out = append(out, opRun|byte(run-1))
				run = 0
			}

			ip := px.hash()
			if index[ip] == px {
				out = append(out, byte(ip))
			} else {
				index[ip] = px

				if px.a == prev.a {
					vr := int8(px.r - prev.r)
					vg := int8(px.g - prev.g)
					vb := int8(px.b - prev.b)
					vgR := int8(vr - vg)
					vgB := int8(vb - vg)

					switch {
					case vr > -3 && vr < 2 && vg > -3 && vg < 2 && vb > -3 && vb < 2:
						out = append(out, opDiff|
							byte(vr+2)<<4|
							byte(vg+2)<<2|
							byte(vb+2))
					case vgR > -9 && vgR < 8 && vg > -33 && vg < 32 && vgB > -9 && vgB < 8:
						out = append(out,
							opLuma|byte(vg+32),
							byte(vgR+8)<<4|byte(vgB+8))
					default:
						out = append(out, opRGB, px.r, px.g, px.b)
					}
				} else {
					out = append(out, opRGBA, px.r, px.g, px.b, px.a)
				}
			}
		}

		prev = px
	}

	out = append(out, endMarker[:]...)
	// The initial capacity covers the worst case (five bytes per pixel).
	// For highly compressible images, avoid retaining that much memory.
	if cap(out)-len(out) > len(out)/2+1024 {
		clipped := make([]byte, len(out))
		copy(clipped, out)
		return clipped, nil
	}
	return out, nil
}

// EncodeImage writes the QOI encoding of img to w.
func EncodeImage(w io.Writer, img *Image) error {
	data, err := Marshal(img)
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
