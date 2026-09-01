//go:build windows && amd64

// Canonical JSON encoder for the conformance-serializer-delegates runner.
//
// Normalization rules (must match rust-oracle/src/json.rs and the other Go
// conformance runners):
//   - JSON objects keep insertion order, no whitespace.
//   - Strings: minimal escaping (\", \\, \n, \r, \t, \b, \f, \u00XX for
//     control characters below 0x20); other code points as raw UTF-8.
//   - Integers: plain decimal.
//   - Floats: shortest round-trip plain decimal, never exponent, no
//     trailing ".0" (Rust Display for f64 semantics); only magnitudes in
//     [1e-4, 1e15) are used by the checks.
package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// jsonValue is a canonical JSON value with insertion-ordered objects.
type jsonValue interface {
	writeTo(b *strings.Builder)
}

type jsonNull struct{}

type jsonBool bool

type jsonInt int64

type jsonFloat float64

type jsonStr string

type jsonArr []jsonValue

type jsonPair struct {
	key string
	val jsonValue
}

type jsonObj []jsonPair

func obj(pairs ...jsonPair) jsonObj  { return jsonObj(pairs) }
func arr(items ...jsonValue) jsonArr { return jsonArr(items) }
func s(v string) jsonValue           { return jsonStr(v) }
func i(v int64) jsonValue            { return jsonInt(v) }
func b(v bool) jsonValue             { return jsonBool(v) }
func f(v float64) jsonValue {
	if v != 0 && (math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) < 1e-4 || math.Abs(v) >= 1e15) {
		panic(fmt.Sprintf("float outside the documented plain-decimal range: %v", v))
	}
	return jsonFloat(v)
}
func kv(k string, v jsonValue) jsonPair { return jsonPair{k, v} }

func (jsonNull) writeTo(b *strings.Builder) { b.WriteString("null") }

func (v jsonBool) writeTo(b *strings.Builder) {
	if v {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
}

func (v jsonInt) writeTo(b *strings.Builder) { b.WriteString(strconv.FormatInt(int64(v), 10)) }

func (v jsonFloat) writeTo(b *strings.Builder) {
	// Shortest round-trip plain decimal, no trailing ".0" — matches Rust
	// Display for f64 for the pinned value range.
	b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 64))
}

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

// lowerHex is the canonical encoding for wire bytes and backing-store
// contents in this slice: lowercase hex without separators.
func lowerHex(bytes []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(bytes)*2)
	for i, v := range bytes {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

// hexDecode parses lowercase hex without separators.
func hexDecode(h string) []byte {
	out := make([]byte, len(h)/2)
	for i := range out {
		out[i] = hexValByte(h[2*i])<<4 | hexValByte(h[2*i+1])
	}
	return out
}

func hexValByte(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return 0
}
