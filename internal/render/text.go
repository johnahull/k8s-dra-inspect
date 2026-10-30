package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Style selects colour and the wrap width of a Printer. Width 0 never wraps.
type Style struct {
	Color bool
	Width int
}

// Span is text the Printer has already neutralised and styled. Line passes
// Spans through untouched; every other argument is cleaned first, so only
// Printer methods can put escape sequences into the output.
type Span string

// Printer is the only way text views write output. It neutralises every
// string with Clean before adding glyphs or colour.
type Printer struct {
	w   io.Writer
	st  Style
	err error
}

// NewPrinter writes to w in the given style.
func NewPrinter(w io.Writer, st Style) *Printer { return &Printer{w: w, st: st} }

// Err returns the first write error.
func (p *Printer) Err() error { return p.err }

func (p *Printer) emit(s string) {
	if p.err == nil {
		_, p.err = io.WriteString(p.w, strings.TrimRight(s, " ")+"\n")
	}
}

func (p *Printer) sgr(code, s string) Span {
	s = Clean(s)
	if !p.st.Color {
		return Span(s)
	}
	return Span("\x1b[" + code + "m" + s + "\x1b[0m")
}

// Bold and Dim style s.
func (p *Printer) Bold(s string) Span { return p.sgr("1", s) }
func (p *Printer) Dim(s string) Span  { return p.sgr("2", s) }

// Banner prints "═══ title ═══" in bold cyan.
func (p *Printer) Banner(title string) {
	p.emit(string(p.sgr("1;36", "═══ "+title+" ═══")))
}

// Marker is the status glyph: ✓ pass, ! warn, ✗ fail, ? unavailable, E error.
func (p *Printer) Marker(s model.Status) Span {
	switch s {
	case model.StatusPass:
		return p.sgr("32", "✓")
	case model.StatusWarn:
		return p.sgr("33", "!")
	case model.StatusFail:
		return p.sgr("31", "✗")
	case model.StatusError:
		return p.sgr("31", "E")
	}
	return p.sgr("2", "?")
}

// Branch is the tree connector for an entry; Trunk is the continuation
// prefix for the lines under it.
func (p *Printer) Branch(last bool) Span {
	if last {
		return "└─ "
	}
	return "├─ "
}

func (p *Printer) Trunk(last bool) Span {
	if last {
		return "   "
	}
	return "│  "
}

// Blank prints an empty line.
func (p *Printer) Blank() { p.emit("") }

// Line prints one logical line, never wrapped. format must be a constant;
// string-like arguments are cleaned, Spans pass through.
func (p *Printer) Line(indent int, format string, args ...any) {
	safe := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case Span, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, nil:
			safe[i] = a
		case string:
			safe[i] = Clean(v)
		default: // errors, Stringers, slices, structs: format first, then clean
			safe[i] = Clean(fmt.Sprint(v))
		}
	}
	p.emit(strings.Repeat(" ", indent) + fmt.Sprintf(Clean(format), safe...))
}

// Prose prints a sentence, wrapped at Style.Width (when > 0) with the
// continuation lines indented by indent+hanging. A word wider than the line
// overflows rather than being split.
func (p *Printer) Prose(indent, hanging int, text string) {
	words := strings.Fields(Clean(text))
	pre := strings.Repeat(" ", indent)
	line, w := pre, indent
	has := false
	for _, word := range words {
		ww := cond.StringWidth(word)
		if has && p.st.Width > 0 && w+1+ww > p.st.Width {
			p.emit(line)
			line, w, has = strings.Repeat(" ", indent+hanging), indent+hanging, false
		}
		if has {
			line, w = line+" ", w+1
		}
		line, w, has = line+word, w+ww, true
	}
	p.emit(line)
}
