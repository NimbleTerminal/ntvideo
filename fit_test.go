package ntvideo

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"regexp"
	"testing"

	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

func TestFitFillsEncodedWidgetRegion(t *testing.T) {
	old := legacy.KittySupported()
	legacy.ForceKittyCapability(ntpicture.KittyCapabilitySupported)
	defer legacy.ForceKittyCapability(old)
	m := New(WithWidth(10), WithHeight(10))
	src := image.NewRGBA(image.Rect(0, 0, 160, 40))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	m.pic.SetImage(src)
	m.pic.Toggle()
	for _, fit := range []FitMode{FitCover, FitFill, FitContain} {
		cmd := m.SetFit(fit)
		if cmd == nil {
			t.Fatal("missing redraw")
		}
		frame := cmd().(legacy.KittyFrameMsg)
		var payload []byte
		for _, part := range regexp.MustCompile(`\x1b_G[^;]+;([^\x1b]*)\x1b\\`).FindAllStringSubmatch(frame.APC, -1) {
			payload = append(payload, part[1]...)
		}
		data, err := base64.StdEncoding.DecodeString(string(payload))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 80 || img.Bounds().Dy() != 160 {
			t.Fatal(img.Bounds())
		}
		r, _, _, a := img.At(0, 0).RGBA()
		if fit == FitContain {
			if a != 0 {
				t.Fatal("contain lost letterboxing")
			}
		} else if r != 65535 || a != 65535 {
			t.Fatalf("%s left blank edges: red=%d alpha=%d", fit, r, a)
		}
	}
}
