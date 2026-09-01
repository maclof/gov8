//! Platform-level checks: version identity, initialization, and shutdown.
//!
//! `dispose_*` checks consume process-wide V8 state and must always be the
//! final entries in the check registry.

use crate::json::Json;
use crate::report::{expect_eq, pass, CheckOutcome};
pub(crate) fn version_constants() -> Vec<CheckOutcome> {
    let actual = Json::obj(vec![
        ("major", Json::i(v8::MAJOR_VERSION as i64)),
        ("minor", Json::i(v8::MINOR_VERSION as i64)),
        ("build", Json::i(v8::BUILD_NUMBER as i64)),
        ("patch", Json::i(v8::PATCH_LEVEL as i64)),
    ]);
    let expected = Json::obj(vec![
        ("major", Json::i(15)),
        ("minor", Json::i(2)),
        ("build", Json::i(124)),
        ("patch", Json::i(1)),
    ]);
    vec![expect_eq("platform/version_constants", expected, actual)]
}

pub(crate) fn version_string() -> Vec<CheckOutcome> {
    let actual = Json::obj(vec![
        ("version_string", Json::s(v8::VERSION_STRING)),
        ("get_version", Json::s(v8::V8::get_version())),
    ]);
    // V8's GetVersion() ends with the crate's embedder suffix "-rusty".
    let expected = Json::obj(vec![
        ("version_string", Json::s("15.2.124.1-rusty")),
        ("get_version", Json::s("15.2.124.1-rusty")),
    ]);
    vec![expect_eq("platform/version_string", expected, actual)]
}

// Note: there is intentionally no "initialize is idempotent" check. In this
// crate `V8::initialize()` is NOT idempotent: the crate-level global state
// machine panics ("Invalid global state") on a second call, unlike raw V8
// C++ where V8::Initialize() returns early. This is characterized by
// tests/v8_lifecycle_negative.rs instead.

pub(crate) fn current_platform_present() -> Vec<CheckOutcome> {
    // Fetching the platform after initialization must succeed; it panics if
    // the platform was never installed, so reaching this line is the check.
    let _platform = v8::V8::get_current_platform();
    vec![pass("platform/current_platform_present", Json::b(true))]
}

pub(crate) fn dispose_returns_true() -> Vec<CheckOutcome> {
    // SAFETY: this is the final check; every isolate has been dropped.
    let disposed = unsafe { v8::V8::dispose() };
    vec![expect_eq(
        "platform/dispose_returns_true",
        Json::b(true),
        Json::b(disposed),
    )]
}

pub(crate) fn dispose_platform_no_panic() -> Vec<CheckOutcome> {
    v8::V8::dispose_platform();
    vec![pass("platform/dispose_platform_no_panic", Json::b(true))]
}
