// Command qoi converts between QOI and other image formats.
//
// Usage:
//
//	qoi encode input.png output.qoi
//	qoi decode input.qoi output.png
//	qoi info input.qoi
//
// Short options are also accepted: -e, -d, -i, and -h.
package main

import (
	"bufio"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	qoi "github.com/dshot/qoi-go/qoi"
)

func main() {
	if len(os.Args) < 2 {
		usage(2)
	}
	switch os.Args[1] {
	case "encode", "e", "-e":
		if len(os.Args) < 3 || len(os.Args) > 4 {
			usage(2)
		}
		input := os.Args[2]
		output := defaultOutput(input, ".qoi")
		if len(os.Args) == 4 {
			output = os.Args[3]
		}
		if err := encodeFile(input, output); err != nil {
			fatal(err)
		}
	case "decode", "d", "-d":
		if len(os.Args) < 3 || len(os.Args) > 4 {
			usage(2)
		}
		input := os.Args[2]
		output := defaultOutput(input, ".png")
		if len(os.Args) == 4 {
			output = os.Args[3]
		}
		if err := decodeFile(input, output); err != nil {
			fatal(err)
		}
	case "info", "i", "-i":
		if len(os.Args) != 3 {
			usage(2)
		}
		if err := infoFile(os.Args[2]); err != nil {
			fatal(err)
		}
	case "help", "-h", "--help":
		usage(0)
	default:
		fmt.Fprintf(os.Stderr, "qoi: unknown command %q\n\n", os.Args[1])
		usage(2)
	}
}

func defaultOutput(input, ext string) string {
	return strings.TrimSuffix(input, filepath.Ext(input)) + ext
}

func usage(code int) {
	fmt.Fprintf(os.Stderr, `Usage:
  qoi encode <input> [output.qoi]   (alias: -e)
  qoi decode <input.qoi> [output]   (alias: -d)
  qoi info <input.qoi>              (alias: -i)
  qoi help                          (alias: -h, --help)

If output is omitted, the input extension is replaced: .qoi for encode, .png for decode.
`)
	os.Exit(code)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "qoi: %v\n", err)
	os.Exit(1)
}

func encodeFile(input, output string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := ensureDistinct(f, output); err != nil {
		return err
	}

	img, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("decoding %s: %w", input, err)
	}

	out, err := os.Create(output)
	if err != nil {
		return err
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	if err := qoi.Encode(w, img); err != nil {
		return fmt.Errorf("encoding %s: %w", output, err)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return out.Close()
}

func decodeFile(input, output string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := ensureDistinct(f, output); err != nil {
		return err
	}

	img, err := qoi.Decode(bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("decoding %s: %w", input, err)
	}

	ext := strings.ToLower(filepath.Ext(output))
	switch ext {
	case ".jpg", ".jpeg", ".png", "":
	default:
		return fmt.Errorf("unsupported output format %q (use .png or .jpg)", ext)
	}

	out, err := os.Create(output)
	if err != nil {
		return err
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	switch ext {
	case ".jpg", ".jpeg":
		err = jpeg.Encode(w, img, &jpeg.Options{Quality: 95})
	default:
		err = png.Encode(w, img)
	}
	if err != nil {
		return fmt.Errorf("encoding %s: %w", output, err)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return out.Close()
}

func infoFile(input string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()

	h, err := qoi.ReadHeader(f)
	if err != nil {
		return fmt.Errorf("reading %s: %w", input, err)
	}
	fmt.Printf("%s: %dx%d, %d channels, %s\n", input, h.Width, h.Height, h.Channels, h.Colorspace)
	return nil
}

// ensureDistinct rejects an output path that refers to the input file, which
// would otherwise truncate the input before it is read.
func ensureDistinct(in *os.File, output string) error {
	inInfo, err := in.Stat()
	if err != nil {
		return err
	}
	outInfo, err := os.Stat(output)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if os.SameFile(inInfo, outInfo) {
		return fmt.Errorf("input and output are the same file: %s", output)
	}
	return nil
}
