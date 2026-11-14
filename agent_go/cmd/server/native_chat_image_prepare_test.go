package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	var gifFile, gifOut bytes.Buffer
	if err := gif.Encode(&gifFile, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Transparent, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	preparedGIF, err := prepareNativeChatImage(bytes.NewReader(gifFile.Bytes()), &gifOut)
	if err != nil || !preparedGIF.FirstFrame || preparedGIF.MIME != "image/png" {
		t.Fatal("GIF first-frame preparation not identified", err)
	}
	// Offset first frames are composited onto the GIF logical screen before scaling.
	offset := image.NewPaletted(image.Rect(2, 3, 6, 5), color.Palette{color.Transparent, color.White})
	for i := range offset.Pix {
		offset.Pix[i] = 1
	}
	gifFile.Reset()
	gifOut.Reset()
	if err := gif.EncodeAll(&gifFile, &gif.GIF{Image: []*image.Paletted{offset}, Delay: []int{0}, Config: image.Config{ColorModel: offset.Palette, Width: 10, Height: 10}}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareNativeChatImage(bytes.NewReader(gifFile.Bytes()), &gifOut); err != nil {
		t.Fatal(err)
	}
	composited, err := png.Decode(bytes.NewReader(gifOut.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, inside := composited.At(2, 3).RGBA()
	_, _, _, outside := composited.At(0, 0).RGBA()
	if composited.Bounds() != image.Rect(0, 0, 10, 10) || inside == 0 || outside != 0 {
		t.Fatal("GIF frame displaced or stretched")
	}
	nativeChatImageDecodeSlot <- struct{}{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, waitErr := prepareNativeChatImageContext(canceled, bytes.NewReader(nil), io.Discard)
	<-nativeChatImageDecodeSlot
	if waitErr != context.Canceled {
		t.Fatal("decode semaphore ignored cancellation", waitErr)
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

// The owner requested a measured 10 MB delivery, not an unqualified memory
// claim. Generate the source before measuring; preparation reads a file and
// releases decoded/encoded buffers when it returns. Logs contain sizes only.
func TestNativeChatImageTenMBPeakMemory(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source-*.png")
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewNRGBA(image.Rect(0, 0, 2100, 1600))
	seed := uint32(0x5ea127c3)
	for i := 0; i < len(picture.Pix); i += 4 {
		for c := range 3 {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			picture.Pix[i+c] = byte(seed)
		}
		picture.Pix[i+3] = 255
	}
	if err = png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	stat, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() > nativeChatImageMaxBytes {
		t.Fatal("measurement source exceeded limit", stat.Size())
	}
	if err = file.Truncate(nativeChatImageMaxBytes); err != nil {
		t.Fatal(err)
	}
	picture = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapAlloc)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				for old := peak.Load(); stats.HeapAlloc > old; old = peak.Load() {
					if peak.CompareAndSwap(old, stats.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	prepared, err := prepareNativeChatImage(file, io.Discard)
	close(done)
	<-stopped
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("VERIFY-METRIC: [CHAT_IMAGE_MEMORY] source_bytes=%d prepared_bytes=%d width=%d height=%d baseline_heap=%d peak_heap=%d peak_delta=%d after_gc_heap=%d", nativeChatImageMaxBytes, prepared.Bytes, prepared.Width, prepared.Height, before.HeapAlloc, peak.Load(), peak.Load()-before.HeapAlloc, after.HeapAlloc)
}

// Worst admitted PNG: 16M 16-bit RGBA pixels. The compressed file fits the
// upload bound; decoded allocations, not compressed bytes, are the risk.
func TestNativeChatImageWorstCasePeakMemory(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "worst-*.png")
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewNRGBA64(image.Rect(0, 0, 4000, 4000))
	for i := 0; i < len(picture.Pix); i += 8 {
		copy(picture.Pix[i:i+8], []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xff, 0xfe})
	}
	if err = png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	picture = nil
	stat, err := file.Stat()
	if err != nil || stat.Size() > nativeChatImageMaxBytes {
		t.Fatal("worst fixture exceeds upload bound", err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapAlloc)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				for old := peak.Load(); stats.HeapAlloc > old; old = peak.Load() {
					if peak.CompareAndSwap(old, stats.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	prepared, err := prepareNativeChatImage(file, io.Discard)
	close(done)
	<-stopped
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("VERIFY-METRIC: [CHAT_IMAGE_MEMORY] case=16m_rgba64 source_bytes=%d prepared_bytes=%d baseline_heap=%d peak_heap=%d peak_delta=%d after_gc_heap=%d", stat.Size(), prepared.Bytes, before.HeapAlloc, peak.Load(), peak.Load()-before.HeapAlloc, after.HeapAlloc)
}
