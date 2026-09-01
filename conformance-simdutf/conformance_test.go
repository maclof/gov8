//go:build windows && amd64

package simdutfconformance

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	gov8 "github.com/maclof/gov8"
)

type fixtureLine struct {
	Check string `json:"check"`
	OK    bool   `json:"ok"`
	Value any    `json:"value"`
}

func fixtures(t *testing.T) map[string]fixtureLine {
	t.Helper()
	path := filepath.Join("..", "rust-oracle", "tests", "fixtures", "conformance-simdutf-v8_152.2.0_x86_64-pc-windows-msvc.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result := map[string]fixtureLine{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err == nil && line.Check != "" {
			result[line.Check] = line
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, fs map[string]fixtureLine, id string, got any) {
	t.Helper()
	want, ok := fs[id]
	if !ok || !want.OK {
		t.Fatalf("missing fixture %s", id)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want.Value)
	if string(g) != string(w) {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", id, g, w)
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
func result(r gov8.SIMDUTFResult) map[string]any {
	return map[string]any{"error": r.Error.String(), "code": int32(r.Error), "count": r.Count, "ok": r.OK()}
}

func TestRustOracleFixture(t *testing.T) {
	fs := fixtures(t)
	t.Run("validation", func(t *testing.T) {
		byteCases := []struct {
			name  string
			input []byte
		}{{"empty", nil}, {"valid", []byte("Aé€😀")}, {"continuation", []byte{0x80}}, {"header_bits", []byte{0xff}}, {"short", []byte{0xe2, 0x82}}, {"long", []byte{0xc2, 0xa2, 0x80}}, {"overlong", []byte{0xc0, 0x80}}, {"large", []byte{0xf4, 0x90, 0x80, 0x80}}, {"surrogate", []byte{0xed, 0xa0, 0x80}}}
		utf8 := make([]any, 0, len(byteCases))
		for _, c := range byteCases {
			valid := must(gov8.SIMDUTFValidateUTF8(c.input))
			r := must(gov8.SIMDUTFValidateUTF8WithErrors(c.input))
			utf8 = append(utf8, map[string]any{"case": c.name, "valid": valid, "result": result(r)})
		}
		ascii := []any{}
		for _, c := range []struct {
			name  string
			input []byte
		}{{"empty", nil}, {"valid", []byte("ASCII")}, {"invalid", []byte{'A', 0x80, 'B'}}} {
			valid := must(gov8.SIMDUTFValidateASCII(c.input))
			r := must(gov8.SIMDUTFValidateASCIIWithErrors(c.input))
			ascii = append(ascii, map[string]any{"case": c.name, "valid": valid, "result": result(r)})
		}
		utf16le := []any{}
		for _, c := range []struct {
			name  string
			input []uint16
		}{{"empty", nil}, {"valid", []uint16{0x41, 0xd83d, 0xde00}}, {"high_alone", []uint16{0x41, 0xd800}}, {"low_alone", []uint16{0x41, 0xdc00}}} {
			valid := must(gov8.SIMDUTFValidateUTF16LE(c.input))
			r := must(gov8.SIMDUTFValidateUTF16LEWithErrors(c.input))
			utf16le = append(utf16le, map[string]any{"case": c.name, "valid": valid, "result": result(r)})
		}
		utf16be := []any{}
		for _, c := range []struct {
			name  string
			input []uint16
		}{{"empty", nil}, {"valid", []uint16{0x4100, 0x3dd8, 0x00de}}, {"high_alone", []uint16{0x4100, 0x00d8}}, {"low_alone", []uint16{0x4100, 0x00dc}}} {
			valid := must(gov8.SIMDUTFValidateUTF16BE(c.input))
			r := must(gov8.SIMDUTFValidateUTF16BEWithErrors(c.input))
			utf16be = append(utf16be, map[string]any{"case": c.name, "valid": valid, "result": result(r)})
		}
		utf32 := []any{}
		for _, c := range []struct {
			name  string
			input []uint32
		}{{"empty", nil}, {"valid", []uint32{0x41, 0x20ac, 0x1f600}}, {"surrogate", []uint32{0x41, 0xd800}}, {"large", []uint32{0x41, 0x110000}}} {
			valid := must(gov8.SIMDUTFValidateUTF32(c.input))
			r := must(gov8.SIMDUTFValidateUTF32WithErrors(c.input))
			utf32 = append(utf32, map[string]any{"case": c.name, "valid": valid, "result": result(r)})
		}
		compare(t, fs, "simdutf/validation", map[string]any{"utf8": utf8, "ascii": ascii, "utf16le": utf16le, "utf16be": utf16be, "utf32": utf32})
	})

	t.Run("unicode_conversions", func(t *testing.T) {
		u8 := []byte("Aé€😀")
		le := []uint16{0x41, 0xe9, 0x20ac, 0xd83d, 0xde00}
		be := []uint16{0x4100, 0xe900, 0xac20, 0x3dd8, 0x00de}
		u32 := []uint32{0x41, 0xe9, 0x20ac, 0x1f600}
		toLE := fill16(len(u8)+2, 0xdead)
		nLE := must(gov8.SIMDUTFConvertUTF8ToUTF16LE(u8, toLE))
		toLEE := fill16(len(u8)+2, 0xdead)
		rLE := must(gov8.SIMDUTFConvertUTF8ToUTF16LEWithErrors(u8, toLEE))
		toLEV := fill16(len(u8)+2, 0xdead)
		nLEV := must(gov8.SIMDUTFConvertValidUTF8ToUTF16LE(u8, toLEV))
		fromLE := fill8(len(le)*3+2, 0xcc)
		nFromLE := must(gov8.SIMDUTFConvertUTF16LEToUTF8(le, fromLE))
		fromLEE := fill8(len(le)*3+2, 0xcc)
		rFromLE := must(gov8.SIMDUTFConvertUTF16LEToUTF8WithErrors(le, fromLEE))
		fromLEV := fill8(len(le)*3+2, 0xcc)
		nFromLEV := must(gov8.SIMDUTFConvertValidUTF16LEToUTF8(le, fromLEV))
		toBE := fill16(len(u8)+2, 0xdead)
		nToBE := must(gov8.SIMDUTFConvertUTF8ToUTF16BE(u8, toBE))
		fromBE := fill8(len(be)*3+2, 0xcc)
		nFromBE := must(gov8.SIMDUTFConvertUTF16BEToUTF8(be, fromBE))
		to32 := fill32(len(u8)+2, 0xdeadbeef)
		nTo32 := must(gov8.SIMDUTFConvertUTF8ToUTF32(u8, to32))
		from32 := fill8(len(u32)*4+2, 0xcc)
		nFrom32 := must(gov8.SIMDUTFConvertUTF32ToUTF8(u32, from32))
		bad8 := []byte{'A', 0xe2, 0x82}
		badLE := fill16(5, 0xdead)
		badLEN := must(gov8.SIMDUTFConvertUTF8ToUTF16LE(bad8, badLE))
		badLEE := fill16(5, 0xdead)
		badLER := must(gov8.SIMDUTFConvertUTF8ToUTF16LEWithErrors(bad8, badLEE))
		bad16 := []uint16{0x41, 0xd800}
		badFrom := fill8(8, 0xcc)
		badFromN := must(gov8.SIMDUTFConvertUTF16LEToUTF8(bad16, badFrom))
		badFromE := fill8(8, 0xcc)
		badFromR := must(gov8.SIMDUTFConvertUTF16LEToUTF8WithErrors(bad16, badFromE))
		badBE := fill8(8, 0xcc)
		badBEN := must(gov8.SIMDUTFConvertUTF16BEToUTF8([]uint16{0x4100, 0x00d8}, badBE))
		badToBE := fill16(5, 0xdead)
		badToBEN := must(gov8.SIMDUTFConvertUTF8ToUTF16BE(bad8, badToBE))
		badTo32 := fill32(5, 0xdeadbeef)
		badTo32N := must(gov8.SIMDUTFConvertUTF8ToUTF32(bad8, badTo32))
		bad32 := fill8(8, 0xcc)
		bad32N := must(gov8.SIMDUTFConvertUTF32ToUTF8([]uint32{0x41, 0x110000}, bad32))
		compare(t, fs, "simdutf/unicode_conversions", map[string]any{
			"utf8_to_utf16le": map[string]any{"count": nLE, "output": toLE[:nLE], "tail_preserved": all16(toLE[nLE:], 0xdead), "with_errors": result(rLE), "with_errors_output": toLEE[:rLE.Count], "valid_count": nLEV, "valid_output": toLEV[:nLEV]},
			"utf16le_to_utf8": map[string]any{"count": nFromLE, "output": bytesJSON(fromLE[:nFromLE]), "tail_preserved": all8(fromLE[nFromLE:], 0xcc), "with_errors": result(rFromLE), "with_errors_output": bytesJSON(fromLEE[:rFromLE.Count]), "valid_count": nFromLEV, "valid_output": bytesJSON(fromLEV[:nFromLEV])},
			"utf8_utf16be":    map[string]any{"to_count": nToBE, "to_output": toBE[:nToBE], "from_count": nFromBE, "from_output": bytesJSON(fromBE[:nFromBE])}, "utf8_utf32": map[string]any{"to_count": nTo32, "to_output": to32[:nTo32], "from_count": nFrom32, "from_output": bytesJSON(from32[:nFrom32])},
			"malformed": map[string]any{"utf8_to_le_count": badLEN, "utf8_to_le_result": result(badLER), "le_to_utf8_count": badFromN, "le_to_utf8_result": result(badFromR), "be_to_utf8_count": badBEN, "utf8_to_be_count": badToBEN, "utf8_to_utf32_count": badTo32N, "utf32_to_utf8_count": bad32N}})
	})

	t.Run("latin1_conversions", func(t *testing.T) {
		latin := []byte{0x41, 0xe9, 0xff}
		u8 := []byte{0x41, 0xc3, 0xa9, 0xc3, 0xbf}
		u16 := []uint16{0x41, 0xe9, 0xff}
		to := fill8(7, 0xcc)
		n := must(gov8.SIMDUTFConvertUTF8ToLatin1(u8, to))
		toe := fill8(7, 0xcc)
		r := must(gov8.SIMDUTFConvertUTF8ToLatin1WithErrors(u8, toe))
		tov := fill8(7, 0xcc)
		nv := must(gov8.SIMDUTFConvertValidUTF8ToLatin1(u8, tov))
		out8 := fill8(8, 0xcc)
		n8 := must(gov8.SIMDUTFConvertLatin1ToUTF8(latin, out8))
		out16 := fill16(5, 0xdead)
		n16 := must(gov8.SIMDUTFConvertLatin1ToUTF16LE(latin, out16))
		back := fill8(5, 0xcc)
		nb := must(gov8.SIMDUTFConvertUTF16LEToLatin1(u16, back))
		outside := []byte("A€")
		outsideOut := fill8(5, 0xcc)
		outsideN := must(gov8.SIMDUTFConvertUTF8ToLatin1(outside, outsideOut))
		outsideE := fill8(5, 0xcc)
		outsideR := must(gov8.SIMDUTFConvertUTF8ToLatin1WithErrors(outside, outsideE))
		compare(t, fs, "simdutf/latin1_conversions", map[string]any{"utf8_to_count": n, "utf8_to_output": bytesJSON(to[:n]), "utf8_to_result": result(r), "utf8_to_errors_output": bytesJSON(toe[:r.Count]), "valid_count": nv, "valid_output": bytesJSON(tov[:nv]), "latin1_to_utf8_count": n8, "latin1_to_utf8": bytesJSON(out8[:n8]), "latin1_to_utf16_count": n16, "latin1_to_utf16": out16[:n16], "utf16_to_latin1_count": nb, "utf16_to_latin1": bytesJSON(back[:nb]), "outside_count": outsideN, "outside_result": result(outsideR)})
	})

	t.Run("lengths_counts_detection", func(t *testing.T) {
		u8 := []byte("Aé€😀")
		le := []uint16{0x41, 0xe9, 0x20ac, 0xd83d, 0xde00}
		be := []uint16{0x4100, 0xe900, 0xac20, 0x3dd8, 0x00de}
		u32 := []uint32{0x41, 0xe9, 0x20ac, 0x1f600}
		latin := []byte{0x41, 0xe9, 0xff}
		detection := []any{}
		for _, c := range []struct {
			name  string
			value []byte
		}{{"empty", nil}, {"ascii", []byte("ABC")}, {"utf8", []byte("€")}, {"utf16le_bom", []byte{0xff, 0xfe, 0x41, 0}}, {"utf16be_bom", []byte{0xfe, 0xff, 0, 0x41}}, {"utf32le_bom", []byte{0xff, 0xfe, 0, 0, 0x41, 0, 0, 0}}, {"utf32be_bom", []byte{0, 0, 0xfe, 0xff, 0, 0, 0, 0x41}}} {
			m := must(gov8.SIMDUTFDetectEncodings(c.value))
			detection = append(detection, map[string]any{"case": c.name, "mask": int32(m), "utf8": m&gov8.SIMDUTFEncodingUTF8 != 0, "utf16le": m&gov8.SIMDUTFEncodingUTF16LE != 0, "utf16be": m&gov8.SIMDUTFEncodingUTF16BE != 0, "utf32le": m&gov8.SIMDUTFEncodingUTF32LE != 0, "utf32be": m&gov8.SIMDUTFEncodingUTF32BE != 0, "latin1": m&gov8.SIMDUTFEncodingLatin1 != 0})
		}
		compare(t, fs, "simdutf/lengths_counts_detection", map[string]any{"lengths": map[string]any{"utf8_from_utf16le": must(gov8.SIMDUTFUTF8LengthFromUTF16LE(le)), "utf8_from_utf16be": must(gov8.SIMDUTFUTF8LengthFromUTF16BE(be)), "utf16_from_utf8": must(gov8.SIMDUTFUTF16LengthFromUTF8(u8)), "utf8_from_latin1": must(gov8.SIMDUTFUTF8LengthFromLatin1(latin)), "latin1_from_utf8": must(gov8.SIMDUTFLatin1LengthFromUTF8([]byte{0x41, 0xc3, 0xa9})), "utf32_from_utf8": must(gov8.SIMDUTFUTF32LengthFromUTF8(u8)), "utf8_from_utf32": must(gov8.SIMDUTFUTF8LengthFromUTF32(u32)), "utf16_from_utf32": must(gov8.SIMDUTFUTF16LengthFromUTF32(u32)), "utf32_from_utf16le": must(gov8.SIMDUTFUTF32LengthFromUTF16LE(le))}, "empty_lengths": []any{must(gov8.SIMDUTFUTF8LengthFromUTF16LE(nil)), must(gov8.SIMDUTFUTF8LengthFromUTF16BE(nil)), must(gov8.SIMDUTFUTF16LengthFromUTF8(nil)), must(gov8.SIMDUTFUTF8LengthFromLatin1(nil)), must(gov8.SIMDUTFLatin1LengthFromUTF8(nil)), must(gov8.SIMDUTFUTF32LengthFromUTF8(nil)), must(gov8.SIMDUTFUTF8LengthFromUTF32(nil)), must(gov8.SIMDUTFUTF16LengthFromUTF32(nil)), must(gov8.SIMDUTFUTF32LengthFromUTF16LE(nil))}, "counts": map[string]any{"utf8": must(gov8.SIMDUTFCountUTF8(u8)), "utf16le": must(gov8.SIMDUTFCountUTF16LE(le)), "utf16be": must(gov8.SIMDUTFCountUTF16BE(be)), "empty_utf8": must(gov8.SIMDUTFCountUTF8(nil)), "empty_utf16le": must(gov8.SIMDUTFCountUTF16LE(nil)), "empty_utf16be": must(gov8.SIMDUTFCountUTF16BE(nil))}, "detection": detection, "encoding_constants": []any{1, 2, 4, 8, 16, 32}})
	})

	t.Run("base64", func(t *testing.T) {
		binary := []byte{0xfb, 0xff}
		opts := []struct {
			name  string
			value gov8.SIMDUTFBase64Options
		}{{"default", gov8.SIMDUTFBase64Default}, {"url", gov8.SIMDUTFBase64URL}, {"default_no_padding", gov8.SIMDUTFBase64DefaultNoPadding}, {"url_with_padding", gov8.SIMDUTFBase64URLWithPadding}}
		encoded := []any{}
		lengths := []any{}
		for _, o := range opts {
			n := int(must(gov8.SIMDUTFBase64LengthFromBinary(uint64(len(binary)), o.value)))
			out := fill8(n+2, 0xcc)
			count := must(gov8.SIMDUTFBinaryToBase64(binary, out, o.value))
			encoded = append(encoded, map[string]any{"option": o.name, "length": n, "count": count, "text": string(out[:count]), "tail_preserved": all8(out[count:], 0xcc)})
			ns := []any{}
			for i := 0; i <= 4; i++ {
				ns = append(ns, must(gov8.SIMDUTFBase64LengthFromBinary(uint64(i), o.value)))
			}
			lengths = append(lengths, map[string]any{"option": o.name, "n0_to_n4": ns,
				"usize_max_minus_one": strconv.FormatUint(must(gov8.SIMDUTFBase64LengthFromBinary(^uint64(0)-1, o.value)), 10),
				"usize_max":           strconv.FormatUint(must(gov8.SIMDUTFBase64LengthFromBinary(^uint64(0), o.value)), 10)})
		}
		type dc struct {
			name   string
			input  []byte
			option gov8.SIMDUTFBase64Options
			last   gov8.SIMDUTFLastChunkHandling
		}
		cases := []dc{{"empty", nil, gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkLoose}, {"default_padded", []byte("+/8="), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict}, {"url_unpadded", []byte("-_8"), gov8.SIMDUTFBase64URL, gov8.SIMDUTFLastChunkLoose}, {"default_no_padding_strict", []byte("+/8"), gov8.SIMDUTFBase64DefaultNoPadding, gov8.SIMDUTFLastChunkStrict}, {"default_no_padding_loose", []byte("+/8"), gov8.SIMDUTFBase64DefaultNoPadding, gov8.SIMDUTFLastChunkLoose}, {"url_with_padding", []byte("-_8="), gov8.SIMDUTFBase64URLWithPadding, gov8.SIMDUTFLastChunkStrict}, {"default_rejects_url_alphabet", []byte("-_8="), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict}, {"url_accepts_standard_alphabet", []byte("+/8="), gov8.SIMDUTFBase64URL, gov8.SIMDUTFLastChunkStrict}, {"whitespace", []byte(" T W\nE= "), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict}, {"invalid_character", []byte("TW$u"), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkLoose}, {"partial_loose", []byte("TQ"), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkLoose}, {"partial_strict", []byte("TQ"), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict}, {"partial_stop", []byte("TQ"), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStopBeforePartial}, {"partial_full_only", []byte("TQ"), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkOnlyFullChunks}, {"extra_bits", []byte("TR=="), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict}}
		decoded := []any{}
		for _, c := range cases {
			max := must(gov8.SIMDUTFMaximalBinaryLengthFromBase64(c.input))
			out := fill8(max+2, 0xcc)
			r := must(gov8.SIMDUTFBase64ToBinary(c.input, out, c.option, c.last))
			var prefix any
			if r.OK() {
				prefix = bytesJSON(out[:r.Count])
			}
			decoded = append(decoded, map[string]any{"case": c.name, "max": max, "result": result(r), "output": prefix, "guard_preserved": all8(out[max:], 0xcc)})
		}
		compare(t, fs, "simdutf/base64", map[string]any{"encoded": encoded, "lengths": lengths, "decoded": decoded})
	})
}

func fill8(n int, v byte) []byte {
	r := make([]byte, n)
	for i := range r {
		r[i] = v
	}
	return r
}
func fill16(n int, v uint16) []uint16 {
	r := make([]uint16, n)
	for i := range r {
		r[i] = v
	}
	return r
}
func fill32(n int, v uint32) []uint32 {
	r := make([]uint32, n)
	for i := range r {
		r[i] = v
	}
	return r
}
func all8(v []byte, w byte) bool {
	for _, x := range v {
		if x != w {
			return false
		}
	}
	return true
}
func all16(v []uint16, w uint16) bool {
	for _, x := range v {
		if x != w {
			return false
		}
	}
	return true
}

func bytesJSON(values []byte) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = int(value)
	}
	return result
}
