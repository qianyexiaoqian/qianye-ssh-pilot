package agent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	imageMaxEdge   = 2000       // 长边超过这个值的图片会缩小：再大模型也看不到更多细节，只是更费流量
	imageKeepBytes = 1500 << 10 // 尺寸合适、又不超过这个大小的图片原样使用
	imageMaxPixels = 80_000_000 // 超过这个像素数的图片不处理（解码要占用的内存太大）
)

var passthroughMime = map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

// prepareImage 把图片整理成适合发给模型的样子：格式是 PNG / JPEG / GIF / WebP 之一，长边不超过 imageMaxEdge，
// 照片按 EXIF 里记录的方向摆正。本来就合适的图片原样返回。
func prepareImage(data []byte) (out []byte, mime string, w, h int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, errors.New("无法识别这张图片（支持 PNG、JPG、GIF、WebP、BMP）")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > imageMaxPixels {
		return nil, "", 0, 0, errors.New("图片的尺寸太大")
	}
	orient := 1
	if format == "jpeg" {
		orient = exifOrientation(data)
	}
	w, h = cfg.Width, cfg.Height
	if orient >= 5 {
		w, h = h, w
	}
	if m, ok := passthroughMime[format]; ok && orient == 1 && max(w, h) <= imageMaxEdge && len(data) <= imageKeepBytes {
		return data, m, w, h, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, errors.New("这张图片的内容损坏了，无法读取")
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if m := max(sw, sh); m > imageMaxEdge {
		sw, sh = max(sw*imageMaxEdge/m, 1), max(sh*imageMaxEdge/m, 1)
	}
	// 透明的地方垫上白色（JPEG 没有透明通道）
	dst := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, xdraw.Over, nil)
	dst = orientRGBA(dst, orient)
	w, h = dst.Bounds().Dx(), dst.Bounds().Dy()

	var buf bytes.Buffer
	// 截图这类大片纯色的图片用 PNG 又小又清楚；照片用 JPEG
	if format != "jpeg" {
		if png.Encode(&buf, dst) == nil && buf.Len() <= imageKeepBytes {
			return buf.Bytes(), "image/png", w, h, nil
		}
	}
	for _, q := range []int{88, 75, 60} {
		buf.Reset()
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, "", 0, 0, err
		}
		if buf.Len() <= 3<<20 {
			break
		}
	}
	return buf.Bytes(), "image/jpeg", w, h, nil
}

// orientRGBA 按 EXIF 方向（1–8）把图片摆正。
func orientRGBA(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2: // 左右翻转
				sx, sy = w-1-x, y
			case 3: // 旋转 180°
				sx, sy = w-1-x, h-1-y
			case 4: // 上下翻转
				sx, sy = x, h-1-y
			case 5: // 沿主对角线翻转
				sx, sy = y, x
			case 6: // 顺时针旋转 90°
				sx, sy = y, h-1-x
			case 7: // 沿副对角线翻转
				sx, sy = w-1-y, h-1-x
			case 8: // 逆时针旋转 90°
				sx, sy = w-1-y, x
			}
			copy(dst.Pix[dst.PixOffset(x, y):dst.PixOffset(x, y)+4], src.Pix[src.PixOffset(sx, sy):src.PixOffset(sx, sy)+4])
		}
	}
	return dst
}

// exifOrientation 读取 JPEG 里 EXIF 记录的拍摄方向（1–8）；没有记录时返回 1。
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF: // 填充字节
			i++
			continue
		case marker == 0xDA || marker == 0xD9: // 图像数据开始 / 文件结束
			return 1
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7): // 没有长度字段的段
			i += 2
			continue
		}
		size := int(b[i+2])<<8 | int(b[i+3])
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	off := int(order.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) == 0x0112 {
			if v := int(order.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
