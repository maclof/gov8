//! Internal fields and external data ownership checks.
//!
//! Characterized contract (the Go port must reproduce):
//! - `ObjectTemplate::set_internal_field_count(n)` gives every instance `n`
//!   internal fields; `Object::internal_field_count` reports it.
//! - Internal fields hold `Data` values: an `External` round-trips through
//!   `set_internal_field`/`get_internal_field` with its raw native pointer
//!   preserved exactly; so does a JS integer stored via `Integer`.
//! - Out-of-bounds internal field access is safe at the crate level:
//!   `set_internal_field` returns false and `get_internal_field` returns
//!   None instead of reaching into V8 memory.
//! - Aligned-pointer internal fields round-trip through
//!   `set_aligned_pointer_in_internal_field` /
//!   `get_aligned_pointer_from_internal_field` with the same tag.
//! - `Isolate::set_slot` takes ownership of Rust host data: a replacement
//!   drops the previous value immediately, `remove_slot` hands ownership
//!   back, and a value still stored is dropped when the isolate is dropped.
//!
//! No addresses are recorded in the fixture: only pointer-equality and
//! round-trip booleans, per the oracle normalization rules.

use crate::json::Json;
use crate::report::{expect_eq, CheckOutcome};
use std::cell::Cell;
use std::rc::Rc;

pub(crate) fn internal_field_externals() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);

    let ot = v8::ObjectTemplate::new(scope);
    let count_set = ot.set_internal_field_count(2);
    let obj = ot.new_instance(scope).unwrap();
    let field_count = obj.internal_field_count();

    // Native heap pointer wrapped in an External. The oracle owns the
    // allocation for the whole check and reconstructs it at the end.
    let native = Box::new(1234_u32);
    let native_ptr = Box::into_raw(native);

    let external = v8::External::new(scope, native_ptr.cast());
    let external_stored = obj.set_internal_field(0, external.into());
    let external_roundtrip = obj
        .get_internal_field(scope, 0)
        .and_then(|d| v8::Local::<v8::External>::try_from(d).ok())
        .map(|ext| ext.value() == native_ptr.cast())
        .unwrap_or(false);

    // A JS integer can live in an internal field too.
    let integer_stored = obj.set_internal_field(1, v8::Integer::new(scope, 99).into());
    let integer_roundtrip = obj
        .get_internal_field(scope, 1)
        .and_then(|d| v8::Local::<v8::Integer>::try_from(d).ok())
        .map(|i| i.value())
        .unwrap_or(-1);

    // Out-of-bounds access is rejected by the crate-level bounds checks.
    let oob_set = obj.set_internal_field(2, external.into());
    let oob_get_is_none = obj.get_internal_field(scope, 2).is_none();

    // Aligned-pointer internal fields on a fresh instance of the same
    // template, retrieved with the matching tag. The tag is an
    // `EmbedderDataTypeTag` whose allowed range is 0..15
    // (V8_EMBEDDER_DATA_TAG_COUNT); out-of-range tags abort the process
    // inside V8, so the oracle pins an in-range tag.
    let obj2 = ot.new_instance(scope).unwrap();
    obj2.set_aligned_pointer_in_internal_field(0, native_ptr.cast(), 7);
    let aligned_roundtrip = unsafe { obj2.get_aligned_pointer_from_internal_field(0, 7) }
        == native_ptr.cast::<std::ffi::c_void>();

    // The oracle regains ownership and verifies the pointee survived.
    let native_back = unsafe { Box::from_raw(native_ptr) };
    let native_allocation_roundtrip = *native_back == 1234;

    let actual = Json::obj(vec![
        ("count_set", Json::b(count_set)),
        ("field_count", Json::i(field_count as i64)),
        ("external_stored", Json::b(external_stored)),
        ("external_roundtrip", Json::b(external_roundtrip)),
        ("integer_stored", Json::b(integer_stored)),
        ("integer_value", Json::i(integer_roundtrip)),
        ("oob_set", Json::b(oob_set)),
        ("oob_get_is_none", Json::b(oob_get_is_none)),
        ("aligned_roundtrip", Json::b(aligned_roundtrip)),
        (
            "native_allocation_roundtrip",
            Json::b(native_allocation_roundtrip),
        ),
    ]);
    let expected = Json::obj(vec![
        ("count_set", Json::b(true)),
        ("field_count", Json::i(2)),
        ("external_stored", Json::b(true)),
        ("external_roundtrip", Json::b(true)),
        ("integer_stored", Json::b(true)),
        ("integer_value", Json::i(99)),
        ("oob_set", Json::b(false)),
        ("oob_get_is_none", Json::b(true)),
        ("aligned_roundtrip", Json::b(true)),
        ("native_allocation_roundtrip", Json::b(true)),
    ]);
    vec![expect_eq(
        "external/internal_field_externals",
        expected,
        actual,
    )]
}

