//go:build windows && amd64

package gov8

import (
	"errors"
	"fmt"
	"runtime"
	"unicode/utf8"
	"unsafe"
)

// The SIMDUTF-prefixed API maps rusty_v8's simdutf namespace into the gov8
// package. Rust marks conversion destinations and valid-input fast paths
// unsafe. Go instead checks every documented worst-case destination size and
// validates the fast-path input, returning an error before native code on
// misuse. SIMDUTFResult continues to represent simdutf data errors; the
// separate Go error reports wrapper/precondition failures.

// SIMDUTFErrorCode is a pinned simdutf error category.
type SIMDUTFErrorCode int32

const (
	SIMDUTFSuccess                SIMDUTFErrorCode = 0
	SIMDUTFHeaderBits             SIMDUTFErrorCode = 1
	SIMDUTFTooShort               SIMDUTFErrorCode = 2
	SIMDUTFTooLong                SIMDUTFErrorCode = 3
	SIMDUTFOverlong               SIMDUTFErrorCode = 4
	SIMDUTFTooLarge               SIMDUTFErrorCode = 5
	SIMDUTFSurrogate              SIMDUTFErrorCode = 6
	SIMDUTFInvalidBase64Character SIMDUTFErrorCode = 7
	SIMDUTFBase64InputRemainder   SIMDUTFErrorCode = 8
	SIMDUTFBase64ExtraBits        SIMDUTFErrorCode = 9
	SIMDUTFOutputBufferTooSmall   SIMDUTFErrorCode = 10
	SIMDUTFOther                  SIMDUTFErrorCode = 11
)

func (c SIMDUTFErrorCode) String() string {
	names := [...]string{"Success", "HeaderBits", "TooShort", "TooLong", "Overlong", "TooLarge", "Surrogate", "InvalidBase64Character", "Base64InputRemainder", "Base64ExtraBits", "OutputBufferTooSmall", "Other"}
	if c < SIMDUTFSuccess || c > SIMDUTFOther {
		return "Other"
	}
	return names[c]
}

// SIMDUTFResult reports either the number of units processed/written or the
// input position at which conversion failed.
type SIMDUTFResult struct {
	Error SIMDUTFErrorCode
	Count int
}

func (r SIMDUTFResult) OK() bool { return r.Error == SIMDUTFSuccess }

func simdutfErrorCode(code int32) SIMDUTFErrorCode {
	if code < int32(SIMDUTFSuccess) || code > int32(SIMDUTFOther) {
		return SIMDUTFOther
	}
	return SIMDUTFErrorCode(code)
}

// SIMDUTFEncoding is a bitmask returned by SIMDUTFDetectEncodings.
type SIMDUTFEncoding int32

const (
	SIMDUTFEncodingUTF8    SIMDUTFEncoding = 1
	SIMDUTFEncodingUTF16LE SIMDUTFEncoding = 2
	SIMDUTFEncodingUTF16BE SIMDUTFEncoding = 4
	SIMDUTFEncodingUTF32LE SIMDUTFEncoding = 8
	SIMDUTFEncodingUTF32BE SIMDUTFEncoding = 16
	SIMDUTFEncodingLatin1  SIMDUTFEncoding = 32
)

// SIMDUTFBase64Options selects alphabet and padding behavior.
type SIMDUTFBase64Options uint64

const (
	SIMDUTFBase64Default SIMDUTFBase64Options = iota
	SIMDUTFBase64URL
	SIMDUTFBase64DefaultNoPadding
	SIMDUTFBase64URLWithPadding
)

// SIMDUTFLastChunkHandling controls incomplete final base64 groups.
type SIMDUTFLastChunkHandling uint64

const (
	SIMDUTFLastChunkLoose SIMDUTFLastChunkHandling = iota
	SIMDUTFLastChunkStrict
	SIMDUTFLastChunkStopBeforePartial
	SIMDUTFLastChunkOnlyFullChunks
)

func slicePointer[T any](values []T) uintptr {
	if len(values) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&values[0]))
}

func simdutfValidate(kind int32, input uintptr, length int, withErrors bool) (bool, SIMDUTFResult, error) {
	var valid, code int32
	var count uintptr
	with := uintptr(0)
	if withErrors {
		with = 1
	}
	r1, _, _ := proc("gov8_simdutf_validate").Call(uintptr(kind), input, uintptr(length), with,
		uintptr(unsafe.Pointer(&valid)), uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&count)))
	if int64(r1) < 0 {
		return false, SIMDUTFResult{}, shimError("SIMDUTF.Validate", r1)
	}
	return valid != 0, SIMDUTFResult{Error: simdutfErrorCode(code), Count: int(count)}, nil
}

