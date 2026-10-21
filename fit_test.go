package ntvideo

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"nimbleterminal.dev/ntpicture"
)

func TestFitFillsEncodedWidgetRegion(t *testing.T) {
	m := New(WithWidth(10), WithHeight(10), WithKittyMedium(ntpicture.KittyMediumDirect))
	defer m.Close()
	m.pic.SetTerminal(true, 8, 16)
	src := image.NewRGBA(image.Rect(0, 0, 160, 40))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	drivePicture(t, m, m.pic.SetImage(src))
	for _, fit := range []FitMode{FitCover, FitFill, FitContain} {
		cmd := m.SetFit(fit)
		if cmd == nil {
			t.Fatal("missing redraw")
		}
		output := drivePicture(t, m, cmd)
		var payload []byte
		for _, part := range regexp.MustCompile(`\x1b_G[^;]+;([^\x1b]*)\x1b\\`).FindAllStringSubmatch(output, -1) {
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

// Execute Picture's asynchronous command/message protocol, including Tea's
// private sequence message. Producer ticks are deliberately left to the host.
func drivePicture(t *testing.T, m *Model, cmd tea.Cmd) string {
	t.Helper()
	var output strings.Builder
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if msg == nil {
			return
		}
		v := reflect.ValueOf(msg)
		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				if sub, ok := v.Index(i).Interface().(tea.Cmd); ok {
					run(sub)
				}
			}
			return
		}
		if raw, ok := msg.(tea.RawMsg); ok {
			output.WriteString(fmt.Sprint(raw.Msg))
			return
		}
		switch msg.(type) {
		case wakeMsg, EventMsg:
			return
		}
		run(m.Update(msg))
	}
	run(cmd)
	return output.String()
}
