//go:build windows && amd64

package gov8

import (
	"unsafe"
)

// Object operations and value conversions, pinned to the Rust oracle's
// conformance-object-ops slice (rust-oracle/src/bin/conformance-object-ops.rs,
// crate v8 =152.2.0, V8 15.2.124.1-rusty, x86_64-pc-windows-msvc):
//
//   - Prototype: GetPrototype / SetPrototype (null prototype, cyclic
//     rejection).
//   - Has/delete family: Has (arbitrary key values), HasIndex,
//     HasOwnProperty, Delete, DeleteIndex — each preserves the engine's
//     Maybe<bool> shape (false + error only when the engine produced an
//     empty maybe).
//   - Real-named queries: GetRealNamedProperty (a plain MISS is not an
//     error), HasRealNamedProperty, GetRealNamedPropertyAttributes (a
//     missing property is present=false, NOT an error — the engine reports
//     Nothing there, unlike GetPropertyAttributes which reports Just(NONE)).
//   - Identity: GetIdentityHash, CreationContextIs, GetConstructorName.
//   - Receivers: GetWithReceiver / SetWithReceiver.
//   - Instance-level accessors: SetAccessor (getter/setter pair) and
//     SetLazyDataProperty (getter fires once, then the property is an
//     ordinary data property).
//   - Call-as-function/constructor on Object, including the plain-object
//     TypeError path.
//   - Conversions: ToObject, ToBoolean, ToInteger, ToBigInt, ToDetailString;
//     InstanceOf; SameValueZero.
//   - The missing Value.Is* predicates no other slice pins (12 of them).
//
// # Receiver runtime checks
//
// The pinned crate performs NO runtime type checks on Object receivers: a
// confounded Local<Object> (one that actually wraps a Number) is undefined
// behavior and, on this platform/build, deterministically terminates the
// process with STATUS_ACCESS_VIOLATION rather than a catchable error
// (characterized in rust-oracle/tests/object_ops_negative.rs). Go has no
// static downcasts to lean on, so EVERY method below validates its receiver
// through the shim (which re-proves the JSReceiver kind at the ABI boundary
// and fails with a ShimError instead of entering undefined behavior), and
// every typed wrapper argument (Object receivers) is re-checked there too.
// A mis-typed receiver therefore returns an error and leaves the isolate
// fully usable — the deliberate Go-side hardening of the crash the oracle
// characterizes.
//
// # Ownership and lifetime
//
//   - Every Value produced here is scope-local: bound to the Scope passed
//     to the call (or, for the receiver-only readers, to the receiver's own
//     creating scope) and invalid once that Scope closes.
//   - Everything is thread-affine like the rest of the module: all calls
//     must run on the owning isolate thread (enforced first, before any
//     engine work).
//   - SetAccessor/SetLazyDataProperty register Go callbacks in the shared
//     registry (no Go pointer ever crosses into the engine); the shim-side
//     dispatch context is released by ReleaseIsolateHostState or on a
//     failed install.
//
// # Intentional API-shape differences from the pinned crate (semantics
// preserved)
//
//   - Option<Local<Value>> maps to (Value, error); a plain MISS that the
//     engine reports as an empty handle without an exception
//     (GetRealNamedProperty) maps to (Value{}, false, nil).
//   - Maybe<bool> maps to (bool, error): Just(b) is (b, nil); Nothing (a
//     pending exception — or, for SetPrototype, the engine's cyclic
//     rejection which schedules NO exception) is (false, non-nil). Callers
//     distinguish the two with TryCatch.HasCaught.
//   - The crate's call_as_function/call_as_function_with_context (and the
//     constructor pair) share one engine binding and differ only in which
//     context local is passed; Go's convention of passing the context
//     explicitly to every context-bound operation subsumes the distinction
//     in one method per shape.
//   - The crate's Option<Local<Context>> creation context is exposed as the
//     query CreationContextIs against a *Context instead of a raw
//     context-typed Value, so no non-Value engine object can leak into the
//     Value API.
//   - SameValueZero is implemented exactly as the pinned crate implements
//     it Rust-side (SameValue, or both sides strictly equal the zero Smi);
//     it needs a Scope only to materialize that zero.

// --- shared validation ------------------------------------------------------------

