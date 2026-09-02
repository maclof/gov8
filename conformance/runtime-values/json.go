//go:build windows && amd64

package main

// Canonical JSON encoding, byte-compatible with rust-oracle/src/json.rs and
// the other Go conformance modules (same normalization rules). Only the
// encodings used by this slice are included.

import (
	"strconv"
	"strings"
)

type jsonValue interface {
	writeTo(b *strings.Builder)
}

// jsonNull encodes the JSON null value (Json::Null in the oracle).
type jsonNull struct{}

func jnull() jsonValue { return jsonNull{} }

func (jsonNull) writeTo(b *strings.Builder) { b.WriteString("null") }

type jsonBool bool

type jsonInt int64

type jsonStr string

type jsonPair struct {
	key string
	val jsonValue
}

type jsonObj []jsonPair

type jsonArr []jsonValue

func jobj(pairs ...jsonPair) jsonObj    { return jsonObj(pairs) }
func jarr(vals ...jsonValue) jsonArr    { return jsonArr(vals) }
func jstr(v string) jsonValue           { return jsonStr(v) }
func jint(v int64) jsonValue            { return jsonInt(v) }
func jbool(v bool) jsonValue            { return jsonBool(v) }
func kv(k string, v jsonValue) jsonPair { return jsonPair{k, v} }

func (v jsonBool) writeTo(b *strings.Builder) {
	if v {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
}

func (v jsonInt) writeTo(b *strings.Builder) { b.WriteString(strconv.FormatInt(int64(v), 10)) }

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

func (a jsonArr) writeTo(b *strings.Builder) {
	b.WriteByte('[')
	for idx, v := range a {
		if idx > 0 {
			b.WriteByte(',')
		}
		v.writeTo(b)
	}
	b.WriteByte(']')
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
				out.WriteString("\\u" + strconv.FormatInt(int64(ch), 16))
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
