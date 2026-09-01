//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

// Benchmarks for the advanced strings/BigInt slice, aligned one for one
// with the oracle binary's --bench mode
// (rust-oracle/src/bin/conformance-strings-bigint.rs, BENCHES):
//
//	strings_bigint/string_new_ascii_32       -> BenchmarkSBStringNewASCII32
//	strings_bigint/string_from_two_byte_16   -> BenchmarkSBStringFromTwoByte16
//	strings_bigint/string_concat_x4_read     -> BenchmarkSBStringConcatX4Read
//	strings_bigint/string_write_utf8_64      -> BenchmarkSBStringWriteUTF864
//	strings_bigint/string_external_static_new-> BenchmarkSBStringExternalStaticNew
//	strings_bigint/bigint_new_from_i64       -> BenchmarkSBBigIntNewFromI64
//	strings_bigint/bigint_words_roundtrip    -> BenchmarkSBBigIntWordsRoundtrip
//
// Each iteration that creates locals opens a fresh Scope, mirroring the
// oracle's fresh nested HandleScope per iteration. Checksum-style result
// consumption prevents dead-code elimination.

// benchSBStringPayload is the oracle's 32-byte ASCII string.
const benchSBStringPayload = "abcdefghijklmnopqrstuvwxyzabcd"

// benchSBExternalPayload is the oracle's static external payload.
const benchSBExternalPayload = "benchmark-static-external-onebyte-payload"

func BenchmarkSBStringNewASCII32(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		s, err := scope.NewString(benchSBStringPayload)
		if err != nil {
			b.Fatal(err)
		}
		ok, _ := s.IsOneByte()
		n, _ := s.Length()
		benchSBSink += uint64(len(benchSBStringPayload))
		if ok {
			benchSBSink += uint64(n)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBStringFromTwoByte16(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	units := []uint16{
		0x0048, 0x00E9, 0xD83E, 0xDD80, 0xD83E, 0xDD80, 0x0063, 0x0064,
		0x0065, 0x0066, 0x0067, 0x0068, 0x0069, 0x006A, 0x006B, 0x006C,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		s, err := scope.NewStringFromTwoByte(units, gov8.StringNormal)
		if err != nil {
			b.Fatal(err)
		}
		only, _ := s.ContainsOnlyOneByte()
		n, _ := s.Length()
		benchSBSink += uint64(n)
		if !only {
			benchSBSink++
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBStringConcatX4Read(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		a, err := scope.NewString("abcdefgh")
		if err != nil {
			b.Fatal(err)
		}
		bv, err := scope.ConcatString(a, a)
		if err != nil {
			b.Fatal(err)
		}
		cv, err := scope.ConcatString(bv, bv)
		if err != nil {
			b.Fatal(err)
		}
		dv, err := scope.ConcatString(cv, cv)
		if err != nil {
			b.Fatal(err)
		}
		ev, err := scope.ConcatString(dv, dv)
		if err != nil {
			b.Fatal(err)
		}
		// The read: full UTF-8 materialization of the 128-char chain.
		txt, err := ev.ToString(ctx)
		if err != nil {
			b.Fatal(err)
		}
		benchSBSink += uint64(len(txt))
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBStringWriteUTF864(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		s, err := scope.NewString("The quick brown fox jumps over the lazy dog \u20AC\U0001F980!")
		if err != nil {
			b.Fatal(err)
		}
		buf := make([]byte, 128)
		n, _, err := s.WriteUTF8(buf, 0)
		if err != nil {
			b.Fatal(err)
		}
		if n > 0 {
			benchSBSink++
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBStringExternalStaticNew(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	payload := []byte(benchSBExternalPayload)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		s, err := scope.NewExternalOneByteStringStatic(payload)
		if err != nil {
			b.Fatal(err)
		}
		ok, _ := s.IsExternalOneByte()
		n, _ := s.Length()
		benchSBSink += uint64(n)
		if ok {
			benchSBSink++
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBBigIntNewFromI64(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		v, err := scope.BigIntFromInt64(-1_234_567_890_123_456_789)
		if err != nil {
			b.Fatal(err)
		}
		val, lossless, err := v.BigIntInt64()
		if err != nil {
			b.Fatal(err)
		}
		benchSBSink += uint64(val)
		if lossless {
			benchSBSink++
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSBBigIntWordsRoundtrip(b *testing.B) {
	iso, ctx := benchSBRuntime(b)
	defer func() { _ = ctx.Close(); _ = iso.Close() }()
	words := []uint64{^uint64(0), 42}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		v, err := scope.BigIntFromWords(ctx, false, words, nil)
		if err != nil {
			b.Fatal(err)
		}
		out := make([]uint64, 2)
		sign, got, err := v.BigIntToWords(out)
		if err != nil {
			b.Fatal(err)
		}
		wc, err := v.BigIntWordCount()
		if err != nil {
			b.Fatal(err)
		}
		if !sign && len(got) == 2 && got[0] == words[0] && got[1] == words[1] {
			benchSBSink++
		}
		benchSBSink += uint64(wc)
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// benchSBSink accumulates benchmark results so the compiler cannot
// eliminate the measured work.
var benchSBSink uint64

// benchSBRuntime prepares an isolate/context pair shared by the benchmarks
// (the scope is opened per iteration).
func benchSBRuntime(b *testing.B) (*gov8.Isolate, *gov8.Context) {
	b.Helper()
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatalf("NewIsolate: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatalf("NewContext: %v", err)
	}
	return iso, ctx
}
