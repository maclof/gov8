//go:build windows && amd64

package gov8_test

import (
	"strings"
	"testing"

	gov8 "github.com/maclof/gov8"
)

// Safe negative characterization for the object-ops slice, mirroring the
// contract documented in rust-oracle/tests/object_ops_negative.rs: the
// pinned Rust crate performs no runtime receiver checks and a confounded
// Local<Object> (one actually wrapping a Number) deterministically kills
// the process with STATUS_ACCESS_VIOLATION. The Go port adds receiver-type
// verification for the whole family instead: every probe below must return
// an error (never crash), leave no pending exception behind, and leave the
// isolate fully usable. These tests are safe to run in-process by design —
// that IS the contract under test.

func newNegativeEnv(t *testing.T) *objEnv {
	t.Helper()
	return newObjectEnv(t)
}

// confoundedObject returns an *Object wrapper whose engine value is a
// Number — exactly the misuse the oracle characterizes as fatal.
func confoundedObject(e *objEnv) *gov8.Object {
	return &gov8.Object{Value: e.mustInt(7)}
}

func TestResidualDataValueSafety(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()
	value := e.mustString("1")

	if _, err := value.ToNumber(nil, e.ctx, nil); err == nil || !strings.Contains(err.Error(), "nil scope") {
		t.Fatalf("ToNumber nil scope = %v", err)
	}
	if _, err := e.ctx.Data(nil); err == nil || !strings.Contains(err.Error(), "nil scope") {
		t.Fatalf("Context.Data nil scope = %v", err)
	}
	if _, err := (gov8.Data{}).IsString(); err == nil || !strings.Contains(err.Error(), "zero data") {
		t.Fatalf("zero Data predicate = %v", err)
	}

	data, err := value.Data()
	if err != nil {
		t.Fatal(err)
	}
	other := newNegativeEnv(t)
	defer other.close()
	foreign, err := other.mustString("1").Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.Equal(foreign); err == nil || !strings.Contains(err.Error(), "different isolate") {
		t.Fatalf("foreign Data equality = %v", err)
	}

	threadResult := make(chan error, 1)
	go func() {
		_, err := data.IsString()
		threadResult <- err
	}()
	if err := <-threadResult; err == nil || !strings.Contains(err.Error(), "thread affinity") {
		t.Fatalf("wrong-thread Data predicate = %v", err)
	}

	closed, err := e.iso.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	closedValue, err := closed.NewString("closed")
	if err != nil {
		t.Fatal(err)
	}
	closedData, err := closedValue.Data()
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closedData.IsString(); err == nil || !strings.Contains(err.Error(), "scope used after Close") {
		t.Fatalf("closed Data predicate = %v", err)
	}
}