// receiverArgs validates the receiver, context and scope for an Object
// method: receiver handle + its scope/isolate lifecycle, context ownership,
// thread affinity (via the receiver's scope check), and the result scope.
// It returns the checked scope handle for the shim call.
func (o *Object) receiverArgs(s *Scope, c *Context) (uintptr, error) {
	if err := o.ctxHandle(c); err != nil {
		return 0, err
	}
	if s.iso != o.iso {
		return 0, foreignIsolate("scope")
	}
	return s.checkedHandleAssumingIsolate()
}

// keyArg validates an arbitrary key value (string, symbol, or anything the
// engine may convert) belongs to the receiver's isolate.
func (o *Object) keyArg(key Value) error {
	if err := key.check(); err != nil {
		return err
	}
	if key.iso != o.iso {
		return foreignIsolate("key")
	}
	return nil
}

// valueArg validates a plain value argument belongs to the receiver's
// isolate.
func (o *Object) valueArg(v Value, what string) error {
	if err := v.check(); err != nil {
		return err
	}
	if v.iso != o.iso {
		return foreignIsolate(what)
	}
	return nil
}

// nameWireArg validates a Name-keyed argument (string or symbol) after
// keyArg; the shim re-validates the kind at the ABI boundary.
func (o *Object) nameArg(key Value) error {
	if err := o.keyArg(key); err != nil {
		return err
	}
	is, err := key.IsName()
	if err != nil {
		return err
	}
	if !is {
		return errNotAName
	}
	return nil
}

// --- prototype --------------------------------------------------------------------

// GetPrototype returns the object's prototype. The engine always produces a
// value here: Object.prototype for fresh plain objects, and the null value
// for objects whose prototype is null (including Object.prototype itself).
// The scope must belong to the object's isolate.
func (o *Object) GetPrototype(s *Scope) (Value, error) {
	if err := o.check(); err != nil {
		return Value{}, err
	}
	if s.iso != o.iso {
		return Value{}, foreignIsolate("scope")
	}
	sh, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return Value{}, err
	}
	h, err := callHandle("Object.GetPrototype", proc("gov8_oo_object_get_prototype"),
		o.iso.handleAssumingCheck(), sh, o.h)
	if err != nil {
		return Value{}, err
	}
	return Value{iso: o.iso, sc: s, h: h}, nil
}

// SetPrototype re-points the object's prototype (v8 Object::SetPrototypeV2).
// Setting null is legal. The engine's cyclic __proto__ rejection surfaces as
// (false, err) WITHOUT a pending exception — HasCaught stays false — so
// callers must not treat the error alone as "an exception was thrown".
func (o *Object) SetPrototype(s *Scope, c *Context, proto Value) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.valueArg(proto, "prototype"); err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_set_prototype").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, proto.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.SetPrototype", r1, uintptr(okv))
}

// --- has / delete family ------------------------------------------------------------

// Has reports whether the object has the property (own or on the prototype
// chain) under an arbitrary key value: strings and symbols work directly,
// other values are converted by the engine (a plain object converts to
// "[object Object]"; an object that cannot convert throws a TypeError which
// is delivered to tc when given). A thrown conversion is reported as an
// error; tc follows the Compile/Run convention.
func (o *Object) Has(s *Scope, c *Context, key Value, tc *TryCatch) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.keyArg(key); err != nil {
		return false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_has").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.Has", r1, uintptr(okv))
}

// HasIndex reports whether the index property exists.
func (o *Object) HasIndex(s *Scope, c *Context, index uint32, tc *TryCatch) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_has_index").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, uintptr(index),
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.HasIndex", r1, uintptr(okv))
}

// HasOwnProperty reports whether key is an OWN property (the prototype
// chain is not consulted). key must be a Name (string or symbol).
func (o *Object) HasOwnProperty(s *Scope, c *Context, key Value, tc *TryCatch) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.nameArg(key); err != nil {
		return false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_has_own_property").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.HasOwnProperty", r1, uintptr(okv))
}

// Delete removes the property under an arbitrary key value. A missing key
// deletes "successfully" (true); a non-configurable or frozen property
// refuses the delete (false) without throwing in sloppy mode.
func (o *Object) Delete(s *Scope, c *Context, key Value, tc *TryCatch) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.keyArg(key); err != nil {
		return false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_delete").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.Delete", r1, uintptr(okv))
}

