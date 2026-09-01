//! Conformance runner binary.
//!
//! Prints the normalized JSON-lines report to stdout. Exit code 0 when every
//! check passed, 1 otherwise. Output is deterministic for a given pinned
//! `v8` crate and platform; see README.md.

use std::io::Write as _;
use std::process::ExitCode;

fn main() -> ExitCode {
    let report = oracle::run_all();
    let stdout = std::io::stdout();
    let mut lock = stdout.lock();
    let _ = lock.write_all(report.text.as_bytes());
    let _ = lock.flush();
    if report.all_passed() {
        ExitCode::SUCCESS
    } else {
        ExitCode::FAILURE
    }
}
