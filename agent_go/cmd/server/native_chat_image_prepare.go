package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	nativeChatImageMaxBytes     = 10 << 20
	nativeChatTurnImageMaxBytes = 80 << 20
	nativeChatImageMaxSide      = 2000
	// Bound decoded allocations before invoking a codec on compressed input.
	nativeChatImageMaxPixels = 16_000_000
)

type preparedNativeChatImage struct {
	MIME          string
	Width, Height int
	Bytes         int64
	FirstFrame    bool
}

var errNativeChatImageSize = errors.New("image_too_large")

type nativeChatImageBuffer struct{ bytes.Buffer }

func (b *nativeChatImageBuffer) Write(p []byte) (int, error) {
	if len(p) > nativeChatImageMaxBytes-b.Len() {
		return 0, errNativeChatImageSize
	}
	return b.Buffer.Write(p)
}

// prepareNativeChatImage handles one already-authorized file, never a path or
// access grant. Write into a private prepared file, not conversation history.
// No decoded pixels or base64 escape this call. Callers process files serially.
// HEIC is deliberately not registered in phase one: it needs a separately
// qualified bounded decoder, not an implicit host-installed conversion tool.
var nativeChatImageDecodeSlot = make(chan struct{}, 1)

func prepareNativeChatImage(source io.ReadSeeker, destination io.Writer) (preparedNativeChatImage, error) {
	return prepareNativeChatImageContext(context.Background(), source, destination)
}
func prepareNativeChatImageContext(ctx context.Context, source io.ReadSeeker, destination io.Writer) (preparedNativeChatImage, error) {
	select {
	case nativeChatImageDecodeSlot <- struct{}{}:
		defer func() { <-nativeChatImageDecodeSlot }()
	case <-ctx.Done():
		return preparedNativeChatImage{}, ctx.Err()
	}
	return prepareNativeChatImageUnlocked(source, destination)
}
func prepareNativeChatImageUnlocked(source io.ReadSeeker, destination io.Writer) (preparedNativeChatImage, error) {
	var result preparedNativeChatImage
	size, err := source.Seek(0, io.SeekEnd)
	if err != nil {
		return result, fmt.Errorf("image_seek: %w", err)
	}
	if size <= 0 || size > nativeChatImageMaxBytes {
		return result, errNativeChatImageSize
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	config, format, err := image.DecodeConfig(io.LimitReader(source, nativeChatImageMaxBytes))
	if err != nil {
		return result, fmt.Errorf("image_header: %w", err)
	}
	switch format {
	case "png", "jpeg", "gif", "webp":
	default:
		return result, errors.New("image_format_unavailable")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > nativeChatImageMaxPixels {
		return result, errors.New("image_pixel_limit")
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	orientation := 1
	if format == "jpeg" {
		orientation = nativeChatJPEGOrientation(source)
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return result, err
		}
	}
	pixels, _, err := image.Decode(io.LimitReader(source, nativeChatImageMaxBytes))
	if err != nil {
		return result, fmt.Errorf("image_decode: %w", err)
	}
	if format == "gif" && pixels.Bounds() != image.Rect(0, 0, config.Width, config.Height) {
		screen := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
		draw.Draw(screen, pixels.Bounds(), pixels, pixels.Bounds().Min, draw.Over)
		pixels = screen
	}
	w, h := config.Width, config.Height
	if w > nativeChatImageMaxSide || h > nativeChatImageMaxSide {
		if w >= h {
			h = max(1, h*nativeChatImageMaxSide/w)
			w = nativeChatImageMaxSide
		} else {
			w = max(1, w*nativeChatImageMaxSide/h)
			h = nativeChatImageMaxSide
		}
		smaller := image.NewNRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(smaller, smaller.Bounds(), pixels, pixels.Bounds(), draw.Src, nil)
		pixels = smaller
	}
	// Rotate only after shrinking: never allocate a second full-size photo.
	if orientation > 1 {
		ow, oh := w, h
		if orientation >= 5 {
			ow, oh = h, w
		}
		oriented := image.NewNRGBA(image.Rect(0, 0, ow, oh))
		for y := range oh {
			for x := range ow {
				sx, sy := x, y
				switch orientation {
				case 2:
					sx = w - 1 - x
				case 3:
					sx, sy = w-1-x, h-1-y
				case 4:
					sy = h - 1 - y
				case 5:
					sx, sy = y, x
				case 6:
					sx, sy = y, h-1-x
				case 7:
					sx, sy = w-1-y, h-1-x
				case 8:
					sx, sy = w-1-y, x
				}
				oriented.Set(x, y, pixels.At(sx+pixels.Bounds().Min.X, sy+pixels.Bounds().Min.Y))
			}
		}
		pixels, w, h = oriented, ow, oh
	}
	var encoded nativeChatImageBuffer
	result.MIME = "image/png"
	// Keep alpha and diagrams lossless where they fit; photographs use JPEG.
	if format != "jpeg" {
		err = png.Encode(&encoded, pixels)
	} else {
		err = errNativeChatImageSize
	}
	if err != nil {
		if !errors.Is(err, errNativeChatImageSize) {
			return result, fmt.Errorf("image_encode: %w", err)
		}
		if opaque, ok := pixels.(interface{ Opaque() bool }); !ok || !opaque.Opaque() {
			return result, errNativeChatImageSize
		}
		result.MIME = "image/jpeg"
		for _, quality := range []int{90, 80, 70, 60} {
			encoded.Reset()
			err = jpeg.Encode(&encoded, pixels, &jpeg.Options{Quality: quality})
			if err == nil {
				break
			}
			if !errors.Is(err, errNativeChatImageSize) {
				return result, fmt.Errorf("image_encode: %w", err)
			}
		}
		if err != nil {
			return result, err
		}
	}
	result.Width, result.Height = w, h
	result.FirstFrame = format == "gif"
	result.Bytes, err = io.Copy(destination, bytes.NewReader(encoded.Bytes()))
	if err != nil {
		return preparedNativeChatImage{}, fmt.Errorf("image_write: %w", err)
	}
	return result, nil
}

// Inspect bounded JPEG APP1 metadata; unknown/malformed EXIF is not an access
// or decode authority. The image codec still validates the actual image.
func nativeChatJPEGOrientation(source io.Reader) int {
	var header [4]byte
	if _, err := io.ReadFull(source, header[:2]); err != nil || header[0] != 0xff || header[1] != 0xd8 {
		return 1
	}
	for {
		if _, err := io.ReadFull(source, header[:]); err != nil || header[0] != 0xff || header[1] == 0xda || header[1] == 0xd9 {
			return 1
		}
		length := int(binary.BigEndian.Uint16(header[2:])) - 2
		if length < 0 {
			return 1
		}
		if header[1] != 0xe1 {
			if _, err := io.CopyN(io.Discard, source, int64(length)); err != nil {
				return 1
			}
			continue
		}
		data := make([]byte, length) // JPEG segment length is a uint16.
		if _, err := io.ReadFull(source, data); err != nil {
			return 1
		}
		if len(data) < 14 || !bytes.Equal(data[:6], []byte("Exif\x00\x00")) {
			continue
		}
		tiff := data[6:]
		var order binary.ByteOrder
		switch string(tiff[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			return 1
		}
		if order.Uint16(tiff[2:4]) != 42 {
			return 1
		}
		offset := uint64(order.Uint32(tiff[4:8]))
		if offset+2 > uint64(len(tiff)) {
			return 1
		}
		count := int(order.Uint16(tiff[offset : offset+2]))
		for i := range count {
			start := offset + 2 + uint64(i)*12
			if start+12 > uint64(len(tiff)) {
				return 1
			}
			entry := tiff[start : start+12]
			if order.Uint16(entry[:2]) == 0x112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
				if value := int(order.Uint16(entry[8:10])); value >= 1 && value <= 8 {
					return value
				}
				return 1
			}
		}
		return 1
	}
}