// DeleteIndex removes the index property (creating a hole in arrays).
func (o *Object) DeleteIndex(s *Scope, c *Context, index uint32, tc *TryCatch) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_delete_index").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, uintptr(index),
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.DeleteIndex", r1, uintptr(okv))
}

// --- real-named queries -------------------------------------------------------------

// GetRealNamedProperty reads the property under a Name key while bypassing
// named interceptors (walking the real prototype chain instead). found is
// false on a plain miss — which is NOT an error; err is non-nil only when
// the lookup threw. key must be a Name (string or symbol).
func (o *Object) GetRealNamedProperty(s *Scope, c *Context, key Value, tc *TryCatch) (val Value, found bool, err error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return Value{}, false, err
	}
	if err := o.nameArg(key); err != nil {
		return Value{}, false, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return Value{}, false, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_object_get_real_named_property").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, false, shimError("Object.GetRealNamedProperty", r1)
	}
	if out == 0 {
		return Value{}, false, nil
	}
	return Value{iso: o.iso, sc: s, h: out}, true, nil
}

// HasRealNamedProperty reports whether a real (interceptor-bypassing)
// property exists under the Name key. Note the pinned engine's own-only
// observation for this query is pinned by the conformance slice: inherited
// real properties are found by GetRealNamedProperty but report false here.
func (o *Object) HasRealNamedProperty(s *Scope, c *Context, key Value) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.nameArg(key); err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_has_real_named_property").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.HasRealNamedProperty", r1, uintptr(okv))
}

// GetRealNamedPropertyAttributes returns the PropertyAttribute bits of the
// real (interceptor-bypassing) property under the Name key. A missing
// property is (AttrNone, false, nil) — the engine's Nothing; err is
// non-nil only when the lookup threw.
func (o *Object) GetRealNamedPropertyAttributes(s *Scope, c *Context, key Value) (attr PropertyAttribute, present bool, err error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return 0, false, err
	}
	if err := o.nameArg(key); err != nil {
		return 0, false, err
	}
	tcv, err := tcArg(o.iso, nil)
	if err != nil {
		return 0, false, err
	}
	var raw, isJust int32
	r1, _, _ := proc("gov8_oo_object_get_real_named_property_attributes").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, key.h,
		uintptr(unsafe.Pointer(&isJust)), uintptr(unsafe.Pointer(&raw)))
	if int64(r1) < 0 {
		return 0, false, shimError("Object.GetRealNamedPropertyAttributes", r1)
	}
	if isJust == 0 {
		return AttrNone, false, nil
	}
	return PropertyAttribute(uint8(raw)), true, nil
}

// --- identity -----------------------------------------------------------------------

// GetIdentityHash returns the object's identity hash: never zero, stable
// for the object's lifetime, seeded per isolate (never compare hashes
// across isolates or processes). Identical to Value.GetHash of the same
// object interpreted as int32.
func (o *Object) GetIdentityHash() (int32, error) {
	if err := o.check(); err != nil {
		return 0, err
	}
	sh, err := o.sc.checkedHandleAssumingIsolate()
	if err != nil {
		return 0, err
	}
	var out int32
	r1, _, _ := proc("gov8_oo_object_identity_hash").Call(
		o.iso.handleAssumingCheck(), sh, o.h, uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return 0, shimError("Object.GetIdentityHash", r1)
	}
	return out, nil
}

// CreationContextIs reports whether the object was created in c. An object
// with no creation context at all (the engine's empty maybe) is an error,
// not a mismatch — the pinned crate maps that case to None.
func (o *Object) CreationContextIs(s *Scope, c *Context) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	var same int32
	r1, _, _ := proc("gov8_oo_object_creation_context_eq").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h,
		uintptr(unsafe.Pointer(&same)))
	if int64(r1) < 0 {
		return false, shimError("Object.CreationContextIs", r1)
	}
	return same == 1, nil
}

// GetConstructorName returns the name of the function invoked as the
// object's constructor (a scope-local string value): the literal constructor
// for instances, "Object" for plain API objects and literals, "Function" for
// function and class objects themselves, and the new.target name for
// Reflect.construct results. Read it with StringValue.
func (o *Object) GetConstructorName(s *Scope) (Value, error) {
	if err := o.check(); err != nil {
		return Value{}, err
	}
	if s.iso != o.iso {
		return Value{}, foreignIsolate("scope")
	}
	sh, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return Value{}, err
	}
	h, err := callHandle("Object.GetConstructorName", proc("gov8_oo_object_constructor_name"),
		o.iso.handleAssumingCheck(), sh, o.h)
	if err != nil {
		return Value{}, err
	}
	return Value{iso: o.iso, sc: s, h: h}, nil
}

