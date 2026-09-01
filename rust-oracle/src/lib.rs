//! gov8 Rust oracle.
//!
//! Executable behavioral reference for the pinned `v8` crate (rusty_v8).
//! The conformance runner produces deterministic, normalized JSON-lines that
//! a Go implementation must be able to reproduce byte-for-byte. See
//! README.md for the exact pinned versions, build configuration, and
//! normalization rules.
//!
//! Sole supported target: `x86_64-pc-windows-msvc`. The pinned `v8` crate
//! ships a prebuilt static V8 library for that target only, and every
//! conformance fixture and benchmark record was captured there. Builds for
//! any other target fail at compile time by design: the `v8` dependency is
//! declared for the supported target only (see `Cargo.toml`) and the
//! `compile_error!` below is the single clear failure such builds get.

// Single-platform guard. Keep the cfg in sync with the
// `[target.'cfg(...)'.dependencies]` table in `Cargo.toml`: the dependency
// gate keeps unsupported targets from downloading or building the V8
// artifact at all, and this error is the explanation they see instead.
#[cfg(not(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc")))]
compile_error!(
    "gov8-rust-oracle builds only for x86_64-pc-windows-msvc: the pinned \
     v8 crate (=152.2.0) ships a prebuilt static V8 library for that target \
     alone, and all conformance fixtures and benchmarks are recorded there. \
     See rust-oracle/README.md, section \"Supported platform\"."
);

// v8-free modules stay unguarded; everything that touches V8 (directly or
// through `checks`) is compiled only on the supported target.
pub mod json;
pub mod report;

#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
pub mod checks;

#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
use std::sync::Once;

#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
static V8_START: Once = Once::new();

/// Initializes the V8 platform exactly once per process.
///
/// `thread_pool_size = 0` lets V8 pick its default worker count;
/// idle-task support is off. These settings are part of the oracle's pinned
/// platform configuration and must stay identical in the Go benchmarks.
#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
pub fn ensure_v8() {
    V8_START.call_once(|| {
        let platform = v8::new_default_platform(0, false).make_shared();
        v8::V8::initialize_platform(platform);
        v8::V8::initialize();
    });
}

/// Result of a full conformance run.
pub struct RunReport {
    /// Complete normalized JSON-lines text (each line `\n`-terminated).
    pub text: String,
    /// Number of checks that failed.
    pub failed: usize,
}

impl RunReport {
    pub fn all_passed(&self) -> bool {
        self.failed == 0
    }
}

/// Runs every conformance check in fixed order and renders the report.
///
/// This consumes the process-wide V8 state: the final checks call
/// `V8::dispose()` and `V8::dispose_platform()`, which can only happen once
/// per process. Call this at most once per process.
#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
pub fn run_all() -> RunReport {
    ensure_v8();
    let outcomes = checks::run_checks();
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&report::summary_line(total, passed, failed));
    text.push('\n');
    RunReport { text, failed }
}

/// Runs the host-interaction conformance slice (templates, native callbacks,
/// accessors, internal fields / external data, promises, adjacent lifetime)
/// and renders the report in the same JSON-lines format as [`run_all`].
///
/// Unlike [`run_all`], this slice performs no platform shutdown, so it does
/// not consume the process-wide V8 state; it may run once in a process that
/// later keeps using V8 (and `run_all` may still run after it, since this
/// slice never disposes).
#[cfg(all(target_os = "windows", target_arch = "x86_64", target_env = "msvc"))]
pub fn run_host_all() -> RunReport {
    ensure_v8();
    let outcomes = checks::host::run_host_checks();
    let total = outcomes.len();
    let mut passed = 0usize;
    let mut text = String::new();
    for outcome in &outcomes {
        if outcome.passed() {
            passed += 1;
        }
        text.push_str(&outcome.to_line());
        text.push('\n');
    }
    let failed = total - passed;
    text.push_str(&report::summary_line(total, passed, failed));
    text.push('\n');
    RunReport { text, failed }
}
