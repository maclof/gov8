use std::process::Output;

fn probe(mode: &str) -> Output {
    std::process::Command::new(env!("CARGO_BIN_EXE_conformance-context-residual"))
        .args(["--negative", mode])
        .output()
        .unwrap()
}

fn assert_v8_fatal(mode: &str, location: &str, reason: &str) {
    let output = probe(mode);
    assert_eq!(output.status.code(), Some(0x8000_0003_u32 as i32));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains(location), "stderr:\n{stderr}");
    assert!(stderr.contains(reason), "stderr:\n{stderr}");
}

#[test]
fn logical_negative_three_reaches_v8_negative_index_fatal() {
    assert_v8_fatal(
        "negative-index",
        "Context::SetEmbedderData()",
        "Negative index",
    );
}

#[test]
fn oversized_embedder_index_is_v8_fatal() {
    assert_v8_fatal("too-large", "Context::SetEmbedderData()", "Index too large");
}

#[test]
fn unaligned_embedder_pointer_is_v8_fatal() {
    assert_v8_fatal(
        "unaligned-pointer",
        "Context::SetAlignedPointerInEmbedderData()",
        "Pointer is not aligned",
    );
}

#[test]
fn logical_negative_one_corrupts_reserved_annex_slot() {
    let output = probe("reserved-annex");
    assert_eq!(output.status.code(), Some(0xC000_0005_u32 as i32));
}

#[test]
fn snapshot_with_uncleared_rc_slot_is_v8_fatal() {
    assert_v8_fatal(
        "uncleared-slot-snapshot",
        "CheckGlobalAndEternalHandles failed",
        "Fatal error",
    );
}