// --- receivers ------------------------------------------------------------------------

// GetWithReceiver reads key with receiver as the lookup start and `this`
// for accessors (even when unrelated to the holder). A missing property
// reads as the undefined value; an error means the getter threw.
func (o *Object) GetWithReceiver(s *Scope, c *Context, key Value, receiver *Object) (Value, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	if err := o.keyArg(key); err != nil {
		return Value{}, err
	}
	if receiver == nil {
		return Value{}, errNilReceiver
	}
	if err := receiver.check(); err != nil {
		return Value{}, err
	}
	if receiver.iso != o.iso {
		return Value{}, foreignIsolate("receiver")
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_object_get_with_receiver").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, key.h, receiver.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("Object.GetWithReceiver", r1)
	}
	return Value{iso: o.iso, sc: s, h: out}, nil
}

// SetWithReceiver writes key with receiver as `this` for accessors and as
// the redirect target for data properties (writing through an unrelated
// receiver creates the property on the receiver). ok is Just(false) when
// the write was ignored; an error means the setter threw.
func (o *Object) SetWithReceiver(s *Scope, c *Context, key, value Value, receiver *Object) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.keyArg(key); err != nil {
		return false, err
	}
	if err := o.valueArg(value, "value"); err != nil {
		return false, err
	}
	if receiver == nil {
		return false, errNilReceiver
	}
	if err := receiver.check(); err != nil {
		return false, err
	}
	if receiver.iso != o.iso {
		return false, foreignIsolate("receiver")
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_set_with_receiver").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, key.h, value.h,
		receiver.h, uintptr(unsafe.Pointer(&okv)))
	return boolResult("Object.SetWithReceiver", r1, uintptr(okv))
}

// --- instance-level accessors and lazy properties ---------------------------------------

// SetAccessor installs a native accessor pair on the OBJECT itself (not a
// template): every read invokes getter and every write invokes setter
// (either may be nil). The write routes through Object::Set, so JS writes
// reach the setter too. To JS property descriptors the property appears as
// a data property carrying its current value (the pinned AccessorInfo
// observation). key must be a Name.
func (o *Object) SetAccessor(s *Scope, c *Context, key Value, getter AccessorGetterCallback, setter AccessorSetterCallback) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.nameArg(key); err != nil {
		return false, err
	}
	if getter == nil && setter == nil {
		return false, errNoAccessorSide
	}
	handle, err := registerAccessorCallbacks(o.iso, getter, setter, Value{})
	if err != nil {
		return false, err
	}
	entry := lookupHostCallback(handle)
	if entry == nil {
		return false, errLostCallbackRegistration
	}
	var okv int32
	withSetter := uintptr(0)
	if setter != nil {
		withSetter = 1
	}
	r1, _, _ := proc("gov8_oo_object_set_accessor").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, key.h, entry.ctx,
		withSetter, uintptr(unsafe.Pointer(&okv)))
	if int64(r1) < 0 {
		dropHostCallback(handle)
		return false, shimError("Object.SetAccessor", r1)
	}
	return okv == 1, nil
}

// SetLazyDataProperty installs a lazy data property: getter runs on the
// first read of key, and the property is then an ordinary data property —
// later reads (native or JS) never re-invoke the getter. key must be a
// Name; the install uses no attributes and side-effect-ful callbacks, the
// pinned crate's defaults.
func (o *Object) SetLazyDataProperty(s *Scope, c *Context, key Value, getter AccessorGetterCallback) (bool, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return false, err
	}
	if err := o.nameArg(key); err != nil {
		return false, err
	}
	if getter == nil {
		return false, errNilLazyGetter
	}
	handle, err := registerAccessorCallbacks(o.iso, getter, nil, Value{})
	if err != nil {
		return false, err
	}
	entry := lookupHostCallback(handle)
	if entry == nil {
		return false, errLostCallbackRegistration
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_object_set_lazy_data_property").Call(
		o.iso.handleAssumingCheck(), c.handle, sh, o.h, key.h, entry.ctx,
		uintptr(unsafe.Pointer(&okv)))
	if int64(r1) < 0 {
		dropHostCallback(handle)
		return false, shimError("Object.SetLazyDataProperty", r1)
	}
	return okv == 1, nil
}

