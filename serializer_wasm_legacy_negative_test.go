//go:build windows && amd64

package gov8_test

import (
	"os"
	"testing"

	gov8 "gov8"
)

type swlPanicWriter struct{}

func (swlPanicWriter) ThrowDataCloneError(string) bool { return true }
func (swlPanicWriter) GetWasmModuleTransferID(gov8.Value) (uint32, bool) {
	panic("wasm transfer-id writer panic boundary")
}

func TestProbeSerializerWasmWriterPanic(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerializerWasmWriterPanic") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	module, err := ctx.CompileWasmModule(scope, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	serializer, err := gov8.NewDelegateValueSerializer(scope, ctx, swlPanicWriter{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serializer.Close() }()
	_, _ = os.Stderr.WriteString("marker:wasm-writer\n")
	_, _ = serializer.WriteValue(ctx, module.Value, nil)
	_, _ = os.Stderr.WriteString("marker:wasm-writer:after\n")
}

func TestSerializerWasmWriterPanicAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerializerWasmWriterPanic", "wasm-writer")
}

type swlPanicReader struct{}

func (swlPanicReader) ResolveWasmModuleFromID(*gov8.DelegateValueDeserializer, uint32) (*gov8.WasmModuleObject, bool) {
	panic("wasm resolver panic boundary")
}

func TestProbeSerializerWasmReaderPanic(t *testing.T) {
	if !serDelProbeBody(t, "TestProbeSerializerWasmReaderPanic") {
		t.Skip("probe body")
	}
	_, ctx, scope := newTestRuntime(t)
	deserializer, err := gov8.NewDelegateValueDeserializer(scope, ctx, []byte{0x77, 0x01}, swlPanicReader{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deserializer.Close() }()
	_, _ = os.Stderr.WriteString("marker:wasm-reader\n")
	_, _ = deserializer.ReadValue(ctx, nil)
	_, _ = os.Stderr.WriteString("marker:wasm-reader:after\n")
}

func TestSerializerWasmReaderPanicAborts(t *testing.T) {
	runSerDelProbe(t, "TestProbeSerializerWasmReaderPanic", "wasm-reader")
}
