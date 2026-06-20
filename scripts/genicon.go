//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	root := "."
	if _, err := os.Stat(filepath.Join(root, "desktop")); err != nil {
		for _, d := range []string{"..", "../.."} {
			if _, err := os.Stat(filepath.Join(d, "desktop")); err == nil {
				root = d
				break
			}
		}
	}

	pngPath := filepath.Join(root, "desktop", "build", "appicon.png")
	icoDir := filepath.Join(root, "desktop", "build", "windows")
	icoPath := filepath.Join(icoDir, "icon.ico")

	os.MkdirAll(filepath.Dir(pngPath), 0755)
	os.MkdirAll(icoDir, 0755)

	img := drawLogo(256)

	f, err := os.Create(pngPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL create png: %v\n", err)
		os.Exit(1)
	}
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL encode png: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL close png: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK png %s\n", pngPath)

	tmp := drawLogo(48)
	bmp := rgbaToBGRA(tmp)

	bmpHdr := new(bytes.Buffer)
	binary.Write(bmpHdr, binary.LittleEndian, uint32(40))
	binary.Write(bmpHdr, binary.LittleEndian, int32(48))
	binary.Write(bmpHdr, binary.LittleEndian, int32(96))
	binary.Write(bmpHdr, binary.LittleEndian, uint16(1))
	binary.Write(bmpHdr, binary.LittleEndian, uint16(32))
	binary.Write(bmpHdr, binary.LittleEndian, uint32(0))
	binary.Write(bmpHdr, binary.LittleEndian, uint32(len(bmp)))
	binary.Write(bmpHdr, binary.LittleEndian, int32(0))
	binary.Write(bmpHdr, binary.LittleEndian, int32(0))
	binary.Write(bmpHdr, binary.LittleEndian, uint32(0))
	binary.Write(bmpHdr, binary.LittleEndian, uint32(0))

	and := andMaskBytes(48)
	imgData := append(bmpHdr.Bytes(), bmp...)
	imgData = append(imgData, and...)

	ico := new(bytes.Buffer)
	binary.Write(ico, binary.LittleEndian, uint16(0))
	binary.Write(ico, binary.LittleEndian, uint16(1))
	binary.Write(ico, binary.LittleEndian, uint16(1))
	ico.WriteByte(48)
	ico.WriteByte(48)
	ico.WriteByte(0)
	ico.WriteByte(0)
	binary.Write(ico, binary.LittleEndian, uint16(1))
	binary.Write(ico, binary.LittleEndian, uint16(32))
	binary.Write(ico, binary.LittleEndian, uint32(len(imgData)))
	binary.Write(ico, binary.LittleEndian, uint32(22))
	ico.Write(imgData)

	if err := os.WriteFile(icoPath, ico.Bytes(), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL write ico: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK ico %s (%d bytes)\n", icoPath, len(ico.Bytes()))
}

func drawLogo(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	v := func(n int) int { return n * size / 256 }

	cCyan := color.RGBA{0x5e, 0xea, 0xd4, 255}
	cBlue := color.RGBA{0x7d, 0xd3, 0xfc, 255}
	cViolet := color.RGBA{0xc4, 0xb5, 0xfd, 255}
	cZero := color.RGBA{0, 0, 0, 0}

	// O: thick rectangular ring
	oL, oR := v(30), v(120)
	oT, oB := v(50), v(210)
	oK := v(22)
	for y := oT; y < oB; y++ {
		for x := oL; x < oR; x++ {
			img.Set(x, y, cCyan)
		}
	}
	for y := oT + oK; y < oB-oK; y++ {
		for x := oL + oK; x < oR-oK; x++ {
			img.Set(x, y, cZero)
		}
	}

	// K: vertical bar
	kL, kR := v(142), v(172)
	for y := oT; y < oB; y++ {
		for x := kL; x < kR; x++ {
			img.Set(x, y, cBlue)
		}
	}

	// K upper arm — tilts UP-right (wider at top, narrows at stem)
	aw := v(18)
	ah := v(55)
	for row := 0; row < ah; row++ {
		shift := (ah - 1 - row) / 2
		x1 := kR + shift
		x2 := x1 + aw
		y := oT + row
		for x := x1; x < x2; x++ {
			if x < size && y < size {
				img.Set(x, y, cViolet)
			}
		}
	}

	// K lower arm — tilts DOWN-right (wider at bottom, narrows at stem)
	for row := 0; row < ah; row++ {
		shift := row / 2
		x1 := kR + shift
		x2 := x1 + aw
		y := oB - ah + row
		for x := x1; x < x2; x++ {
			if x < size && y < size {
				img.Set(x, y, cViolet)
			}
		}
	}

	return img
}

func rgbaToBGRA(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var buf []byte
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			buf = append(buf, byte(b>>8))
			buf = append(buf, byte(g>>8))
			buf = append(buf, byte(r>>8))
			buf = append(buf, byte(a>>8))
		}
		p := (w * 4) % 4
		if p > 0 {
			for i := 0; i < 4-p; i++ {
				buf = append(buf, 0)
			}
		}
	}
	return buf
}

func andMaskBytes(n int) []byte {
	row := (n + 31) / 32 * 4
	return make([]byte, row*n)
}
