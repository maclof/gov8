//go:build windows && amd64

package gov8

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"
)

// takeCRDTPBytesLegacyBenchmark preserves the pre-fusion three-boundary path
// solely so the byte-copy benchmarks quantify the boundary change directly.
func takeCRDTPBytesLegacyBenchmark(handle uintptr) (result []byte, err error) {
	defer func() {
		if closeErr := callErr("legacy CRDTP bytes close", proc("gov8_crdtp_bytes_delete"), handle); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	length, _, _ := proc("gov8_crdtp_bytes_copy").Call(handle, 0, 0)
	if int64(length) < 0 {
		return nil, shimError("legacy CRDTP bytes length", length)
	}
	result = make([]byte, int(length))
	status, _, _ := proc("gov8_crdtp_bytes_copy").Call(handle, slicePointer(result), uintptr(len(result)))
	runtime.KeepAlive(result)
	if int64(status) < 0 {
		return nil, shimError("legacy CRDTP bytes copy", status)
	}
	return result, nil
}

func benchmarkCRDTPSerializableBytesLegacy(serializable *CRDTPSerializable) ([]byte, error) {
	serializable.mu.Lock()
	defer serializable.mu.Unlock()
	handle, err := serializable.withHandle()
	if err != nil {
		return nil, err
	}
	var out uintptr
	status, _, _ := proc("gov8_crdtp_serializable_bytes").Call(handle, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(&out)
	if int64(status) < 0 {
		return nil, shimError("legacy CRDTP Serializable bytes", status)
	}
	return takeCRDTPBytesLegacyBenchmark(out)
}

func benchmarkCRDTPCBORToJSONLegacy(input []byte) ([]byte, bool, error) {
	var out uintptr
	status, _, _ := proc("gov8_crdtp_cbor_to_json").Call(
		slicePointer(input), uintptr(len(input)), uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(input)
	runtime.KeepAlive(&out)
	if int64(status) < 0 {
		return nil, false, shimError("legacy CRDTP CBOR-to-JSON", status)
	}
	if status == 0 {
		return nil, false, nil
	}
	result, err := takeCRDTPBytesLegacyBenchmark(out)
	return result, err == nil, err
}

func BenchmarkCRDTPSerializableBytesBoundary(b *testing.B) {
	serializable, err := CreateCRDTPResponse(1, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = serializable.Close() })

	b.Run("LegacyRepeated", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := benchmarkCRDTPSerializableBytesLegacy(serializable); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CachedViewRepeated", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := serializable.Bytes(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("LegacyFirstUse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, err := CreateCRDTPResponse(1, nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := benchmarkCRDTPSerializableBytesLegacy(value); err != nil {
				b.Fatal(err)
			}
			if err := value.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ViewFirstUse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, err := CreateCRDTPResponse(1, nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := value.Bytes(); err != nil {
				b.Fatal(err)
			}
			if err := value.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCRDTPCBORToJSONBoundary(b *testing.B) {
	cbor, ok, err := CRDTPJSONToCBOR([]byte(`{"id":1,"result":{}}`))
	if err != nil || !ok {
		b.Fatalf("request conversion: ok=%v err=%v", ok, err)
	}

	b.Run("Legacy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, ok, err := benchmarkCRDTPCBORToJSONLegacy(cbor); err != nil || !ok {
				b.Fatalf("legacy conversion: ok=%v err=%v", ok, err)
			}
		}
	})
	b.Run("Fused", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, ok, err := CRDTPCBORToJSON(cbor); err != nil || !ok {
				b.Fatalf("fused conversion: ok=%v err=%v", ok, err)
			}
		}
	})
}
