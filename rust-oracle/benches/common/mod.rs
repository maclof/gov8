//! Shared benchmark support: V8 platform init and an environment banner
//! printed to stderr at the start of every benchmark binary.

use std::sync::Once;
use std::time::Duration;

/// Methodology shared by all oracle benchmarks (configured in each
/// `criterion_group!`):
/// - warm-up: 1 second per benchmark
/// - measurement: 3 seconds per benchmark
/// - samples: 50
/// - iteration: one full operation per sample iteration (`b.iter`)
pub const WARM_UP_TIME: Duration = Duration::from_secs(1);
pub const MEASUREMENT_TIME: Duration = Duration::from_secs(3);
pub const SAMPLE_SIZE: usize = 50;

static BANNER: Once = Once::new();

/// Prints in-process environment facts to stderr. Full machine metadata
/// (CPU model, OS build, RAM) is captured separately via
/// `scripts/capture-env.ps1` and stored under `bench-results/`.
pub fn banner() {
    BANNER.call_once(|| {
        eprintln!(
            "# oracle bench env: os={} arch={} logical_cpus={} build_profile={} \
			 v8_version_string={} v8_get_version={}",
            std::env::consts::OS,
            std::env::consts::ARCH,
            std::thread::available_parallelism().map_or(0, std::num::NonZeroUsize::get),
            if cfg!(debug_assertions) {
                "debug"
            } else {
                "release"
            },
            v8::VERSION_STRING,
            v8::V8::get_version(),
        );
    });
}
