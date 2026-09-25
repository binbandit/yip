// Package schemagen generates versioned JSON Schemas and TypeScript types
// from yip's Go protocol types, so the web client's types come from the same
// source as the server.
package schemagen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})
var rawType = reflect.TypeOf(json.RawMessage{})

// Generator collects named struct types reachable from the roots.
type Generator struct {
	named map[string]reflect.Type
	order []string
}

func New(roots ...any) *Generator {
	g := &Generator{named: map[string]reflect.Type{}}
	for _, r := range roots {
		g.collect(reflect.TypeOf(r))
	}
	sort.Strings(g.order)
	return g
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func (g *Generator) collect(t reflect.Type) {
	t = deref(t)
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		if t == rawType {
			return
		}
		g.collect(t.Elem())
		return
	case reflect.Struct:
		if t == timeType || t.Name() == "" {
			for i := 0; i < t.NumField(); i++ {
				g.collect(t.Field(i).Type)
			}
			return
		}
		if _, ok := g.named[t.Name()]; ok {
			return
		}
		g.named[t.Name()] = t
		g.order = append(g.order, t.Name())
		for i := 0; i < t.NumField(); i++ {
			g.collect(t.Field(i).Type)
		}
	}
}

type field struct {
	name     string
	t        reflect.Type
	optional bool
}

func fields(t reflect.Type) []field {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			out = append(out, fields(deref(f.Type))...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		optional := strings.Contains(opts, "omitempty") || f.Type.Kind() == reflect.Pointer
		out = append(out, field{name: name, t: f.Type, optional: optional})
	}
	return out
}

func (g *Generator) tsType(t reflect.Type) string {
	nullable := t.Kind() == reflect.Pointer
	t = deref(t)
	var s string
	switch {
	case t == timeType:
		s = "string"
	case t == rawType:
		s = "unknown"
	case t.Kind() == reflect.Interface:
		s = "unknown"
	case t.Kind() == reflect.String:
		if t.Name() != "" && t.Name() != "string" {
			s = t.Name()
		} else {
			s = "string"
		}
	case t.Kind() == reflect.Bool:
		s = "boolean"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Float64:
		s = "number"
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		inner := g.tsType(t.Elem())
		if strings.ContainsAny(inner, " |") {
			inner = "(" + inner + ")"
		}
		s = inner + "[]"
	case t.Kind() == reflect.Map:
		s = "Record<string, " + g.tsType(t.Elem()) + ">"
	case t.Kind() == reflect.Struct:
		s = t.Name()
	default:
		s = "unknown"
	}
	if nullable {
		s += " | null"
	}
	return s
}

// TypeScript renders all collected types. stringEnums maps named string
// types to their allowed values.
func (g *Generator) TypeScript(header string, stringEnums map[string][]string) string {
	var b strings.Builder
	b.WriteString(header)
	var enumNames []string
	for n := range stringEnums {
		enumNames = append(enumNames, n)
	}
	sort.Strings(enumNames)
	for _, n := range enumNames {
		var vals []string
		for _, v := range stringEnums[n] {
			vals = append(vals, fmt.Sprintf("%q", v))
		}
		fmt.Fprintf(&b, "export type %s = %s;\n\n", n, strings.Join(vals, " | "))
	}
	for _, name := range g.order {
		t := g.named[name]
		fmt.Fprintf(&b, "export interface %s {\n", name)
		for _, f := range fields(t) {
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", f.name, opt, g.tsType(f.t))
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func (g *Generator) schemaFor(t reflect.Type, stringEnums map[string][]string) map[string]any {
	nullable := t.Kind() == reflect.Pointer
	t = deref(t)
	var s map[string]any
	switch {
	case t == timeType:
		s = map[string]any{"type": "string", "format": "date-time"}
	case t == rawType || t.Kind() == reflect.Interface:
		s = map[string]any{}
	case t.Kind() == reflect.String:
		s = map[string]any{"type": "string"}
		if vals, ok := stringEnums[t.Name()]; ok {
			s["enum"] = vals
		}
	case t.Kind() == reflect.Bool:
		s = map[string]any{"type": "boolean"}
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		s = map[string]any{"type": "integer"}
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		s = map[string]any{"type": "number"}
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		s = map[string]any{"type": "array", "items": g.schemaFor(t.Elem(), stringEnums)}
	case t.Kind() == reflect.Map:
		s = map[string]any{"type": "object", "additionalProperties": g.schemaFor(t.Elem(), stringEnums)}
	case t.Kind() == reflect.Struct:
		s = map[string]any{"$ref": "#/$defs/" + t.Name()}
	default:
		s = map[string]any{}
	}
	if nullable {
		return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}
	return s
}

// JSONSchema renders a single schema document with every type under $defs.
func (g *Generator) JSONSchema(id, title string, stringEnums map[string][]string) ([]byte, error) {
	defs := map[string]any{}
	for _, name := range g.order {
		t := g.named[name]
		props := map[string]any{}
		var required []string
		for _, f := range fields(t) {
			props[f.name] = g.schemaFor(f.t, stringEnums)
			if !f.optional {
				required = append(required, f.name)
			}
		}
		def := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			def["required"] = required
		}
		defs[name] = def
	}
	doc := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": id, "title": title, "$defs": defs}
	return json.MarshalIndent(doc, "", "  ")
}
