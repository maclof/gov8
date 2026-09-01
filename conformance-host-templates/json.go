//go:build windows && amd64

package main

// Canonical JSON encoding, byte-compatible with rust-oracle/src/json.rs and
// the base conformance runner's json.go (same normalization rules).

import (
	"fmt"
	"strconv"
	"strings"
)

type jsonValue interface {
	writeTo(b *strings.Builder)
}

type jsonNull struct{}

type jsonBool bool

type jsonInt int64

type jsonFloat float64

type jsonStr string

type jsonPair struct {
	key string
	val jsonValue
}

type jsonObj []jsonPair

// jsonRaw embeds an already-rendered JSON fragment.
type jsonRaw string

func (v jsonRaw) writeTo(b *strings.Builder) { b.WriteString(string(v)) }

func jobj(pairs ...jsonPair) jsonObj    { return jsonObj(pairs) }
func jstr(v string) jsonValue           { return jsonStr(v) }
func jint(v int64) jsonValue            { return jsonInt(v) }
func jbool(v bool) jsonValue            { return jsonBool(v) }
func jfloat(v float64) jsonValue        { return jsonFloat(v) }
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
	// Shortest round-trip plain decimal, no trailing ".0" — the oracle's
	// json.rs uses Rust Display for f64 semantics (5.0 renders as "5").
	b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 64))
}

func (v jsonStr) writeTo(b *strings.Builder) { writeEscaped(string(v), b) }

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