func SIMDUTFValidateUTF8(input []byte) (bool, error) {
	v, _, e := simdutfValidate(0, slicePointer(input), len(input), false)
	runtime.KeepAlive(input)
	return v, e
}
func SIMDUTFValidateUTF8WithErrors(input []byte) (SIMDUTFResult, error) {
	_, r, e := simdutfValidate(0, slicePointer(input), len(input), true)
	runtime.KeepAlive(input)
	return r, e
}
func SIMDUTFValidateASCII(input []byte) (bool, error) {
	v, _, e := simdutfValidate(1, slicePointer(input), len(input), false)
	runtime.KeepAlive(input)
	return v, e
}
func SIMDUTFValidateASCIIWithErrors(input []byte) (SIMDUTFResult, error) {
	_, r, e := simdutfValidate(1, slicePointer(input), len(input), true)
	runtime.KeepAlive(input)
	return r, e
}
func SIMDUTFValidateUTF16LE(input []uint16) (bool, error) {
	v, _, e := simdutfValidate(2, slicePointer(input), len(input), false)
	runtime.KeepAlive(input)
	return v, e
}
func SIMDUTFValidateUTF16LEWithErrors(input []uint16) (SIMDUTFResult, error) {
	_, r, e := simdutfValidate(2, slicePointer(input), len(input), true)
	runtime.KeepAlive(input)
	return r, e
}
func SIMDUTFValidateUTF16BE(input []uint16) (bool, error) {
	v, _, e := simdutfValidate(3, slicePointer(input), len(input), false)
	runtime.KeepAlive(input)
	return v, e
}
func SIMDUTFValidateUTF16BEWithErrors(input []uint16) (SIMDUTFResult, error) {
	_, r, e := simdutfValidate(3, slicePointer(input), len(input), true)
	runtime.KeepAlive(input)
	return r, e
}
func SIMDUTFValidateUTF32(input []uint32) (bool, error) {
	v, _, e := simdutfValidate(4, slicePointer(input), len(input), false)
	runtime.KeepAlive(input)
	return v, e
}
func SIMDUTFValidateUTF32WithErrors(input []uint32) (SIMDUTFResult, error) {
	_, r, e := simdutfValidate(4, slicePointer(input), len(input), true)
	runtime.KeepAlive(input)
	return r, e
}

func simdutfCapacity(outputLength, inputLength, multiplier int) error {
	if inputLength > int(^uint(0)>>1)/multiplier {
		return errors.New("gov8: simdutf destination size overflows int")
	}
	if outputLength < inputLength*multiplier {
		return fmt.Errorf("gov8: simdutf destination capacity %d, need %d", outputLength, inputLength*multiplier)
	}
	return nil
}

func simdutfConvert(kind int32, input uintptr, inputLength int, output uintptr, outputLength int) (SIMDUTFResult, error) {
	var code int32
	var count uintptr
	r1, _, _ := proc("gov8_simdutf_convert").Call(uintptr(kind), input, uintptr(inputLength), output, uintptr(outputLength),
		uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&count)))
	if int64(r1) < 0 {
		return SIMDUTFResult{}, shimError("SIMDUTF.Convert", r1)
	}
	return SIMDUTFResult{Error: simdutfErrorCode(code), Count: int(count)}, nil
}

func simdutfConvertSlices[I, O any](kind int32, input []I, output []O, multiplier int) (SIMDUTFResult, error) {
	if err := simdutfCapacity(len(output), len(input), multiplier); err != nil {
		return SIMDUTFResult{}, err
	}
	r, err := simdutfConvert(kind, slicePointer(input), len(input), slicePointer(output), len(output))
	runtime.KeepAlive(input)
	runtime.KeepAlive(output)
	return r, err
}

