package view

import (
	"fmt"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// CannotRead starts the note a view prints when a Details value does not have
// the type its producer stores. It never appears on real results: the fixture
// matrix test asserts that, so a producer that changes a type breaks a test
// instead of silently emptying a view.
const CannotRead = "cannot read "

// decoder collects the type mismatches found while a view reads one result.
type decoder struct{ errs []string }

// get returns v as a T. An absent value (nil) is the zero T; a value of
// another type is the zero T and an error for flush.
func get[T any](d *decoder, what string, v any) T {
	var zero T
	if v == nil {
		return zero
	}
	t, ok := v.(T)
	if !ok {
		d.errs = append(d.errs, fmt.Sprintf("%s%s: want %T, got %T", CannotRead, what, zero, v))
		return zero
	}
	return t
}

// detail reads r.Details[key] as a T.
func detail[T any](d *decoder, r model.Result, key string) T { return get[T](d, key, r.Details[key]) }

// field reads m[key] as a T, for a map nested in Details under parent.
func field[T any](d *decoder, parent string, m map[string]any, key string) T {
	return get[T](d, parent+"."+key, m[key])
}

// flush prints the collected mismatches as error notes.
func (d *decoder) flush(p *render.Printer, indent int) {
	for _, e := range d.errs {
		p.Note(indent, model.StatusError, e)
	}
	d.errs = nil
}
