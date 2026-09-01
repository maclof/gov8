//go:build windows && amd64

package main

import (
	"strconv"
	"strings"
)

type jsonValue interface{ writeTo(*strings.Builder) }

type jsonNull struct{}
type jsonBool bool
type jsonInt int64
type jsonStr string
type jsonPair struct {
	key string
	val jsonValue
}
type jsonObj []jsonPair
type jsonArr []jsonValue

func jnull() jsonValue                  { return jsonNull{} }
func jbool(v bool) jsonValue            { return jsonBool(v) }
func jint(v int64) jsonValue            { return jsonInt(v) }
func jstr(v string) jsonValue           { return jsonStr(v) }
func kv(k string, v jsonValue) jsonPair { return jsonPair{k, v} }
func jobj(p ...jsonPair) jsonValue      { return jsonObj(p) }
func jarr(v ...jsonValue) jsonValue     { return jsonArr(v) }
func jopt(v string, ok bool) jsonValue {
	if ok {
		return jstr(v)
	}
	return jnull()
}
func (jsonNull) writeTo(b *strings.Builder) { b.WriteString("null") }
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
	for i, p := range o {
		if i != 0 {
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
	for i, v := range a {
		if i != 0 {
			b.WriteByte(',')
		}
		v.writeTo(b)
	}
	b.WriteByte(']')
}
func writeEscaped(s string, b *strings.Builder) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 {
				b.WriteString("\\u")
				b.WriteString(strconv.FormatInt(int64(r), 16))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
func jsonString(v jsonValue) string {
	var b strings.Builder
	v.writeTo(&b)
	return b.String()
}
