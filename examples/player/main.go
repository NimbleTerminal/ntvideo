package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntvideo"
)

type model struct {
	v                                          *ntvideo.Model
	width, height                              int
	expanded                                   bool
	source                                     string
	volumeDragging, volumePending, mutePending bool
	desiredVolume                              float64
	desiredMuted                               bool
	captionMenu                                bool
	captionCursor                              int
	captionChoices                             []ntvideo.Track
	lastCaption, desiredCaption                string
	captionPending                             bool
	cueEvents                                  bool
	cueCount                                   int
}

func (m *model) Init() tea.Cmd { return m.v.Init() }
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var layout tea.Cmd
	switch msg := msg.(type) {
	case ntvideo.CaptionCueMsg:
		if msg.VideoID == m.v.ID() {
			m.cueCount++
			reportCue(msg)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		layout = m.layout()
	case tea.KeyPressMsg:
		if m.captionMenu && msg.String() != "q" && msg.String() != "ctrl+c" {
			layout = m.captionKey(msg.String())
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Sequence(m.v.Shutdown(), tea.Quit)
		case "space":
			layout = m.activate("play")
		case "enter":
			layout = m.activate("size")
		case "r":
			layout = m.activate("render")
		case "c":
			m.toggleCaptions()
		case "t":
			m.v.SetNativeCaptionOverlay(!m.v.NativeCaptionOverlay())
		case "C":
			layout = m.openCaptions()
		case "f":
			layout = m.activate("fit")
		case "left":
			layout = m.activate("back")
		case "right":
			layout = m.activate("forward")
		case "m":
			layout = m.activate("mute")
		case "+", "=":
			layout = m.activate("louder")
		case "-":
			layout = m.activate("quieter")
		}
	}
	mouse := m.mouse(msg)
	update := m.v.Update(msg)
	m.syncControls()
	return m, tea.Batch(layout, mouse, update)
}
func main() {
	render := flag.String("render", "auto", "native rendering: auto, glyph, or raster (Kitty graphics)")
	captionRender := flag.String("caption-render", "backend", "native caption rendering: backend or text")
	cueEvents := flag.Bool("cue-events", false, "show caption cue event count (browser also logs events to the console)")
	var captions []ntvideo.Caption
	flag.Func("captions", "subtitle file or browser WebVTT URL (repeat for multiple tracks)", func(source string) error {
		captions = append(captions, ntvideo.Caption{Source: source, Label: fmt.Sprintf("Captions %d", len(captions)+1)})
		return nil
	})
	flag.Parse()
	if *captionRender != "backend" && *captionRender != "text" {
		fmt.Fprintln(os.Stderr, "invalid -caption-render: use backend or text")
		os.Exit(2)
	}
	mode := ntvideo.RenderAuto
	switch *render {
	case "auto":
	case "glyph":
		mode = ntvideo.RenderGlyph
	case "raster":
		mode = ntvideo.RenderRaster
	default:
		fmt.Fprintln(os.Stderr, "invalid -render: use auto, glyph, or raster")
		os.Exit(2)
	}
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: player [-render auto|glyph|raster] [-caption-render backend|text] [-cue-events] [-captions subtitle] [video]")
		os.Exit(2)
	}
	source := "demo.mp4"
	if flag.NArg() > 0 {
		source = flag.Arg(0)
	}
	v := ntvideo.New(ntvideo.WithCaptionEvents(*cueEvents), ntvideo.WithNativeControls(false), ntvideo.WithNativeCaptionOverlay(*captionRender == "text"), ntvideo.WithRenderMode(mode), ntvideo.WithMedia(ntvideo.Source{URL: source, Captions: captions}), ntvideo.WithScrubber(true), ntvideo.WithWidth(60), ntvideo.WithHeight(20), ntvideo.WithDescription("Video demonstration"))
	defer v.Close()
	v.Focus()
	m := &model{v: v, expanded: true, source: source, cueEvents: *cueEvents}
	if err := run(m); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
