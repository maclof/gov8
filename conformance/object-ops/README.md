# conformance-object-ops

Go port of the pinned Rust oracle's object-operations and value-conversion
slice (`rust-oracle/src/bin/conformance-object-ops.rs`, crate
`v8 =152.2.0`, engine `V8 15.2.124.1-rusty`, target
`x86_64-pc-windows-msvc`).

## What is verified

The runner executes the 22 checks in the fixed oracle order and compares the
normalized JSON-lines report **byte-for-byte** against the pinned fixture:

    ../../rust-oracle/tests/fixtures/conformance-object-ops-v8_152.2.0_x86_64-pc-windows-msvc.jsonl

Areas (check id prefixes, in contract order):

| Area | Check | Go entry point |
|---|---|---|
| `obj-ops/proto/` | get_and_set | `checkProtoGetAndSet` |
| `obj-ops/property/` | has_delete_family | `checkHasDeleteFamily` |
| | real_named_interceptor_bypass | `checkRealNamedInterceptorBypass` |
| `obj-ops/identity/` | identity_hash | `checkIdentityHash` |
| | creation_context | `checkCreationContext` |
| | constructor_name | `checkConstructorName` |
| `obj-ops/receiver/` | get_set_with_receiver | `checkGetSetWithReceiver` |
| `obj-ops/lazy/` | lazy_data_property | `checkLazyDataProperty` |
| | instance_accessor | `checkInstanceAccessor` |
| `obj-ops/call/` | plain_object_not_callable | `checkCallPlainObjectNotCallable` |
| | function_call_and_construct | `checkCallFunctionCallAndConstruct` |
| | callable_constructor_predicates | `checkCallableConstructorPredicates` |
| `obj-ops/convert/` | to_object | `checkToObjectMatrix` |
| | to_boolean | `checkToBooleanMatrix` |
| | to_integer | `checkToIntegerMatrix` |
| | to_big_int | `checkToBigIntMatrix` |
| | to_detail_string | `checkToDetailStringMatrix` |
| `obj-ops/instanceof/` | api_instance_of | `checkAPIInstanceOf` |
| `obj-ops/equality/` | same_value_zero | `checkEqualitySameValueZero` |
| | value_hash | `checkValueHashSemantics` |
| `obj-ops/typeof/` | type_representation | `checkTypeRepresentation` |
| `obj-ops/predicates/` | missing_inventory | `checkPredicatesMissingInventory` |

## Go API surface exercised (gov8/object_ops.go)

Prototype get/set, has/has-index/has-own-property/delete/delete-index,
real-named get/has/attributes (interceptor bypass), identity hash,
creation-context identity, constructor name, receiver get/set, instance
accessors, lazy data properties, call-as-function/constructor, callable and
constructor predicates, ToObject/ToBoolean/ToInteger/ToBigInt/
ToDetailString, InstanceOf, SameValueZero, Value.GetHash, and the 12
missing `Value.Is*` predicates. The typed-array predicates reused here live
in the buffers/typed-arrays slices (`IsTypedArray`, `IsUint8Array`, ...).

## Documented shape differences (semantics preserved)

- `Option<bool>` → `(bool, error)`: `Just(b)` is `(b, nil)`; an empty maybe
  is `(false, non-nil)` and callers read `TryCatch.HasCaught` to learn
  whether an exception is actually pending (the cyclic `__proto__`
  rejection schedules none).
- `Option<Local<Value>>` real-named misses → `(Value{}, false, nil)`; only
  a throw is an error.
- `Object::call_as_function[_with_context]` (and the constructor pair) are
  one Go method with an explicit context, matching the module-wide
  convention; the two Rust variants share one engine binding.
- `Option<Local<Context>>` creation context → `CreationContextIs(*Context)`
  so no context-typed value leaks into the `Value` API.
- `same_value_zero` is implemented exactly as the pinned crate implements
  it Rust-side (`same_value || both strict_equals zero-Smi`).

## Running

    go test ./conformance/object-ops

requires the shim DLL (see the repository README;
the packaged DLL is used unless `GOV8_SHIM_DLL` selects a developer build).
