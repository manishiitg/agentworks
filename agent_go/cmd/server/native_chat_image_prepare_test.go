package server

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// PLAT-746-1000: compressed image headers must be bounded before decoding;
// prepared pixels must remain aspect-correct and preserve alpha.
func TestNativeChatImagePreparationBoundsPixelsAndEncodedOutput(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			picture := image.NewNRGBA(image.Rect(0, 0, 2100, 30))
			for y := range 30 {
				for x := range 2100 {
					picture.SetNRGBA(x, y, color.NRGBA{R: 200, A: 128})
				}
			}
			var source, out bytes.Buffer
			var err error
			if format == "png" {
				err = png.Encode(&source, picture)
			} else {
				err = jpeg.Encode(&source, picture, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareNativeChatImage(bytes.NewReader(source.Bytes()), &out)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Width != 2000 || prepared.Height != 28 || prepared.Bytes != int64(out.Len()) || out.Len() > nativeChatImageMaxBytes {
				t.Fatal("image bound/aspect lost", prepared)
			}
			decoded, _, err := image.Decode(bytes.NewReader(out.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if format == "png" {
				_, _, _, alpha := decoded.At(0, 0).RGBA()
				if alpha == 0xffff || alpha == 0 {
					t.Fatal("transparent pixels lost alpha")
				}
			}
		})
	}
	var pngFile bytes.Buffer
	if err := png.Encode(&pngFile, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// A valid IHDR with hostile dimensions; no pixel allocation is needed.
	header := append([]byte(nil), pngFile.Bytes()...)
	binary.BigEndian.PutUint32(header[16:20], 100000)
	binary.BigEndian.PutUint32(header[20:24], 100000)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	var out bytes.Buffer
	if _, err := prepareNativeChatImage(bytes.NewReader(header), &out); err == nil || !strings.Contains(err.Error(), "image_pixel_limit") || out.Len() != 0 {
		t.Fatal("hostile header reached decode/output", err)
	}
	if _, err := prepareNativeChatImage(bytes.NewReader(make([]byte, nativeChatImageMaxBytes+1)), &out); err != errNativeChatImageSize {
		t.Fatal("oversized source reached decoder", err)
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		photo := image.NewRGBA(image.Rect(0, 0, 30, 20))
		var source bytes.Buffer
		if err := jpeg.Encode(&source, photo, nil); err != nil {
			t.Fatal(err)
		}
		exif := make([]byte, 32)
		copy(exif, "Exif\x00\x00")
		tiff := exif[6:]
		copy(tiff, "II")
		if order == binary.BigEndian {
			copy(tiff, "MM")
		}
		order.PutUint16(tiff[2:], 42)
		order.PutUint32(tiff[4:], 8)
		order.PutUint16(tiff[8:], 1)
		order.PutUint16(tiff[10:], 0x112)
		order.PutUint16(tiff[12:], 3)
		order.PutUint32(tiff[14:], 1)
		order.PutUint16(tiff[18:], 6)
		input := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(exif) + 2)}, exif...)
		input = append(input, source.Bytes()[2:]...)
		out.Reset()
		prepared, err := prepareNativeChatImage(bytes.NewReader(input), &out)
		if err != nil || prepared.Width != 20 || prepared.Height != 30 {
			t.Fatal("phone orientation lost", prepared, err)
		}
	}
}
