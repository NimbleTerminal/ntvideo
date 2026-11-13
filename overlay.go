package ntvideo

import (
	"image/color"
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxOverlayDim   = 1000
	maxOverlayCells = 65536
)

// OverlayCell describes styled terminal text to composite onto a rendered video
// cell grid at zero-based column X and row Y. Multi-grapheme Symbol strings
// advance across consecutive columns starting at X. ANSI escapes, control
// characters, and bidirectional overrides are stripped before placement.
type OverlayCell struct {
	X, Y      int
	Symbol    string
	Fg, Bg    color.Color
	Bold      bool
	Italic    bool
	Underline bool
	Faint     bool
}

func sanitizeOverlaySpan(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\u061c' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(s))
	runes := []rune(s)
	if len(runes) > 1024 {
		s = string(runes[:1024])
	}
	return s
}

func overlayStyle(c OverlayCell) uv.Style {
	var st uv.Style
	st.Fg = c.Fg
	st.Bg = c.Bg
	if c.Bold {
		st.Attrs |= uv.AttrBold
	}
	if c.Italic {
		st.Attrs |= uv.AttrItalic
	}
	if c.Faint {
		st.Attrs |= uv.AttrFaint
	}
	if c.Underline {
		st.Underline = uv.UnderlineSingle
	}
	return st
}

// OverlayCells composites styled terminal cells onto a width×height rendered
// frame (glyph half-blocks or Kitty Unicode image placeholders). Each modified
// row is parsed into cells once so untouched Kitty placeholder cells keep their
// image IDs and row/column diacritics, and repeated overlays do not accumulate
// redundant ANSI SGR sequences.
func OverlayCells(view string, width, height int, cells []OverlayCell) string {
	if width <= 0 || height <= 0 || len(cells) == 0 {
		return view
	}
	width = min(width, maxOverlayDim)
	height = min(height, maxOverlayDim)
	if len(cells) > maxOverlayCells {
		cells = cells[:maxOverlayCells]
	}
	lines := strings.Split(view, "\n")
	if len(lines) < height {
		return view
	}

	var parsedRows map[int]uv.Line
	getRow := func(y int) uv.Line {
		if parsedRows == nil {
			parsedRows = make(map[int]uv.Line)
		}
		if row, ok := parsedRows[y]; ok {
			return row
		}
		row := make(uv.Line, width)
		for i := range row {
			row[i] = uv.EmptyCell
		}
		parsed := uv.NewStyledString(lines[y]).Lines(ansi.GraphemeWidth)
		if len(parsed) > 0 {
			copy(row, parsed[0])
		}
		parsedRows[y] = row
		return row
	}

	for _, item := range cells {
		if item.Y < 0 || item.Y >= height || item.Symbol == "" {
			continue
		}
		clean := sanitizeOverlaySpan(item.Symbol)
		if clean == "" {
			continue
		}
		st := overlayStyle(item)
		x := item.X
		rem := clean
		for len(rem) > 0 {
			cluster, w := ansi.FirstGraphemeCluster(rem, ansi.GraphemeWidth)
			if len(cluster) == 0 {
				break
			}
			rem = rem[len(cluster):]
			if w < 1 {
				continue
			}
			if x >= 0 && x+w <= width {
				row := getRow(item.Y)
				row.Set(x, &uv.Cell{
					Content: cluster,
					Width:   w,
					Style:   st,
				})
			}
			x += w
			if x >= width {
				break
			}
		}
	}

	for y, row := range parsedRows {
		lines[y] = row.Render()
	}
	return strings.Join(lines, "\n")
}

// SpliceRows composites the top splitRow rows from topView above the remaining
// rows from bottomView into a width×height terminal block, padding or
// truncating each line to width cells.
func SpliceRows(topView, bottomView string, width, height, splitRow int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	width = min(width, maxOverlayDim)
	height = min(height, maxOverlayDim)
	splitRow = max(0, min(height, splitRow))
	topLines := strings.Split(topView, "\n")
	bottomLines := strings.Split(bottomView, "\n")
	out := make([]string, height)
	for y := range out {
		src := bottomLines
		if y < splitRow {
			src = topLines
		}
		var line string
		if y < len(src) {
			line = ansi.Truncate(src[y], width, "")
		}
		if pad := width - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		out[y] = line
	}
	return strings.Join(out, "\n")
}