// --- call-as-function / constructor ------------------------------------------------------

// callArgs validates the argument slice and returns it as a wire array (a
// valid non-nil pointer even when empty).
func callArgs(iso *Isolate, args []Value) ([]uintptr, error) {
	for _, a := range args {
		if err := a.check(); err != nil {
			return nil, err
		}
		if a.iso != iso {
			return nil, foreignIsolate("argument")
		}
	}
	var dummy [1]uintptr
	wires := dummy[:]
	if len(args) > 0 {
		wires = make([]uintptr, len(args))
		for i, a := range args {
			wires[i] = a.h
		}
	}
	return wires, nil
}

// CallAsFunction invokes the object as a function with recv as the receiver
// (the undefined receiver is normalized to the global object by the engine,
// exactly like a sloppy-mode JS call). A non-callable object raises the
// pinned "object is not a function" TypeError, delivered to tc when given
// and reported as an error.
func (o *Object) CallAsFunction(s *Scope, c *Context, recv Value, args []Value, tc *TryCatch) (Value, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	if err := o.valueArg(recv, "receiver"); err != nil {
		return Value{}, err
	}
	wires, err := callArgs(o.iso, args)
	if err != nil {
		return Value{}, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return Value{}, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_object_call_as_function").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h, recv.h,
		uintptr(len(args)), uintptr(unsafe.Pointer(&wires[0])),
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("Object.CallAsFunction", r1)
	}
	return Value{iso: o.iso, sc: s, h: out}, nil
}

// CallAsConstructor invokes the object as a constructor (`new`): the
// returned value is the constructed `this` unless the constructor returns an
// object, which replaces it. A non-constructor raises the pinned "object is
// not a constructor" TypeError, delivered to tc when given and reported as
// an error.
func (o *Object) CallAsConstructor(s *Scope, c *Context, args []Value, tc *TryCatch) (Value, error) {
	sh, err := o.receiverArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	wires, err := callArgs(o.iso, args)
	if err != nil {
		return Value{}, err
	}
	tcv, err := tcArg(o.iso, tc)
	if err != nil {
		return Value{}, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_object_call_as_constructor").Call(
		o.iso.handleAssumingCheck(), c.handle, tcv, sh, o.h,
		uintptr(len(args)), uintptr(unsafe.Pointer(&wires[0])),
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("Object.CallAsConstructor", r1)
	}
	return Value{iso: o.iso, sc: s, h: out}, nil
}

// IsCallable reports whether the object can be called as a function
// (functions, arrows, methods, bound functions, class constructors, callable
// proxies, builtins — but not plain objects).
func (o *Object) IsCallable() (bool, error) {
	return o.boolQuery("gov8_oo_object_is_callable", "Object.IsCallable")
}

// IsConstructor reports whether the object can be invoked by `new`. It
// follows bound targets and proxies of constructors; arrows, methods,
// generators, async functions and non-constructable builtins are false.
func (o *Object) IsConstructor() (bool, error) {
	return o.boolQuery("gov8_oo_object_is_constructor", "Object.IsConstructor")
}

func (o *Object) boolQuery(op, export string) (bool, error) {
	if err := o.check(); err != nil {
		return false, err
	}
	sh, err := o.sc.checkedHandleAssumingIsolate()
	if err != nil {
		return false, err
	}
	r1, _, _ := proc(op).Call(o.iso.handleAssumingCheck(), sh, o.h)
	if int64(r1) < 0 {
		return false, shimError(export, r1)
	}
	return r1 == 1, nil
}

// --- value conversions -------------------------------------------------------------------

// ToObject returns the ECMAScript ToObject of the value: wrapper objects
// for primitives, identity for objects. undefined and null throw a
// TypeError, delivered to tc when given and reported as an error.
func (v Value) ToObject(s *Scope, c *Context, tc *TryCatch) (*Object, error) {
	sh, err := v.convArgs(s, c)
	if err != nil {
		return nil, err
	}
	tcv, err := tcArg(v.iso, tc)
	if err != nil {
		return nil, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_value_to_object").Call(
		v.iso.handleAssumingCheck(), c.handle, tcv, sh, v.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return nil, shimError("ToObject", r1)
	}
	return &Object{Value: Value{iso: v.iso, sc: s, h: out}}, nil
}