func SIMDUTFConvertUTF8ToUTF16LE(input []byte, output []uint16) (int, error) {
	r, e := simdutfConvertSlices(0, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF8ToUTF16LEWithErrors(input []byte, output []uint16) (SIMDUTFResult, error) {
	return simdutfConvertSlices(1, input, output, 1)
}
func SIMDUTFConvertValidUTF8ToUTF16LE(input []byte, output []uint16) (int, error) {
	valid, e := SIMDUTFValidateUTF8(input)
	if e != nil {
		return 0, e
	}
	if !valid {
		return 0, errors.New("gov8: valid UTF-8 conversion received malformed input")
	}
	r, e := simdutfConvertSlices(2, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF16LEToUTF8(input []uint16, output []byte) (int, error) {
	r, e := simdutfConvertSlices(3, input, output, 3)
	return r.Count, e
}
func SIMDUTFConvertUTF16LEToUTF8WithErrors(input []uint16, output []byte) (SIMDUTFResult, error) {
	return simdutfConvertSlices(4, input, output, 3)
}
func SIMDUTFConvertValidUTF16LEToUTF8(input []uint16, output []byte) (int, error) {
	valid, e := SIMDUTFValidateUTF16LE(input)
	if e != nil {
		return 0, e
	}
	if !valid {
		return 0, errors.New("gov8: valid UTF-16LE conversion received malformed input")
	}
	r, e := simdutfConvertSlices(5, input, output, 3)
	return r.Count, e
}
func SIMDUTFConvertUTF8ToUTF16BE(input []byte, output []uint16) (int, error) {
	r, e := simdutfConvertSlices(6, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF16BEToUTF8(input []uint16, output []byte) (int, error) {
	r, e := simdutfConvertSlices(7, input, output, 3)
	return r.Count, e
}
func SIMDUTFConvertUTF8ToLatin1(input, output []byte) (int, error) {
	r, e := simdutfConvertSlices(8, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF8ToLatin1WithErrors(input, output []byte) (SIMDUTFResult, error) {
	return simdutfConvertSlices(9, input, output, 1)
}
func SIMDUTFConvertValidUTF8ToLatin1(input, output []byte) (int, error) {
	remaining := input
	for len(remaining) > 0 {
		value, size := utf8.DecodeRune(remaining)
		if value == utf8.RuneError && size == 1 || value > 0xff {
			return 0, errors.New("gov8: valid Latin-1 conversion received unsupported UTF-8 input")
		}
		remaining = remaining[size:]
	}
	r, e := simdutfConvertSlices(10, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertLatin1ToUTF8(input, output []byte) (int, error) {
	r, e := simdutfConvertSlices(11, input, output, 2)
	return r.Count, e
}
func SIMDUTFConvertLatin1ToUTF16LE(input []byte, output []uint16) (int, error) {
	r, e := simdutfConvertSlices(12, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF16LEToLatin1(input []uint16, output []byte) (int, error) {
	for _, v := range input {
		if v > 0xff {
			return 0, errors.New("gov8: Latin-1 conversion received an out-of-range UTF-16 unit")
		}
	}
	r, e := simdutfConvertSlices(13, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF8ToUTF32(input []byte, output []uint32) (int, error) {
	r, e := simdutfConvertSlices(14, input, output, 1)
	return r.Count, e
}
func SIMDUTFConvertUTF32ToUTF8(input []uint32, output []byte) (int, error) {
	r, e := simdutfConvertSlices(15, input, output, 4)
	return r.Count, e
}

func simdutfMeasure(kind int32, input uintptr, length int) (int, error) {
	var out uintptr
	r1, _, _ := proc("gov8_simdutf_measure").Call(uintptr(kind), input, uintptr(length), uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return 0, shimError("SIMDUTF.Measure", r1)
	}
	return int(out), nil
}
func simdutfMeasureSlice[T any](kind int32, input []T) (int, error) {
	n, e := simdutfMeasure(kind, slicePointer(input), len(input))
	runtime.KeepAlive(input)
	return n, e
}
func SIMDUTFUTF8LengthFromUTF16LE(v []uint16) (int, error)  { return simdutfMeasureSlice(0, v) }
func SIMDUTFUTF8LengthFromUTF16BE(v []uint16) (int, error)  { return simdutfMeasureSlice(1, v) }
func SIMDUTFUTF16LengthFromUTF8(v []byte) (int, error)      { return simdutfMeasureSlice(2, v) }
func SIMDUTFUTF8LengthFromLatin1(v []byte) (int, error)     { return simdutfMeasureSlice(3, v) }
func SIMDUTFLatin1LengthFromUTF8(v []byte) (int, error)     { return simdutfMeasureSlice(4, v) }
func SIMDUTFUTF32LengthFromUTF8(v []byte) (int, error)      { return simdutfMeasureSlice(5, v) }
func SIMDUTFUTF8LengthFromUTF32(v []uint32) (int, error)    { return simdutfMeasureSlice(6, v) }
func SIMDUTFUTF16LengthFromUTF32(v []uint32) (int, error)   { return simdutfMeasureSlice(7, v) }
func SIMDUTFUTF32LengthFromUTF16LE(v []uint16) (int, error) { return simdutfMeasureSlice(8, v) }
func SIMDUTFCountUTF8(v []byte) (int, error)                { return simdutfMeasureSlice(9, v) }
func SIMDUTFCountUTF16LE(v []uint16) (int, error)           { return simdutfMeasureSlice(10, v) }
func SIMDUTFCountUTF16BE(v []uint16) (int, error)           { return simdutfMeasureSlice(11, v) }
func SIMDUTFDetectEncodings(v []byte) (SIMDUTFEncoding, error) {
	n, e := simdutfMeasureSlice(12, v)
	return SIMDUTFEncoding(n), e
}

func validBase64Options(options SIMDUTFBase64Options) bool {
	return options <= SIMDUTFBase64URLWithPadding
}
func validLastChunk(last SIMDUTFLastChunkHandling) bool {
	return last <= SIMDUTFLastChunkOnlyFullChunks
}
func simdutfBase64(kind int32, input, output []byte, logicalLength int, options SIMDUTFBase64Options, last SIMDUTFLastChunkHandling) (SIMDUTFResult, error) {
	if !validBase64Options(options) || !validLastChunk(last) {
		return SIMDUTFResult{}, errors.New("gov8: invalid simdutf base64 option")
	}
	var code int32
	var count uintptr
	r1, _, _ := proc("gov8_simdutf_base64").Call(uintptr(kind), slicePointer(input), uintptr(logicalLength), slicePointer(output), uintptr(len(output)), uintptr(options), uintptr(last), uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&count)))
	runtime.KeepAlive(input)
	runtime.KeepAlive(output)
	if int64(r1) < 0 {
		return SIMDUTFResult{}, shimError("SIMDUTF.Base64", r1)
	}
	return SIMDUTFResult{Error: simdutfErrorCode(code), Count: int(count)}, nil
}
func SIMDUTFMaximalBinaryLengthFromBase64(input []byte) (int, error) {
	r, e := simdutfBase64(0, input, nil, len(input), SIMDUTFBase64Default, SIMDUTFLastChunkLoose)
	return r.Count, e
}
func SIMDUTFBase64ToBinary(input, output []byte, options SIMDUTFBase64Options, last SIMDUTFLastChunkHandling) (SIMDUTFResult, error) {
	required, e := SIMDUTFMaximalBinaryLengthFromBase64(input)
	if e != nil {
		return SIMDUTFResult{}, e
	}
	if len(output) < required {
		return SIMDUTFResult{}, fmt.Errorf("gov8: simdutf destination capacity %d, need %d", len(output), required)
	}
	return simdutfBase64(1, input, output, len(input), options, last)
}

// SIMDUTFBase64LengthFromBinary accepts uint64 so Windows amd64 callers can
// observe the full size_t boundary behavior characterized by the Rust oracle.
func SIMDUTFBase64LengthFromBinary(length uint64, options SIMDUTFBase64Options) (uint64, error) {
	if !validBase64Options(options) {
		return 0, errors.New("gov8: invalid simdutf base64 option")
	}
	var code int32
	var count uintptr
	r1, _, _ := proc("gov8_simdutf_base64").Call(2, 0, uintptr(length), 0, 0, uintptr(options),
		uintptr(SIMDUTFLastChunkLoose), uintptr(unsafe.Pointer(&code)), uintptr(unsafe.Pointer(&count)))
	if int64(r1) < 0 {
		return 0, shimError("SIMDUTF.Base64LengthFromBinary", r1)
	}
	return uint64(count), nil
}
func SIMDUTFBinaryToBase64(input, output []byte, options SIMDUTFBase64Options) (int, error) {
	required64, e := SIMDUTFBase64LengthFromBinary(uint64(len(input)), options)
	if e != nil {
		return 0, e
	}
	if required64 > uint64(int(^uint(0)>>1)) {
		return 0, errors.New("gov8: simdutf base64 output size overflows int")
	}
	required := int(required64)
	if len(output) < required {
		return 0, fmt.Errorf("gov8: simdutf destination capacity %d, need %d", len(output), required)
	}
	r, e := simdutfBase64(3, input, output, len(input), options, SIMDUTFLastChunkLoose)
	return r.Count, e
}
