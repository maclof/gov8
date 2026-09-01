//! Host-interaction conformance slice: object/function templates, native
//! function callbacks, accessor callbacks, internal fields and external
//! data ownership, Promise/PromiseResolver native APIs, and adjacent
//! lifetime behavior (globals, weak handles, isolate slots).
//!
//! This registry is fully independent of the 34-check base registry in
//! `src/checks/mod.rs`; it is executed by the `conformance-host` binary and
//! by `oracle::run_host_all()` and pinned by its own fixture under
//! `tests/fixtures/`. It deliberately performs no platform shutdown:
//! process-level dispose semantics are already pinned by the base slice,
//! and keeping this slice shutdown-free lets its fixture be verified
//! in-process without consuming the process-wide V8 state.
//!
//! Check order is part of the observable contract, exactly as for the base
//! registry. Rust panics inside native callbacks are NOT characterized
//! here: a panic unwinds out of the crate's `extern "C"` callback
//! trampoline and aborts the process (rustc >= 1.81 semantics). That
//! boundary is characterized out-of-process by the dedicated
//! `panic-boundary` executable and `tests/callback_panic_boundary.rs`.

mod accessors;
mod callbacks;
mod external_data;
mod lifetime;
mod promises;
mod templates;

use crate::report::CheckOutcome;

type CheckFn = fn() -> Vec<CheckOutcome>;

const HOST_CHECKS: &[CheckFn] = &[
    // template construction
    templates::function_template_construction,
    templates::instance_prototype_and_constructor,
    templates::object_template_instances,
    // native function callbacks
    callbacks::arguments_and_return,
    callbacks::arity_and_out_of_bounds_arguments,
    callbacks::receiver_and_callback_data,
    callbacks::construct_call_semantics,
    callbacks::native_reenters_javascript,
    callbacks::js_exception_from_native,
    // accessor callbacks
    accessors::native_data_property_getter_setter,
    accessors::static_accessor_on_constructor,
    // internal fields / external data ownership
    external_data::internal_field_externals,
    external_data::isolate_slot_ownership,
    // promise native APIs
    promises::resolver_settlement_semantics,
    promises::native_then_checkpoint,
    promises::reject_callback_events,
    // adjacent lifetime behavior
    lifetime::global_clone_equality,
    lifetime::weak_collect_forced_gc,
];

pub(crate) fn run_host_checks() -> Vec<CheckOutcome> {
    let mut outcomes = Vec::new();
    for check in HOST_CHECKS {
        outcomes.extend(check());
    }
    outcomes
}