func TestNegativeConfoundedReceiverWholeFamily(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()

	bad := confoundedObject(e)
	undef, err := e.scope.Undefined()
	if err != nil {
		t.Fatalf("Undefined: %v", err)
	}
	good := e.mustObject()
	key := e.mustString("k")

	probes := []struct {
		name string
		fn   func() error
	}{
		{"GetIdentityHash", func() error { _, err := bad.GetIdentityHash(); return err }},
		{"IsCallable", func() error { _, err := bad.IsCallable(); return err }},
		{"IsConstructor", func() error { _, err := bad.IsConstructor(); return err }},
		{"GetPrototype", func() error { _, err := bad.GetPrototype(e.scope); return err }},
		{"GetConstructorName", func() error { _, err := bad.GetConstructorName(e.scope); return err }},
		{"CreationContextIs", func() error { _, err := bad.CreationContextIs(e.scope, e.ctx); return err }},
		{"SetPrototype", func() error { _, err := bad.SetPrototype(e.scope, e.ctx, undef); return err }},
		{"Has", func() error { _, err := bad.Has(e.scope, e.ctx, key, nil); return err }},
		{"HasIndex", func() error { _, err := bad.HasIndex(e.scope, e.ctx, 0, nil); return err }},
		{"HasOwnProperty", func() error { _, err := bad.HasOwnProperty(e.scope, e.ctx, key, nil); return err }},
		{"Delete", func() error { _, err := bad.Delete(e.scope, e.ctx, key, nil); return err }},
		{"DeleteIndex", func() error { _, err := bad.DeleteIndex(e.scope, e.ctx, 0, nil); return err }},
		{"GetRealNamedProperty", func() error { _, _, err := bad.GetRealNamedProperty(e.scope, e.ctx, key, nil); return err }},
		{"HasRealNamedProperty", func() error { _, err := bad.HasRealNamedProperty(e.scope, e.ctx, key); return err }},
		{"GetRealNamedPropertyAttributes", func() error { _, _, err := bad.GetRealNamedPropertyAttributes(e.scope, e.ctx, key); return err }},
		{"GetWithReceiver", func() error { _, err := bad.GetWithReceiver(e.scope, e.ctx, key, good); return err }},
		{"SetWithReceiver", func() error { _, err := bad.SetWithReceiver(e.scope, e.ctx, key, e.mustInt(1), good); return err }},
		{"SetLazyDataProperty", func() error {
			_, err := bad.SetLazyDataProperty(e.scope, e.ctx, key,
				func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {})
			return err
		}},
		{"CallAsFunction", func() error { _, err := bad.CallAsFunction(e.scope, e.ctx, undef, nil, nil); return err }},
		{"CallAsConstructor", func() error { _, err := bad.CallAsConstructor(e.scope, e.ctx, nil, nil); return err }},
	}
	for _, p := range probes {
		if err := p.fn(); err == nil {
			t.Errorf("%s on a confounded receiver must return an error", p.name)
		}
		// No probe may leave a pending exception behind.
		tc, err := e.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		if caught, err := tc.HasCaught(); err != nil || caught {
			t.Errorf("%s left a caught exception behind: caught=%v err=%v", p.name, caught, err)
		}
		if err := tc.Close(); err != nil {
			t.Errorf("TryCatch.Close: %v", err)
		}
	}
	// The isolate is fully usable afterwards (the crash the Rust oracle
	// documents cannot happen on the Go path).
	if n := e.evalInt("40 + 2"); n != 42 {
		t.Fatalf("isolate unusable after rejected misuse: %v", n)
	}
}

func TestNegativeConfoundedReceiverArgPositions(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()

	bad := confoundedObject(e)
	good := e.mustObject()
	key := e.mustString("k")

	// The confounded object as the RECEIVER argument of a well-typed call.
	if _, err := good.GetWithReceiver(e.scope, e.ctx, key, bad); err == nil {
		t.Fatal("confounded receiver argument must be refused")
	}
	if _, err := good.SetWithReceiver(e.scope, e.ctx, key, e.mustInt(1), bad); err == nil {
		t.Fatal("confounded receiver argument (set) must be refused")
	}
	// ...and as the right-hand side of instanceof.
	if _, err := good.Value.InstanceOf(e.scope, e.ctx, bad, nil); err == nil {
		t.Fatal("confounded instanceof RHS must be refused")
	}
	if n := e.evalInt("1"); n != 1 {
		t.Fatalf("isolate unusable: %v", n)
	}
}

func TestNegativeAccessorAndLazyRegistrationRules(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()

	obj := e.mustObject()
	key := e.mustString("x")
	// An accessor with neither side is refused before any engine work.
	if _, err := obj.SetAccessor(e.scope, e.ctx, key, nil, nil); err == nil {
		t.Fatal("SetAccessor without getter/setter must fail")
	}
	// A lazy property without a getter is refused.
	if _, err := obj.SetLazyDataProperty(e.scope, e.ctx, key, nil); err == nil {
		t.Fatal("SetLazyDataProperty without a getter must fail")
	}
	// Non-Name keys are refused by the wrapper.
	numberKey := e.mustInt(1)
	if _, err := obj.SetAccessor(e.scope, e.ctx, numberKey,
		func(cs *gov8.CallbackScope, args gov8.PropertyCallbackArguments, rv gov8.ReturnValue) {},
		nil); err == nil {
		t.Fatal("SetAccessor with a non-Name key must fail")
	}
	if n := e.evalInt("2"); n != 2 {
		t.Fatalf("isolate unusable: %v", n)
	}
}