// ToBoolean returns the value coerced to a Boolean (never throws). Read the
// result with BooleanValue, or use BooleanValue directly for the same
// one-step observation.
func (v Value) ToBoolean(s *Scope) (Value, error) {
	if err := v.check(); err != nil {
		return Value{}, err
	}
	if s.iso != v.iso {
		return Value{}, foreignIsolate("scope")
	}
	sh, err := s.checkedHandleAssumingIsolate()
	if err != nil {
		return Value{}, err
	}
	h, err := callHandle("ToBoolean", proc("gov8_oo_value_to_boolean"),
		v.iso.handleAssumingCheck(), sh, v.h)
	if err != nil {
		return Value{}, err
	}
	return Value{iso: v.iso, sc: s, h: h}, nil
}

// ToInteger returns the ECMAScript ToInteger truncation of the value as an
// Integer (a scope-local value; read the raw int64 with IntegerValueRaw —
// note the raw read saturates out-of-range magnitudes exactly like the
// pinned C++ double→int64 cast, e.g. ±Infinity reads as math.MinInt64).
// A BigInt operand throws a TypeError, delivered to tc when given.
func (v Value) ToInteger(s *Scope, c *Context, tc *TryCatch) (Value, error) {
	sh, err := v.convArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	tcv, err := tcArg(v.iso, tc)
	if err != nil {
		return Value{}, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_value_to_integer").Call(
		v.iso.handleAssumingCheck(), c.handle, tcv, sh, v.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("ToInteger", r1)
	}
	return Value{iso: v.iso, sc: s, h: out}, nil
}

// ToBigInt returns the per-spec ToBigInt of the value: booleans and
// integral decimal strings convert; numbers and non-integral strings throw
// a TypeError (delivered to tc when given). Read the result with
// BigIntInt64.
func (v Value) ToBigInt(s *Scope, c *Context, tc *TryCatch) (Value, error) {
	sh, err := v.convArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	tcv, err := tcArg(v.iso, tc)
	if err != nil {
		return Value{}, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_value_to_big_int").Call(
		v.iso.handleAssumingCheck(), c.handle, tcv, sh, v.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("ToBigInt", r1)
	}
	return Value{iso: v.iso, sc: s, h: out}, nil
}

// ToDetailString returns Value::ToDetailString: identical to ToString for
// primitives, `Symbol(desc)` for symbols, an error's ToString message
// without the "Uncaught" prefix, and V8's compact `#<Object>` form for
// plain JSReceiver objects. A failed conversion is an error (delivered to
// tc when given).
func (v Value) ToDetailString(s *Scope, c *Context, tc *TryCatch) (Value, error) {
	sh, err := v.convArgs(s, c)
	if err != nil {
		return Value{}, err
	}
	tcv, err := tcArg(v.iso, tc)
	if err != nil {
		return Value{}, err
	}
	var out uintptr
	r1, _, _ := proc("gov8_oo_value_to_detail_string").Call(
		v.iso.handleAssumingCheck(), c.handle, tcv, sh, v.h,
		uintptr(unsafe.Pointer(&out)))
	if int64(r1) < 0 {
		return Value{}, shimError("ToDetailString", r1)
	}
	return Value{iso: v.iso, sc: s, h: out}, nil
}

// InstanceOf reports the Value::InstanceOf prototype-chain membership test
// against obj. A non-callable right-hand side throws the pinned
// "Right-hand side of 'instanceof' is not callable" TypeError, delivered to
// tc when given and reported as an error.
func (v Value) InstanceOf(s *Scope, c *Context, obj *Object, tc *TryCatch) (bool, error) {
	sh, err := v.convArgs(s, c)
	if err != nil {
		return false, err
	}
	if obj == nil {
		return false, errNilReceiver
	}
	if err := obj.check(); err != nil {
		return false, err
	}
	if obj.iso != v.iso {
		return false, foreignIsolate("object")
	}
	tcv, err := tcArg(v.iso, tc)
	if err != nil {
		return false, err
	}
	var okv int32
	r1, _, _ := proc("gov8_oo_value_instance_of").Call(
		v.iso.handleAssumingCheck(), c.handle, tcv, sh, v.h, obj.h,
		uintptr(unsafe.Pointer(&okv)))
	return boolResult("InstanceOf", r1, uintptr(okv))
}

