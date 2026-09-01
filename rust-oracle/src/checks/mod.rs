//! Ordered conformance check registry.
//!
//! The order of the registry below is part of the observable contract: the
//! JSON-lines report is emitted in exactly this order. Shutdown checks
//! (`platform/dispose_*`) run last because they consume V8 process state.

mod exceptions;
mod harness;
// Host-interaction slice: an independent registry executed by the
// `conformance-host` binary and `oracle::run_host_all()`.
pub(crate) mod host;
mod isolate_context;
mod microtasks;
mod platform;
mod scripts;
mod values;

use crate::report::CheckOutcome;

type CheckFn = fn() -> Vec<CheckOutcome>;

const CHECKS: &[CheckFn] = &[
    // platform: version identity and init state
    platform::version_constants,
    platform::version_string,
    platform::current_platform_present,
    // isolates and contexts
    isolate_context::context_script_roundtrip,
    isolate_context::sequential_isolates,
    isolate_context::global_object_native_access,
    isolate_context::context_reports_default_microtask_queue,
    // primitive values and conversions
    values::undefined,
    values::null,
    values::booleans,
    values::integers,
    values::number_f64,
    values::number_special,
    values::string_roundtrip,
    values::value_to_string_conversions,
    values::bigint_roundtrip,
    values::script_number_formatting,
    // script compile/run success
    scripts::arithmetic,
    scripts::string_concat,
    scripts::value_types,
    scripts::script_ids_distinct_and_increasing,
    scripts::empty_source,
    // exceptions
    exceptions::syntax_error_compile_fails,
    exceptions::syntax_error_message_position,
    exceptions::runtime_reference_error,
    exceptions::runtime_type_error,
    exceptions::throw_string,
    exceptions::throw_error_object,
    exceptions::trycatch_reset_allows_continue,
    // microtasks
    microtasks::explicit_policy_ordering,
    microtasks::auto_policy_ordering,
    microtasks::native_microtask_queue,
    // platform shutdown (must stay last)
    platform::dispose_returns_true,
    platform::dispose_platform_no_panic,
];

pub(crate) fn run_checks() -> Vec<CheckOutcome> {
    let mut outcomes = Vec::new();
    for check in CHECKS {
        outcomes.extend(check());
    }
    outcomes
}
