//go:build windows && amd64

package gov8_test

import (
	"runtime"
	"strings"
	"testing"

	gov8 "gov8"
)

type swlNoneResolver struct{}

func (swlNoneResolver) ResolveWasmModuleFromID(*gov8.DelegateValueDeserializer, uint32) (*gov8.WasmModuleObject, bool) {
	return nil, false
}

type swlFixedResolver struct{ module *gov8.WasmModuleObject }

func (r swlFixedResolver) ResolveWasmModuleFromID(*gov8.DelegateValueDeserializer, uint32) (*gov8.WasmModuleObject, bool) {
	return r.module, true
}

func TestSerializerLegacyConfigurationOrdering(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)

	base, err := gov8.NewValueDeserializer(scope, ctx, []byte{0x54})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.SetSupportsLegacyWireFormat(true); err != nil {
		t.Fatal(err)
	}
	if ok, err := base.ReadHeader(ctx); err != nil || !ok {
		t.Fatalf("base legacy header = %v, %v", ok, err)
	}
	if version, err := base.GetWireFormatVersion(); err != nil || version != 0 {
		t.Fatalf("base version = %d, %v", version, err)
	}
	if err := base.SetSupportsLegacyWireFormat(false); err == nil || !strings.Contains(err.Error(), "before reading") {
		t.Fatalf("late base setter = %v", err)
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := base.GetWireFormatVersion(); err == nil {
		t.Fatal("base GetWireFormatVersion after Close succeeded")
	}

	delegated, err := gov8.NewDelegateValueDeserializer(scope, ctx, []byte{0x54}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := delegated.SetSupportsLegacyWireFormat(false); err != nil {
		t.Fatal(err)
	}
	if err := delegated.SetSupportsLegacyWireFormat(true); err != nil {
		t.Fatal(err)
	}
	if ok, err := delegated.ReadHeader(ctx, nil); err != nil || !ok {
		t.Fatalf("delegate legacy header = %v, %v", ok, err)
	}
	if err := delegated.SetSupportsLegacyWireFormat(true); err == nil || !strings.Contains(err.Error(), "before reading") {
		t.Fatalf("late delegate setter = %v", err)
	}
	if err := delegated.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSerializerLegacyReadValueAlsoSealsConfiguration(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	for _, delegated := range []bool{false, true} {
		if delegated {
			d, err := gov8.NewDelegateValueDeserializer(scope, ctx, []byte{0x54}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.ReadValue(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if err := d.SetSupportsLegacyWireFormat(true); err == nil {
				t.Fatal("delegate late setter after direct ReadValue succeeded")
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		d, err := gov8.NewValueDeserializer(scope, ctx, []byte{0x54})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.ReadValue(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := d.SetSupportsLegacyWireFormat(true); err == nil {
			t.Fatal("base late setter after direct ReadValue succeeded")
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSerializerLegacyWrongThread(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	d, err := gov8.NewValueDeserializer(scope, ctx, []byte{0x54})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		result <- d.SetSupportsLegacyWireFormat(true)
	}()
	if err := <-result; err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("wrong-thread setter = %v", err)
	}
}

func TestSerializerLegacyLocalValidationRemainsRetryable(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	_, foreignContext, _ := newTestRuntime(t)
	d, err := gov8.NewValueDeserializer(scope, ctx, []byte{0x54})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if _, err := d.ReadHeader(foreignContext); err == nil {
		t.Fatal("foreign-context ReadHeader succeeded")
	}
	if err := d.SetSupportsLegacyWireFormat(true); err != nil {
		t.Fatalf("failed local validation sealed configuration: %v", err)
	}
}

func swlReadFailure(t *testing.T, iso *gov8.Isolate, ctx *gov8.Context, scope *gov8.Scope, resolver gov8.ValueDeserializerDelegate) string {
	t.Helper()
	tc, err := iso.NewTryCatch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tc.Close() }()
	d, err := gov8.NewDelegateValueDeserializer(scope, ctx, []byte{0x77, 0x01}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if _, err := d.ReadValue(ctx, tc); err == nil {
		t.Fatal("invalid Wasm resolver unexpectedly succeeded")
	}
	text, err := tc.ExceptionText(scope, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestWasmResolverRejectsWrongTypeAndScope(t *testing.T) {
	iso, ctx, scope := newTestRuntime(t)
	number, err := scope.Number(1)
	if err != nil {
		t.Fatal(err)
	}
	wrongType := &gov8.WasmModuleObject{Value: number}
	if text := swlReadFailure(t, iso, ctx, scope, swlFixedResolver{wrongType}); !strings.Contains(text, "not a WasmModuleObject") {
		t.Fatalf("wrong-type exception = %q", text)
	}

	otherScope, err := iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	module, err := ctx.CompileWasmModule(otherScope, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := swlReadFailure(t, iso, ctx, scope, swlFixedResolver{module}); !strings.Contains(text, "callback scope") {
		t.Fatalf("wrong-scope exception = %q", text)
	}
	if err := otherScope.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWasmResolverRejectsForeignIsolate(t *testing.T) {
	isoA, ctxA, scopeA := newTestRuntime(t)
	_, ctxB, scopeB := newTestRuntime(t)
	module, err := ctxB.CompileWasmModule(scopeB, emptyWasmModule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := swlReadFailure(t, isoA, ctxA, scopeA, swlFixedResolver{module}); !strings.Contains(text, "callback scope") {
		t.Fatalf("foreign-isolate exception = %q", text)
	}
}

type swlBenchWriter struct{}

func (swlBenchWriter) ThrowDataCloneError(string) bool { return true }
func (swlBenchWriter) GetWasmModuleTransferID(gov8.Value) (uint32, bool) {
	return 1, true
}

type swlBenchReader struct{ compiled *gov8.CompiledWasmModule }

func (r swlBenchReader) ResolveWasmModuleFromID(d *gov8.DelegateValueDeserializer, id uint32) (*gov8.WasmModuleObject, bool) {
	if id != 1 {
		return nil, false
	}
	module, err := d.Context().WasmModuleFromCompiled(d.Scope(), r.compiled)
	if err != nil {
		panic(err)
	}
	return module, true
}

func BenchmarkSerializerWasmRoundTrip(b *testing.B) {
	iso, err := gov8.NewIsolate()
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		b.Fatal(err)
	}
	outer, err := iso.NewScope()
	if err != nil {
		b.Fatal(err)
	}
	module, err := ctx.CompileWasmModule(outer, answerWasmModule, nil)
	if err != nil {
		b.Fatal(err)
	}
	compiled, err := module.CompiledModule()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = compiled.Close()
		_ = outer.Close()
		_ = ctx.Close()
		_ = iso.Close()
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scope, err := iso.NewScope()
		if err != nil {
			b.Fatal(err)
		}
		serializer, err := gov8.NewDelegateValueSerializer(scope, ctx, swlBenchWriter{})
		if err != nil {
			b.Fatal(err)
		}
		ok, err := serializer.WriteValue(ctx, module.Value, nil)
		if err != nil || !ok {
			b.Fatalf("write = %v, %v", ok, err)
		}
		wire, err := serializer.Release()
		if err != nil {
			b.Fatal(err)
		}
		if err := serializer.Close(); err != nil {
			b.Fatal(err)
		}
		deserializer, err := gov8.NewDelegateValueDeserializer(scope, ctx, wire, swlBenchReader{compiled})
		if err != nil {
			b.Fatal(err)
		}
		value, err := deserializer.ReadValue(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if ok, err := value.IsWasmModuleObject(); err != nil || !ok {
			b.Fatalf("module predicate = %v, %v", ok, err)
		}
		if err := deserializer.Close(); err != nil {
			b.Fatal(err)
		}
		if err := scope.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
