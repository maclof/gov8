//go:build windows && amd64

package gov8_test

import (
	"testing"

	gov8 "gov8"
)

func simdutfMixed4K() []byte {
	unit := []byte("Aé€😀")
	input := make([]byte, 0, 4096)
	for len(input)+len(unit) <= 4096 {
		input = append(input, unit...)
	}
	for len(input) < 4096 {
		input = append(input, 'A')
	}
	return input
}

func BenchmarkSIMDUTFValidateUTF8Mixed4K(b *testing.B) {
	input := simdutfMixed4K()
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		valid, err := gov8.SIMDUTFValidateUTF8(input)
		if err != nil || !valid {
			b.Fatalf("ValidateUTF8 = %v, %v", valid, err)
		}
	}
}

func BenchmarkSIMDUTFUTF8ToUTF16LE4K(b *testing.B) {
	input := simdutfMixed4K()
	output := make([]uint16, len(input))
	expected, err := gov8.SIMDUTFUTF16LengthFromUTF8(input)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if written, err := gov8.SIMDUTFConvertUTF8ToUTF16LE(input, output); err != nil || written != expected {
			b.Fatalf("ConvertUTF8ToUTF16LE = %d, %v", written, err)
		}
	}
}

func BenchmarkSIMDUTFUTF16LEToUTF8_4K(b *testing.B) {
	input8 := simdutfMixed4K()
	input16 := make([]uint16, len(input8))
	written, err := gov8.SIMDUTFConvertUTF8ToUTF16LE(input8, input16)
	if err != nil {
		b.Fatal(err)
	}
	input16 = input16[:written]
	output := make([]byte, len(input16)*3)
	b.SetBytes(int64(len(input8)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if n, err := gov8.SIMDUTFConvertUTF16LEToUTF8(input16, output); err != nil || n != len(input8) {
			b.Fatalf("ConvertUTF16LEToUTF8 = %d, %v", n, err)
		}
	}
}

func BenchmarkSIMDUTFBase64DecodeStandard4K(b *testing.B) {
	binary := make([]byte, 3072)
	for i := range binary {
		binary[i] = 0x5a
	}
	encodedLength, err := gov8.SIMDUTFBase64LengthFromBinary(uint64(len(binary)), gov8.SIMDUTFBase64Default)
	if err != nil {
		b.Fatal(err)
	}
	encoded := make([]byte, int(encodedLength))
	if _, err := gov8.SIMDUTFBinaryToBase64(binary, encoded, gov8.SIMDUTFBase64Default); err != nil {
		b.Fatal(err)
	}
	max, err := gov8.SIMDUTFMaximalBinaryLengthFromBase64(encoded)
	if err != nil {
		b.Fatal(err)
	}
	output := make([]byte, max)
	b.SetBytes(int64(len(encoded)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, err := gov8.SIMDUTFBase64ToBinary(encoded, output, gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict)
		if err != nil || !result.OK() || result.Count != len(binary) {
			b.Fatalf("Base64ToBinary = %+v, %v", result, err)
		}
	}
}

func BenchmarkSIMDUTFBase64EncodeStandard3K(b *testing.B) {
	binary := make([]byte, 3072)
	for i := range binary {
		binary[i] = 0x5a
	}
	encodedLength, err := gov8.SIMDUTFBase64LengthFromBinary(uint64(len(binary)), gov8.SIMDUTFBase64Default)
	if err != nil {
		b.Fatal(err)
	}
	output := make([]byte, int(encodedLength))
	b.SetBytes(int64(len(binary)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		written, err := gov8.SIMDUTFBinaryToBase64(binary, output, gov8.SIMDUTFBase64Default)
		if err != nil || written != len(output) {
			b.Fatalf("BinaryToBase64 = %d, %v", written, err)
		}
	}
}
