//go:build libmpv && cgo && !js

package ntvideo

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"encoding/base64"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"github.com/charmbracelet/colorprofile"
	"image/png"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPausedInitialFrame(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	b, err := newBackend(config{muted: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Load(Source{URL: source}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	frames, nonblack := 0, 0
	for time.Now().Before(deadline) {
		s, err := b.Poll()
		if err != nil {
			t.Fatal(err)
		}
		img, err := b.Frame(480, 320)
		if err != nil {
			t.Fatal(err)
		}
		if img != nil {
			frames++
			var total uint64
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					r, g, b, _ := img.At(x, y).RGBA()
					total += uint64(r + g + b)
				}
			}
			t.Logf("frame %d brightness=%d state=%s time=%s", frames, total, s.State, s.CurrentTime)
			if total > 0 {
				nonblack++
			}
		}
		time.Sleep(time.Second / 30)
	}
	if nonblack == 0 {
		t.Fatal("no visible initial frame while paused")
	}
}

type capturePlayer struct{ v *Model }
type captureQuit struct{}

func (m *capturePlayer) Init() tea.Cmd {
	return tea.Batch(m.v.Init(), tea.Tick(time.Second, func(time.Time) tea.Msg { return captureQuit{} }))
}
func (m *capturePlayer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(captureQuit); ok {
		return m, tea.Quit
	}
	return m, m.v.Update(msg)
}
func (m *capturePlayer) View() tea.View { v := tea.NewView(m.v.View()); v.AltScreen = true; return v }
func TestKittyPresentation(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	old := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	defer picture.ForceKittyCapability(old)
	// The captured output is a pipe, not a terminal, so the shared-memory probe is
	// never answered; request direct PNG frames explicitly so the payload check
	// below does not depend on that fallback.
	m := &capturePlayer{v: New(WithSource(source), WithMuted(true), WithKittyMedium(picture.KittyMediumDirect))}
	defer m.v.Close()
	var output bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&output), tea.WithWindowSize(80, 24), tea.WithColorProfile(colorprofile.TrueColor), tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	data := output.String()
	t.Logf("state=%s bytes=%d kitty transmissions=%d", m.v.Status().State, len(data), strings.Count(data, "\x1b_G"))
	if !strings.Contains(data, "\U0010EEEE") {
		t.Fatal("Kitty placeholder missing")
	}
	if !strings.Contains(data, "i="+strconv.Itoa(0x560000+int(m.v.id))) {
		t.Fatal("Kitty image missing")
	}
	// Verify that the transmitted PNG actually contains a visible image, rather
	// than merely checking that a Kitty command reached the output stream.
	var payload []byte
	visible := false
	for _, part := range regexp.MustCompile("\\x1b_G([^;]+);([^\\x1b]*)\\x1b\\\\").FindAllSubmatch(output.Bytes(), -1) {
		if bytes.HasPrefix(part[1], []byte("a=q")) {
			continue // Capability query (e.g. shared-memory probe); its payload is not image data.
		}
		payload = append(payload, part[2]...)
		if bytes.Contains(part[1], []byte("m=1")) {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(string(payload))
		payload = nil
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(decoded))
		if err != nil {
			t.Fatal(err)
		}
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y && !visible; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r+g+b > 0 {
					visible = true
					break
				}
			}
		}
	}
	if !visible {
		t.Fatal("no visible image in Kitty output")
	}
}
