//go:build windows && amd64

// Package main implements the promise-slice conformance runner.
//
// It re-implements the three Promise/PromiseResolver checks of the pinned
// Rust oracle host slice (rust-oracle/src/checks/host/promises.rs, executed
// in registry order by src/checks/host/mod.rs) on top of the Go binding and
// compares the normalized JSON-lines against the checked-in host fixture:
//
//	../../rust-oracle/tests/fixtures/conformance-host-v8_152.2.0_x86_64-pc-windows-msvc.jsonl
//
// The host fixture pins all 18 host-slice checks; this runner extracts and
// reproduces the three promise lines byte-for-byte. The other 15 checks
// belong to sibling feature slices and are intentionally out of scope here.
//
// Normalization rules (must match rust-oracle/src/json.rs; identical to the
// base conformance runner in ../json.go):
//   - JSON objects keep insertion order, no whitespace.
//   - Strings: minimal escaping (\", \\, \n, \r, \t, \b, \f, \u00XX for
//     control characters below 0x20); other code points as raw UTF-8.
//   - Integers: plain decimal.
package main

import (
	"fmt"
	"strings"
)

// jsonValue is a canonical JSON value with insertion-ordered objects.
type jsonValue interface {
	writeTo(b *strings.Builder)
}

type jsonNull struct{}

type jsonBool bool

type jsonInt int64

type jsonStr string

type jsonArr []jsonValue

type jsonPair struct {
	key string
	val jsonValue
}

type jsonObj []jsonPair

func obj(pairs ...jsonPair) jsonObj     { return jsonObj(pairs) }
func arr(items ...jsonValue) jsonArr    { return jsonArr(items) }
func s(v string) jsonValue              { return jsonStr(v) }
func i(v int64) jsonValue               { return jsonInt(v) }
func b(v bool) jsonValue                { return jsonBool(v) }
func kv(k string, v jsonValue) jsonPair { return jsonPair{k, v} }

func (jsonNull) writeTo(b *strings.Builder) { b.WriteString("null") }

func (v jsonBool) writeTo(b *strings.Builder) {
	if v {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
}

func (v jsonInt) writeTo(b *strings.Builder) { b.WriteString(fmt.Sprintf("%d", int64(v))) }

func (v jsonStr) writeTo(b *strings.Builder) { writeEscaped(string(v), b) }

func (a jsonArr) writeTo(b *strings.Builder) {
	b.WriteByte('[')
	for idx, item := range a {
		if idx > 0 {
			b.WriteByte(',')
		}
		item.writeTo(b)
	}
	b.WriteByte(']')
}

func (o jsonObj) writeTo(b *strings.Builder) {
	b.WriteByte('{')
	for idx, p := range o {
		if idx > 0 {
			b.WriteByte(',')
		}
		writeEscaped(p.key, b)
		b.WriteByte(':')
		p.val.writeTo(b)
	}
	b.WriteByte('}')
}

func writeEscaped(value string, out *strings.Builder) {
	out.WriteByte('"')
	for _, ch := range value {
		switch ch {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		case 0x08:
			out.WriteString("\\b")
		case 0x0C:
			out.WriteString("\\f")
		default:
			if ch < 0x20 {
				out.WriteString(fmt.Sprintf("\\u%04x", ch))
			} else {
				out.WriteRune(ch)
			}
		}
	}
	out.WriteByte('"')
}

func jsonString(v jsonValue) string {
	var sb strings.Builder
	v.writeTo(&sb)
	return sb.String()
}