// SameValueZero reports the ECMAScript SameValueZero relation (the Map/Set
// key semantics): SameValue plus +0 == -0. It is implemented exactly as the
// pinned crate implements it on the Rust side: SameValue, or both sides
// strictly equal to the zero Smi. The scope only materializes that zero.
func (v Value) SameValueZero(s *Scope, other Value) (bool, error) {
	same, err := v.SameValue(other)
	if err != nil {
		return false, err
	}
	if same {
		return true, nil
	}
	zero, err := s.Int32(0)
	if err != nil {
		return false, err
	}
	if err := zero.check(); err != nil {
		return false, err
	}
	vIsZero, err := v.StrictEquals(zero)
	if err != nil {
		return false, err
	}
	if !vIsZero {
		return false, nil
	}
	return other.StrictEquals(zero)
}

// convArgs validates a Value conversion's scope/context pair (same rules as
// the context conversions) and returns the checked scope handle.
func (v Value) convArgs(s *Scope, c *Context) (uintptr, error) {
	if err := v.ctxHandle(c); err != nil {
		return 0, err
	}
	if s.iso != v.iso {
		return 0, foreignIsolate("scope")
	}
	return s.checkedHandleAssumingIsolate()
}

// --- missing predicates inventory ----------------------------------------------------
//
// The 12 Value.Is* predicates the object-ops slice pins that no other slice
// exports. (The typed-array family — IsTypedArray and the twelve per-kind
// Is*Array — already lives in the buffers and typed-arrays slices.)

// IsFalse reports whether the value is exactly the primitive false.
func (v Value) IsFalse() (bool, error) { return v.predicate("gov8_oo_is_false") }

// IsArgumentsObject reports whether the value is an arguments object.
func (v Value) IsArgumentsObject() (bool, error) {
	return v.predicate("gov8_oo_is_arguments_object")
}

// IsSymbolObject reports whether the value is a Symbol wrapper object.
func (v Value) IsSymbolObject() (bool, error) {
	return v.predicate("gov8_oo_is_symbol_object")
}

// IsNativeError reports whether the value is an Error instance from the
// engine's native error family (TypeError, RangeError, ...).
func (v Value) IsNativeError() (bool, error) {
	return v.predicate("gov8_oo_is_native_error")
}

// IsAsyncFunction reports whether the value is an async function object.
func (v Value) IsAsyncFunction() (bool, error) {
	return v.predicate("gov8_oo_is_async_function")
}

// IsGeneratorFunction reports whether the value is a generator function
// object.
func (v Value) IsGeneratorFunction() (bool, error) {
	return v.predicate("gov8_oo_is_generator_function")
}

// IsPromise reports whether the value is a Promise object.
func (v Value) IsPromise() (bool, error) { return v.predicate("gov8_oo_is_promise") }

// IsMapIterator reports whether the value is a Map iterator object.
func (v Value) IsMapIterator() (bool, error) {
	return v.predicate("gov8_oo_is_map_iterator")
}

// IsSetIterator reports whether the value is a Set iterator object.
func (v Value) IsSetIterator() (bool, error) {
	return v.predicate("gov8_oo_is_set_iterator")
}

// IsGeneratorObject reports whether the value is a generator object.
func (v Value) IsGeneratorObject() (bool, error) {
	return v.predicate("gov8_oo_is_generator_object")
}

// IsWeakMap reports whether the value is a JSWeakMap instance.
func (v Value) IsWeakMap() (bool, error) { return v.predicate("gov8_oo_is_weak_map") }

// IsWeakSet reports whether the value is a JSWeakSet instance.
func (v Value) IsWeakSet() (bool, error) { return v.predicate("gov8_oo_is_weak_set") }

// --- shared error values --------------------------------------------------------------

var (
	errNotAName                 = errStr("gov8: key is not a Name (string or symbol)")
	errNilReceiver              = errStr("gov8: receiver object is required")
	errNoAccessorSide           = errStr("gov8: accessor requires a getter or a setter")
	errNilLazyGetter            = errStr("gov8: lazy data property requires a getter")
	errLostCallbackRegistration = errStr("gov8: callback registration lost")
)

type errStr string

func (e errStr) Error() string { return string(e) }