func TestNegativeConversionAndEqualityMisuse(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()

	// ToObject of undefined/null throws the pinned TypeError.
	for _, expr := range []string{"undefined", "null"} {
		tc, err := e.iso.NewTryCatch()
		if err != nil {
			t.Fatalf("NewTryCatch: %v", err)
		}
		v := e.evalValue(expr)
		if _, err := v.ToObject(e.scope, e.ctx, tc); err == nil {
			t.Fatalf("ToObject(%s) must fail", expr)
		}
		if caught, _ := tc.HasCaught(); !caught {
			t.Fatalf("ToObject(%s) must be caught", expr)
		}
		msg, err := tc.MessageText(e.scope, e.ctx)
		if err != nil {
			t.Fatalf("MessageText: %v", err)
		}
		if !strings.Contains(msg, "TypeError") {
			t.Fatalf("ToObject(%s) message: %q", expr, msg)
		}
		if err := tc.Close(); err != nil {
			t.Errorf("TryCatch.Close: %v", err)
		}
	}

	// ToInteger of a BigInt throws.
	bigInt := func() gov8.Value {
		v, err := e.scope.BigIntFromInt64(1)
		if err != nil {
			t.Fatalf("BigIntFromInt64: %v", err)
		}
		return v
	}
	tc, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, err := bigInt().ToInteger(e.scope, e.ctx, tc); err == nil {
		t.Fatal("ToInteger(BigInt) must fail")
	}
	if caught, _ := tc.HasCaught(); !caught {
		t.Fatal("ToInteger(BigInt) must be caught")
	}
	if err := tc.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}

	// ToBigInt of a number throws the pinned message.
	nv, err := e.scope.Number(42)
	if err != nil {
		t.Fatalf("Number: %v", err)
	}
	tc2, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, err := nv.ToBigInt(e.scope, e.ctx, tc2); err == nil {
		t.Fatal("ToBigInt(number) must fail")
	}
	msg, err := tc2.MessageText(e.scope, e.ctx)
	if err != nil {
		t.Fatalf("MessageText: %v", err)
	}
	if !strings.Contains(msg, "Cannot convert 42 to a BigInt") {
		t.Fatalf("ToBigInt message: %q", msg)
	}
	if err := tc2.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}

	// instanceof against a non-callable RHS throws the pinned message.
	plain := e.mustObject()
	rhs := e.mustObject()
	tc3, err := e.iso.NewTryCatch()
	if err != nil {
		t.Fatalf("NewTryCatch: %v", err)
	}
	if _, err := plain.Value.InstanceOf(e.scope, e.ctx, rhs, tc3); err == nil {
		t.Fatal("InstanceOf(non-callable) must fail")
	}
	msg, err = tc3.MessageText(e.scope, e.ctx)
	if err != nil {
		t.Fatalf("MessageText: %v", err)
	}
	if !strings.Contains(msg, "Right-hand side of 'instanceof' is not callable") {
		t.Fatalf("instanceof message: %q", msg)
	}
	if err := tc3.Close(); err != nil {
		t.Errorf("TryCatch.Close: %v", err)
	}

	// Cross-isolate SameValueZero is a wrapper error, not an engine call.
	iso2, err := gov8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer func() { _ = iso2.Close() }()
	scope2, err := iso2.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	defer func() { _ = scope2.Close() }()
	foreign, err := scope2.Int32(0)
	if err != nil {
		t.Fatalf("Int32: %v", err)
	}
	if _, err := nv.SameValueZero(scope2, foreign); err == nil {
		t.Fatal("SameValueZero across isolates must fail")
	}

	if n := e.evalInt("3"); n != 3 {
		t.Fatalf("isolate unusable: %v", n)
	}
}

func TestNegativeZeroHandleAndClosedContext(t *testing.T) {
	e := newNegativeEnv(t)
	defer e.close()

	obj := e.mustObject()

	// A zero-value key is refused without an engine call.
	if _, err := obj.Has(e.scope, e.ctx, gov8.Value{}, nil); err == nil {
		t.Fatal("Has with a zero key must fail")
	}

	// After the object's creating SCOPE closes, its wire is invalid and
	// receiver operations fail cleanly (a closed creating CONTEXT does not
	// invalidate the wire while the isolate lives, so that is not probed).
	scope2, err := e.iso.NewScope()
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	obj3, err := scope2.NewObject(e.ctx)
	if err != nil {
		t.Fatalf("NewObject: %v", err)
	}
	if err := scope2.Close(); err != nil {
		t.Fatalf("scope2.Close: %v", err)
	}
	if _, err := obj3.GetIdentityHash(); err == nil {
		t.Fatal("GetIdentityHash after creating-scope close must fail")
	}
	if n := e.evalInt("3"); n != 3 {
		t.Fatalf("isolate unusable: %v", n)
	}
}
