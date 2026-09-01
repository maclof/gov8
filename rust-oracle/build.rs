fn main() {
    println!("cargo:rerun-if-changed=native/wasm_cache_oracle_bridge.cc");
    cc::Build::new()
        .cpp(true)
        .file("native/wasm_cache_oracle_bridge.cc")
        .flag_if_supported("/std:c++20")
        .compile("gov8_wasm_cache_oracle_bridge");
}
