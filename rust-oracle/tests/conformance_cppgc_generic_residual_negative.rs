//! Compile-fail evidence for cppgc's public maximum-alignment boundary.

use std::fs;
use std::process::Command;

#[test]
fn cppgc_rejects_alignment_over_16_at_compile_time() {
    let deps = std::env::current_exe()
        .expect("current test executable")
        .parent()
        .expect("target deps directory")
        .to_path_buf();
    let v8_rlib = fs::read_dir(&deps)
        .expect("read target deps")
        .filter_map(Result::ok)
        .map(|entry| entry.path())
        .find(|path| {
            path.file_name()
                .and_then(|name| name.to_str())
                .is_some_and(|name| name.starts_with("libv8-") && name.ends_with(".rlib"))
        })
        .expect("compiled v8 rlib");

    let temp_root = std::env::temp_dir().join(format!(
        "gov8-cppgc-align-compile-fail-{}",
        std::process::id()
    ));
    if temp_root.exists() {
        fs::remove_dir_all(&temp_root).expect("remove stale exact temp directory");
    }
    fs::create_dir(&temp_root).expect("create exact temp directory");
    let source = temp_root.join("over_aligned.rs");
    fs::write(
        &source,
        r#"
use std::ffi::CStr;

#[repr(align(32))]
pub struct OverAligned;

unsafe impl v8::cppgc::GarbageCollected for OverAligned {
    fn trace(&self, _visitor: &mut v8::cppgc::Visitor) {}
    fn get_name(&self) -> &'static CStr { c"OverAligned" }
}

pub unsafe fn allocate(heap: &v8::cppgc::Heap) {
    let _ = unsafe { v8::cppgc::make_garbage_collected(heap, OverAligned) };
}
"#,
    )
    .expect("write compile-fail source");
    let output = Command::new("rustc")
        .arg("--edition=2021")
        .arg("--crate-type=lib")
        .arg(&source)
        .arg("-L")
        .arg(format!("dependency={}", deps.display()))
        .arg("--extern")
        .arg(format!("v8={}", v8_rlib.display()))
        .arg("--out-dir")
        .arg(&temp_root)
        .output()
        .expect("execute rustc compile-fail probe");
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "probe unexpectedly compiled");
    assert!(
        stderr.contains("assertion failed: std::mem::align_of::<T>() <= 16"),
        "unexpected compile failure:\n{stderr}"
    );
    fs::remove_dir_all(&temp_root).expect("remove exact temp directory");
}
