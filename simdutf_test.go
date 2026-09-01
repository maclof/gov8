//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"reflect"
	"testing"

	gov8 "gov8"
)

func TestSIMDUTFValidationConversionsAndCapacity(t *testing.T) {
	input := []byte("Aé€😀")
	valid, err := gov8.SIMDUTFValidateUTF8(input)
	if err != nil || !valid {
		t.Fatalf("ValidateUTF8 = %v, %v", valid, err)
	}
	bad, err := gov8.SIMDUTFValidateUTF8WithErrors([]byte{'A', 0xe2, 0x82})
	if err != nil || bad.Error != gov8.SIMDUTFTooShort || bad.Count != 1 {
		t.Fatalf("malformed result = %+v, %v", bad, err)
	}
	if _, err := gov8.SIMDUTFConvertUTF8ToUTF16LE(input, make([]uint16, len(input)-1)); err == nil {
		t.Fatal("undersized UTF-16 destination accepted")
	}
	if _, err := gov8.SIMDUTFConvertUTF16LEToUTF8([]uint16{0x41, 0xe9}, make([]byte, 5)); err == nil {
		t.Fatal("undersized UTF-8 destination accepted")
	}
	out := make([]uint16, len(input)+2)
	for i := range out {
		out[i] = 0xdead
	}
	n, err := gov8.SIMDUTFConvertUTF8ToUTF16LE(input, out)
	if err != nil || n != 5 || !reflect.DeepEqual(out[:n], []uint16{65, 233, 8364, 55357, 56832}) {
		t.Fatalf("UTF8ToUTF16LE = %d, %v, %v", n, out[:n], err)
	}
	if out[n] != 0xdead || out[n+1] != 0xdead {
		t.Fatal("conversion overwrote destination tail")
	}
	if n, err := gov8.SIMDUTFConvertUTF8ToUTF16LE(nil, nil); err != nil || n != 0 {
		t.Fatalf("empty conversion = %d, %v", n, err)
	}
}

func TestSIMDUTFBase64OptionsAndBoundaries(t *testing.T) {
	input := []byte{0xfb, 0xff}
	for _, tc := range []struct {
		option gov8.SIMDUTFBase64Options
		want   string
	}{{gov8.SIMDUTFBase64Default, "+/8="}, {gov8.SIMDUTFBase64URL, "-_8"},
		{gov8.SIMDUTFBase64DefaultNoPadding, "+/8"}, {gov8.SIMDUTFBase64URLWithPadding, "-_8="}} {
		length64, err := gov8.SIMDUTFBase64LengthFromBinary(uint64(len(input)), tc.option)
		if err != nil {
			t.Fatal(err)
		}
		length := int(length64)
		out := bytes.Repeat([]byte{0xcc}, length+2)
		n, err := gov8.SIMDUTFBinaryToBase64(input, out, tc.option)
		if err != nil || string(out[:n]) != tc.want || out[n] != 0xcc {
			t.Fatalf("option %d = %q, %d, %v", tc.option, out[:n], n, err)
		}
	}
	max, err := gov8.SIMDUTFMaximalBinaryLengthFromBase64([]byte("+/8="))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gov8.SIMDUTFBase64ToBinary([]byte("+/8="), make([]byte, max-1), gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict); err == nil {
		t.Fatal("undersized base64 destination accepted")
	}
	decoded := make([]byte, max)
	result, err := gov8.SIMDUTFBase64ToBinary([]byte("+/8="), decoded, gov8.SIMDUTFBase64Default, gov8.SIMDUTFLastChunkStrict)
	if err != nil || !result.OK() || !bytes.Equal(decoded[:result.Count], input) {
		t.Fatalf("decode = %+v, %v, %v", result, decoded, err)
	}
	if _, err := gov8.SIMDUTFBase64LengthFromBinary(1, 99); err == nil {
		t.Fatal("unknown base64 option accepted")
	}
}

func TestSIMDUTFStateIndependentAndSafeValidConversions(t *testing.T) {
	if valid, err := gov8.SIMDUTFValidateASCII([]byte("ASCII")); err != nil || !valid {
		t.Fatalf("state-free validation = %v, %v", valid, err)
	}
	if _, err := gov8.SIMDUTFConvertValidUTF8ToUTF16LE([]byte{0xff}, make([]uint16, 1)); err == nil {
		t.Fatal("unsafe valid-input path accepted malformed UTF-8")
	}
	if _, err := gov8.SIMDUTFConvertValidUTF8ToLatin1([]byte("€"), make([]byte, 3)); err == nil {
		t.Fatal("unsafe Latin-1 path accepted out-of-range codepoint")
	}
}