struct SlotGuard {
    id: u32,
    dropped: Rc<Cell<u32>>,
}

impl Drop for SlotGuard {
    fn drop(&mut self) {
        self.dropped.set(self.dropped.get() + 1);
    }
}

pub(crate) fn isolate_slot_ownership() -> Vec<CheckOutcome> {
    let dropped = Rc::new(Cell::new(0_u32));

    let first_set;
    let read_back_id;
    let replaced;
    let drops_after_replace;
    let removed_id;
    let get_after_remove_is_none;
    let drops_before_isolate_drop;

    {
        let isolate = &mut v8::Isolate::new(Default::default());

        first_set = isolate.set_slot(SlotGuard {
            id: 1,
            dropped: Rc::clone(&dropped),
        });
        read_back_id = isolate.get_slot::<SlotGuard>().map(|g| g.id);

        // Setting the same slot type again replaces (and drops) the old value.
        replaced = isolate.set_slot(SlotGuard {
            id: 2,
            dropped: Rc::clone(&dropped),
        });
        drops_after_replace = dropped.get();

        // remove_slot hands ownership back to the caller.
        let removed = isolate.remove_slot::<SlotGuard>();
        removed_id = removed.as_ref().map(|g| g.id);
        get_after_remove_is_none = isolate.get_slot::<SlotGuard>().is_none();
        drop(removed);
        debug_assert_eq!(dropped.get(), 2);

        // A value still stored when the isolate dies is dropped with it.
        let stored_with_isolate = isolate.set_slot(SlotGuard {
            id: 3,
            dropped: Rc::clone(&dropped),
        });
        debug_assert!(stored_with_isolate);
        drops_before_isolate_drop = dropped.get();
    }
    let drops_after_isolate_drop = dropped.get();

    let actual = Json::obj(vec![
        ("first_set", Json::b(first_set)),
        (
            "read_back_id",
            read_back_id
                .map(|id| Json::i(id as i64))
                .unwrap_or(Json::Null),
        ),
        ("replaced_returns_false", Json::b(!replaced)),
        ("drops_after_replace", Json::i(drops_after_replace as i64)),
        (
            "removed_id",
            removed_id
                .map(|id| Json::i(id as i64))
                .unwrap_or(Json::Null),
        ),
        (
            "get_after_remove_is_none",
            Json::b(get_after_remove_is_none),
        ),
        (
            "drops_before_isolate_drop",
            Json::i(drops_before_isolate_drop as i64),
        ),
        (
            "drops_after_isolate_drop",
            Json::i(drops_after_isolate_drop as i64),
        ),
    ]);
    let expected = Json::obj(vec![
        ("first_set", Json::b(true)),
        ("read_back_id", Json::i(1)),
        ("replaced_returns_false", Json::b(true)),
        ("drops_after_replace", Json::i(1)),
        ("removed_id", Json::i(2)),
        ("get_after_remove_is_none", Json::b(true)),
        ("drops_before_isolate_drop", Json::i(2)),
        ("drops_after_isolate_drop", Json::i(3)),
    ]);
    vec![expect_eq(
        "external/isolate_slot_ownership",
        expected,
        actual,
    )]
}
