//go:build windows && amd64

package gov8_test

import (
	"bytes"
	"strings"
	"testing"

	gov8 "gov8"
)

func baseHeaderWire(t *testing.T, ctx *gov8.Context, scope *gov8.Scope, header bool) []byte {
	t.Helper()
	value, ok := evalValue(t, ctx, scope, nil, "true")
	if !ok {
		t.Fatal("evaluate true failed")
	}
	wire, written, _ := serializeValue(t, ctx, scope, nil, value, header)
	if !written {
		t.Fatal("base serializer did not write true")
	}
	return wire
}

func TestBaseValueDeserializerReadHeaderCurrentWire(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	wire := baseHeaderWire(t, ctx, scope, true)
	if want := []byte{0xff, 0x10, 0x54}; !bytes.Equal(wire, want) {
		t.Fatalf("current true wire = %x, want %x", wire, want)
	}
	vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vd.Close() }()
	ok, err := vd.ReadHeader(ctx)
	if err != nil || !ok {
		t.Fatalf("ReadHeader = %v, %v", ok, err)
	}
	value, err := vd.ReadValue(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.BooleanValue(); err != nil || !got {
		t.Fatalf("roundtrip boolean = %v, %v", got, err)
	}
}

func TestBaseValueDeserializerReadHeaderRejectsMalformedAndLegacy(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	headerless := baseHeaderWire(t, ctx, scope, false)
	if !bytes.Equal(headerless, []byte{0x54}) {
		t.Fatalf("headerless true wire = %x", headerless)
	}
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{"empty", nil},
		{"truncated_version", []byte{0xff}},
		{"headerless_legacy", headerless},
	} {
		vd, err := gov8.NewValueDeserializer(scope, ctx, tc.wire)
		if err != nil {
			t.Fatalf("%s NewValueDeserializer: %v", tc.name, err)
		}
		ok, headerErr := vd.ReadHeader(ctx)
		if ok || !gov8.IsException(headerErr) {
			t.Fatalf("%s ReadHeader = %v, %v; want false, exception", tc.name, ok, headerErr)
		}
		if err := vd.Close(); err != nil {
			t.Fatalf("%s Close: %v", tc.name, err)
		}
	}

	// A failed parser is isolated to that deserializer: destroying it and
	// opening a current-wire reader on the same isolate must still succeed.
	current := baseHeaderWire(t, ctx, scope, true)
	vd, err := gov8.NewValueDeserializer(scope, ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := vd.ReadHeader(ctx); err != nil || !ok {
		t.Fatalf("recovery ReadHeader = %v, %v", ok, err)
	}
	if err := vd.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBaseValueDeserializerReadHeaderLifecycleAndThread(t *testing.T) {
	_, ctx, scope := newTestRuntime(t)
	wire := baseHeaderWire(t, ctx, scope, true)
	vd, err := gov8.NewValueDeserializer(scope, ctx, wire)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 1)
	go func() {
		_, err := vd.ReadHeader(ctx)
		errs <- err
	}()
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread ReadHeader error = %v", err)
	}
	if err := vd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := vd.ReadHeader(ctx); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("closed ReadHeader error = %v", err)
	}
}
