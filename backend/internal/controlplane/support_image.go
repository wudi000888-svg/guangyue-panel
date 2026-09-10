package controlplane

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"syscall"
)

const maxTicketImage = 2 << 20

// Test injection exercises the same normalizer without starting a service CLI.
func (a *App) processTicketImage(ctx context.Context, raw []byte, format string) ([]byte, error) {
	if a.ticketProcessor != nil {
		return a.ticketProcessor(ctx, raw, format)
	}
	path, e := os.Executable()
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, path, "-normalize-ticket-image", format)
	cmd.Stdin = bytes.NewReader(raw)
	var out boundedImageBuffer
	cmd.Stdout = &out
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}

type boundedImageBuffer struct{ bytes.Buffer }

func (b *boundedImageBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxTicketImage {
		return 0, errors.New("image output too large")
	}
	return b.Buffer.Write(p)
}
func ticketImageWorker(format string) int {
	debug.SetMemoryLimit(64 << 20)
	if runtime.GOOS == "linux" {
		if syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: 128 << 20, Max: 128 << 20}) != nil {
			return 2
		}
	}
	if syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: 12, Max: 15}) != nil {
		return 2
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, maxTicketImage+1))
	if e != nil {
		return 2
	}
	out, e := normalizeTicketImage(raw, format)
	if e != nil {
		return 2
	}
	if _, e = os.Stdout.Write(out); e != nil {
		return 2
	}
	return 0
}
func normalizeTicketImage(raw []byte, format string) ([]byte, error) {
	bad := errors.New("invalid ticket image")
	if len(raw) == 0 || len(raw) > maxTicketImage || format != "png" && format != "jpeg" {
		return nil, bad
	}
	cfg, actual, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || actual != format || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 8000000 {
		return nil, bad
	}
	if format == "png" {
		for p := 8; p+12 <= len(raw); {
			n := uint64(binary.BigEndian.Uint32(raw[p : p+4]))
			if n > uint64(len(raw)-p-12) {
				return nil, bad
			}
			tag := string(raw[p+4 : p+8])
			if tag == "acTL" || tag == "fcTL" || tag == "fdAT" {
				return nil, bad
			}
			p += 12 + int(n)
		}
	}
	img, actual, e := image.Decode(bytes.NewReader(raw))
	if e != nil || actual != format {
		return nil, bad
	}
	if format == "jpeg" {
		img = orientTicketImage(img, jpegOrientation(raw))
	}
	var out boundedImageBuffer
	if format == "png" {
		e = png.Encode(&out, img)
	} else {
		e = jpeg.Encode(&out, img, &jpeg.Options{Quality: 88})
	}
	if e != nil || out.Len() > maxTicketImage {
		return nil, bad
	}
	return out.Bytes(), nil
}
func jpegOrientation(raw []byte) int {
	for p := 2; p+4 < len(raw); {
		if raw[p] != 0xff {
			return 1
		}
		tag := raw[p+1]
		if tag == 0xda || tag == 0xd9 {
			return 1
		}
		n := int(binary.BigEndian.Uint16(raw[p+2 : p+4]))
		if n < 2 || p+2+n > len(raw) {
			return 1
		}
		v := raw[p+4 : p+2+n]
		p += n + 2
		if tag != 0xe1 || len(v) < 14 || !bytes.Equal(v[:6], []byte("Exif\x00\x00")) {
			continue
		}
		v = v[6:]
		var order binary.ByteOrder = binary.LittleEndian
		if bytes.Equal(v[:2], []byte("MM")) {
			order = binary.BigEndian
		} else if !bytes.Equal(v[:2], []byte("II")) {
			return 1
		}
		offset := uint64(order.Uint32(v[4:8]))
		if offset+2 > uint64(len(v)) {
			return 1
		}
		count := int(order.Uint16(v[offset : offset+2]))
		for j := 0; j < count; j++ {
			i := int(offset) + 2 + j*12
			if i+12 > len(v) {
				return 1
			}
			if order.Uint16(v[i:i+2]) == 0x112 && order.Uint16(v[i+2:i+4]) == 3 && order.Uint32(v[i+4:i+8]) == 1 {
				return int(order.Uint16(v[i+8 : i+10]))
			}
		}
	}
	return 1
}
func orientTicketImage(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch o {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(x+src.Bounds().Min.X, y+src.Bounds().Min.Y))
		}
	}
	return dst
}
func ticketDiskAvailable(path string) bool {
	var v syscall.Statfs_t
	if syscall.Statfs(path, &v) != nil {
		return false
	}
	free := uint64(v.Bavail) * uint64(v.Bsize)
	total := uint64(v.Blocks) * uint64(v.Bsize)
	return free >= 1<<30 && free >= total/10
}
