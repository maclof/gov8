//! Residual Fast API options, one-byte-string, and CType flag oracle.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty on
//! x86_64-pc-windows-msvc. Fast-path selection is requested explicitly; call
//! counters and return values, rather than optimizer timing, are observed.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::ffi::{c_char, c_void};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use v8::fast_api::{
    CFunction, CFunctionInfo, CTypeInfo, FastApiCallbackOptions, FastApiOneByteString, Flags,
    Int64Representation, Type,
};

const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

static OPTIONS_FAST: AtomicUsize = AtomicUsize::new(0);
static OPTIONS_SLOW: AtomicUsize = AtomicUsize::new(0);
static OPTIONS_EXTERNAL: AtomicBool = AtomicBool::new(false);
static OPTIONS_POINTER_MATCH: AtomicBool = AtomicBool::new(false);
static OPTIONS_ISOLATE_MATCH: AtomicBool = AtomicBool::new(false);
static OPTIONS_SCOPE_CONTEXT: AtomicBool = AtomicBool::new(false);
static OPTIONS_UNDEFINED: AtomicBool = AtomicBool::new(false);
static DATA_SENTINEL: AtomicUsize = AtomicUsize::new(0x51a7);

extern "C" fn fast_options(
    _receiver: v8::Local<v8::Object>,
    value: u32,
    options: *mut FastApiCallbackOptions,
) -> u32 {
    OPTIONS_FAST.fetch_add(1, Ordering::SeqCst);
    let options = unsafe { &mut *options };
    if options.data.is_external() {
        OPTIONS_EXTERNAL.store(true, Ordering::SeqCst);
        let external = unsafe { v8::Local::<v8::External>::cast_unchecked(options.data) };
        OPTIONS_POINTER_MATCH.store(
            external.value() == std::ptr::addr_of!(DATA_SENTINEL).cast_mut().cast(),
            Ordering::SeqCst,
        );
    } else if options.data.is_undefined() {
        OPTIONS_UNDEFINED.store(true, Ordering::SeqCst);
    }

    let immutable = unsafe { options.isolate_unchecked() } as *const v8::Isolate;
    let mutable = unsafe { options.isolate_unchecked_mut() } as *mut v8::Isolate;
    OPTIONS_ISOLATE_MATCH.store(immutable.cast_mut() == mutable, Ordering::SeqCst);

    let storage = unsafe { v8::CallbackScope::new(&*options) };
    let mut storage = std::pin::pin!(storage);
    let callback_scope = &mut storage.as_mut().init();
    let _context = callback_scope.get_current_context();
    OPTIONS_SCOPE_CONTEXT.store(true, Ordering::SeqCst);
    value + 1
}

const OPTIONS_CALL: CFunction = CFunction::new(
    fast_options as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::Uint32.as_info(),
            Type::CallbackOptions.as_info(),
        ],
        Int64Representation::Number,
    ),
);
const OPTIONS_OVERLOADS: &[CFunction] = &[OPTIONS_CALL];

extern "C" fn fast_options_ref(
    _receiver: v8::Local<v8::Object>,
    value: u32,
    options: &FastApiCallbackOptions,
) -> u32 {
    OPTIONS_FAST.fetch_add(1, Ordering::SeqCst);
    OPTIONS_UNDEFINED.store(options.data.is_undefined(), Ordering::SeqCst);
    let _isolate = unsafe { options.isolate_unchecked() };
    let storage = unsafe { v8::CallbackScope::new(options) };
    let mut storage = std::pin::pin!(storage);
    let callback_scope = &mut storage.as_mut().init();
    let _context = callback_scope.get_current_context();
    OPTIONS_SCOPE_CONTEXT.store(true, Ordering::SeqCst);
    value + 1
}

const OPTIONS_REF_CALL: CFunction = CFunction::new(
    fast_options_ref as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::Uint32.as_info(),
            Type::CallbackOptions.as_info(),
        ],
        Int64Representation::Number,
    ),
);
const OPTIONS_REF_OVERLOADS: &[CFunction] = &[OPTIONS_REF_CALL];

fn slow_options(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    OPTIONS_SLOW.fetch_add(1, Ordering::SeqCst);
    rv.set_uint32(args.get(0).uint32_value(scope).unwrap_or(0) + 1000);
}

static STRING_FAST: AtomicUsize = AtomicUsize::new(0);
static STRING_SLOW: AtomicUsize = AtomicUsize::new(0);

extern "C" fn fast_string(
    _receiver: v8::Local<v8::Object>,
    input: *const FastApiOneByteString,
) -> u32 {
    STRING_FAST.fetch_add(1, Ordering::SeqCst);
    let bytes = unsafe { &*input }.as_bytes();
    bytes.iter().enumerate().fold(0_u32, |sum, (index, byte)| {
        sum + (index as u32 + 1) * u32::from(*byte)
    })
}

const STRING_CALL: CFunction = CFunction::new(
    fast_string as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[Type::V8Value.as_info(), Type::SeqOneByteString.as_info()],
        Int64Representation::Number,
    ),
);
const STRING_OVERLOADS: &[CFunction] = &[STRING_CALL];

fn slow_string(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    STRING_SLOW.fetch_add(1, Ordering::SeqCst);
    rv.set_uint32(u32::MAX - 1);
}

static FLAG_FAST: AtomicUsize = AtomicUsize::new(0);
static FLAG_SLOW: AtomicUsize = AtomicUsize::new(0);
static SHARED_FAST: AtomicUsize = AtomicUsize::new(0);
static SHARED_SLOW: AtomicUsize = AtomicUsize::new(0);

extern "C" fn fast_i32(_receiver: v8::Local<v8::Object>, value: i32) -> i32 {
    FLAG_FAST.fetch_add(1, Ordering::SeqCst);
    value
}

extern "C" fn fast_restricted(_receiver: v8::Local<v8::Object>, value: f64) -> i32 {
    FLAG_FAST.fetch_add(1, Ordering::SeqCst);
    (value * 100.0) as i32
}

const ENFORCE_CALL: CFunction = CFunction::new(
    fast_i32 as *const c_void,
    &CFunctionInfo::new(
        Type::Int32.as_info(),
        &[
            Type::V8Value.as_info(),
            CTypeInfo::new(Type::Int32, Flags::EnforceRange),
        ],
        Int64Representation::Number,
    ),
);
const ENFORCE_OVERLOADS: &[CFunction] = &[ENFORCE_CALL];

const CLAMP_CALL: CFunction = CFunction::new(
    fast_i32 as *const c_void,
    &CFunctionInfo::new(
        Type::Int32.as_info(),
        &[
            Type::V8Value.as_info(),
            CTypeInfo::new(Type::Int32, Flags::Clamp),
        ],
        Int64Representation::Number,
    ),
);
const CLAMP_OVERLOADS: &[CFunction] = &[CLAMP_CALL];

const RESTRICTED_CALL: CFunction = CFunction::new(
    fast_restricted as *const c_void,
    &CFunctionInfo::new(
        Type::Int32.as_info(),
        &[
            Type::V8Value.as_info(),
            CTypeInfo::new(Type::Float64, Flags::IsRestricted),
        ],
        Int64Representation::Number,
    ),
);
const RESTRICTED_OVERLOADS: &[CFunction] = &[RESTRICTED_CALL];

fn slow_flag(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    FLAG_SLOW.fetch_add(1, Ordering::SeqCst);
    rv.set_int32(-1000);
}

extern "C" fn fast_allow_shared(
    _receiver: v8::Local<v8::Object>,
    value: v8::Local<v8::Value>,
) -> u32 {
    SHARED_FAST.fetch_add(1, Ordering::SeqCst);
    if value.is_array_buffer() {
        1
    } else if value.is_shared_array_buffer() {
        2
    } else {
        3
    }
}

const ALLOW_SHARED_CALL: CFunction = CFunction::new(
    fast_allow_shared as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            CTypeInfo::new(Type::V8Value, Flags::AllowShared),
        ],
        Int64Representation::Number,
    ),
);
const ALLOW_SHARED_OVERLOADS: &[CFunction] = &[ALLOW_SHARED_CALL];

fn slow_allow_shared(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    SHARED_SLOW.fetch_add(1, Ordering::SeqCst);
    rv.set_uint32(1000);
}

fn run_script<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).expect("source");
    v8::Script::compile(scope, source, None)
        .expect("compile")
        .run(scope)
        .expect("run")
}

fn rust_string(scope: &v8::PinScope<'_, '_>, source: &str) -> String {
    run_script(scope, source)
        .to_string(scope)
        .expect("string")
        .to_rust_string_lossy(scope)
}

fn set_function(
    scope: &v8::PinScope<'_, '_>,
    context: v8::Local<'_, v8::Context>,
    name: &str,
    function: v8::Local<'_, v8::Function>,
) {
    let name = v8::String::new(scope, name).unwrap();
    assert_eq!(
        context
            .global(scope)
            .set(scope, name.into(), function.into()),
        Some(true)
    );
}

fn reset_option_observations() {
    OPTIONS_FAST.store(0, Ordering::SeqCst);
    OPTIONS_SLOW.store(0, Ordering::SeqCst);
    OPTIONS_EXTERNAL.store(false, Ordering::SeqCst);
    OPTIONS_POINTER_MATCH.store(false, Ordering::SeqCst);
    OPTIONS_ISOLATE_MATCH.store(false, Ordering::SeqCst);
    OPTIONS_SCOPE_CONTEXT.store(false, Ordering::SeqCst);
    OPTIONS_UNDEFINED.store(false, Ordering::SeqCst);
}

fn options_function<'s>(
    scope: &v8::PinScope<'s, '_>,
    data: Option<v8::Local<'s, v8::Value>>,
    overloads: &'static [CFunction],
) -> v8::Local<'s, v8::Function> {
    let mut builder = v8::FunctionTemplate::builder(slow_options);
    if let Some(data) = data {
        builder = builder.data(data);
    }
    builder
        .build_fast(scope, overloads)
        .get_function(scope)
        .unwrap()
}

fn callback_options_checks() -> Vec<CheckOutcome> {
    let isolate = &mut v8::Isolate::new(Default::default());
    let (context_global, with_data_global, without_data_global) = {
        v8::scope!(let scope, isolate);
        let context = v8::Context::new(scope, Default::default());
        let scope = &mut v8::ContextScope::new(scope, context);
        let external =
            v8::External::new(scope, std::ptr::addr_of!(DATA_SENTINEL).cast_mut().cast());
        let with_data = options_function(scope, Some(external.into()), OPTIONS_OVERLOADS);
        let without_data = options_function(scope, None, OPTIONS_REF_OVERLOADS);
        (
            v8::Global::new(scope, context),
            v8::Global::new(scope, with_data),
            v8::Global::new(scope, without_data),
        )
    };

    v8::scope!(let scope, isolate);
    let context = v8::Local::new(scope, &context_global);
    let scope = &mut v8::ContextScope::new(scope, context);

    reset_option_observations();
    let with_data = v8::Local::new(scope, &with_data_global);
    set_function(scope, context, "withData", with_data);
    let external_results = rust_string(
        scope,
        "function od(x){return withData(x)};\
         %PrepareFunctionForOptimization(od);\
         const cold=od(10);\
         %OptimizeFunctionOnNextCall(od);\
         const fast=od(20); [cold,fast].join(',')",
    );
    let external_check = pass(
        "fast-api-residual/options/external_data_and_callback_scope",
        Json::obj(vec![
            ("creation_handle_scope_closed", Json::b(true)),
            ("results", Json::s(&external_results)),
            (
                "slow_calls",
                Json::i(OPTIONS_SLOW.load(Ordering::SeqCst) as i64),
            ),
            (
                "fast_calls",
                Json::i(OPTIONS_FAST.load(Ordering::SeqCst) as i64),
            ),
            (
                "data_is_external",
                Json::b(OPTIONS_EXTERNAL.load(Ordering::SeqCst)),
            ),
            (
                "external_pointer_matches",
                Json::b(OPTIONS_POINTER_MATCH.load(Ordering::SeqCst)),
            ),
            (
                "unchecked_isolate_accessors_match",
                Json::b(OPTIONS_ISOLATE_MATCH.load(Ordering::SeqCst)),
            ),
            (
                "callback_scope_has_context",
                Json::b(OPTIONS_SCOPE_CONTEXT.load(Ordering::SeqCst)),
            ),
        ]),
    );

    reset_option_observations();
    let without_data = v8::Local::new(scope, &without_data_global);
    set_function(scope, context, "withoutData", without_data);
    let undefined_results = rust_string(
        scope,
        "function ou(x){return withoutData(x)};\
         %PrepareFunctionForOptimization(ou); ou(1);\
         %OptimizeFunctionOnNextCall(ou);\
         const fast2=ou(2); const mismatch2=ou('x'); [fast2,mismatch2].join(',')",
    );
    let undefined_check = pass(
        "fast-api-residual/options/undefined_data_and_type_fallback",
        Json::obj(vec![
            ("results", Json::s(&undefined_results)),
            (
                "data_is_undefined",
                Json::b(OPTIONS_UNDEFINED.load(Ordering::SeqCst)),
            ),
            ("callback_abi", Json::s("shared_reference")),
            (
                "slow_calls",
                Json::i(OPTIONS_SLOW.load(Ordering::SeqCst) as i64),
            ),
            (
                "fast_calls",
                Json::i(OPTIONS_FAST.load(Ordering::SeqCst) as i64),
            ),
        ]),
    );
    vec![external_check, undefined_check]
}

fn direct_one_byte_checks() -> CheckOutcome {
    static BYTES: &[u8] = b"a\0\xffz";
    let direct = FastApiOneByteString {
        data: BYTES.as_ptr().cast::<c_char>(),
        length: BYTES.len() as u32,
    };
    let null_empty = FastApiOneByteString {
        data: std::ptr::null(),
        length: 0,
    };
    let null_nonzero = FastApiOneByteString {
        data: std::ptr::null(),
        length: 7,
    };
    pass(
        "fast-api-residual/one_byte/direct_as_bytes_boundaries",
        Json::obj(vec![
            (
                "direct_bytes",
                Json::arr(
                    direct
                        .as_bytes()
                        .iter()
                        .map(|byte| Json::i(i64::from(*byte)))
                        .collect(),
                ),
            ),
            ("null_zero_len", Json::i(null_empty.as_bytes().len() as i64)),
            (
                "null_nonzero_len",
                Json::i(null_nonzero.as_bytes().len() as i64),
            ),
            (
                "layout",
                Json::obj(vec![
                    (
                        "size",
                        Json::i(std::mem::size_of::<FastApiOneByteString>() as i64),
                    ),
                    (
                        "align",
                        Json::i(std::mem::align_of::<FastApiOneByteString>() as i64),
                    ),
                ]),
            ),
        ]),
    )
}

fn optimized_one_byte_check() -> CheckOutcome {
    STRING_FAST.store(0, Ordering::SeqCst);
    STRING_SLOW.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = v8::FunctionTemplate::builder(slow_string)
        .build_fast(scope, STRING_OVERLOADS)
        .get_function(scope)
        .unwrap();
    set_function(scope, context, "stringBytes", function);
    let results = rust_string(
        scope,
        r#"function os(x){return stringBytes(x)};
           %PrepareFunctionForOptimization(os);
           const cold=os("hello");
           %OptimizeFunctionOnNextCall(os);
           const ascii=os("hello");
           const nulLatin=os("\0A\xFF");
           const latin=os("\xFF");
           const empty=os("");
           const twoByte=os("雪");
           [cold,ascii,nulLatin,latin,empty,twoByte].join(',')"#,
    );
    pass(
        "fast-api-residual/one_byte/optimized_input_matrix",
        Json::obj(vec![
            ("results", Json::s(&results)),
            (
                "slow_calls",
                Json::i(STRING_SLOW.load(Ordering::SeqCst) as i64),
            ),
            (
                "fast_calls",
                Json::i(STRING_FAST.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )
}

fn info_bytes(info: CTypeInfo) -> [u8; 2] {
    const { assert!(std::mem::size_of::<CTypeInfo>() == 2) };
    unsafe { std::mem::transmute(info) }
}

fn ctype_matrix_check() -> CheckOutcome {
    let pairs = [
        ("Uint8+EnforceRange", Type::Uint8, Flags::EnforceRange),
        ("Int32+EnforceRange", Type::Int32, Flags::EnforceRange),
        ("Uint32+EnforceRange", Type::Uint32, Flags::EnforceRange),
        ("Int64+EnforceRange", Type::Int64, Flags::EnforceRange),
        ("Uint64+EnforceRange", Type::Uint64, Flags::EnforceRange),
        ("Uint8+Clamp", Type::Uint8, Flags::Clamp),
        ("Int32+Clamp", Type::Int32, Flags::Clamp),
        ("Uint32+Clamp", Type::Uint32, Flags::Clamp),
        ("Int64+Clamp", Type::Int64, Flags::Clamp),
        ("Uint64+Clamp", Type::Uint64, Flags::Clamp),
        ("Float32+IsRestricted", Type::Float32, Flags::IsRestricted),
        ("Float64+IsRestricted", Type::Float64, Flags::IsRestricted),
        ("V8Value+AllowShared", Type::V8Value, Flags::AllowShared),
    ];
    pass(
        "fast-api-residual/ctype_info/constructor_flag_matrix",
        Json::obj(vec![
            (
                "pairs",
                Json::arr(
                    pairs
                        .iter()
                        .map(|(name, ty, flags)| {
                            let bytes = info_bytes(CTypeInfo::new(
                                *ty,
                                Flags::from_bits_retain(flags.bits()),
                            ));
                            Json::obj(vec![
                                ("name", Json::s(name)),
                                ("type_byte", Json::i(i64::from(bytes[0]))),
                                ("flags_byte", Json::i(i64::from(bytes[1]))),
                                (
                                    "identifier",
                                    Json::i(
                                        (u32::from(bytes[0]) << 8 | u32::from(bytes[1])) as i64,
                                    ),
                                ),
                            ])
                        })
                        .collect(),
                ),
            ),
            ("rust_get_type_exposed", Json::b(false)),
            ("rust_get_flags_exposed", Json::b(false)),
            ("rust_get_id_exposed", Json::b(false)),
        ]),
    )
}

fn flag_execution_check() -> CheckOutcome {
    FLAG_FAST.store(0, Ordering::SeqCst);
    FLAG_SLOW.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    for (name, overloads) in [
        ("enforce", ENFORCE_OVERLOADS),
        ("clamp", CLAMP_OVERLOADS),
        ("restricted", RESTRICTED_OVERLOADS),
    ] {
        let function = v8::FunctionTemplate::builder(slow_flag)
            .build_fast(scope, overloads)
            .get_function(scope)
            .unwrap();
        set_function(scope, context, name, function);
    }
    let results = rust_string(
        scope,
        r#"function e0(x){return enforce(x)} function e1(x){return enforce(x)}
           function e2(x){return enforce(x)} function e3(x){return enforce(x)}
           function c0(x){return clamp(x)} function c1(x){return clamp(x)}
           function c2(x){return clamp(x)} function c3(x){return clamp(x)}
           function r0(x){return restricted(x)} function r1(x){return restricted(x)}
           function r2(x){return restricted(x)}
           function exercise(f,value) {
             %PrepareFunctionForOptimization(f); f(value);
             %OptimizeFunctionOnNextCall(f); return f(value);
           }
           [exercise(e0,42),exercise(e1,3.5),exercise(e2,2147483648),exercise(e3,NaN),
            exercise(c0,3.7),exercise(c1,1e100),exercise(c2,-1e100),exercise(c3,NaN),
            exercise(r0,1.25),exercise(r1,Infinity),exercise(r2,NaN)].join(',')"#,
    );
    pass(
        "fast-api-residual/ctype_info/optimized_flag_semantics",
        Json::obj(vec![
            ("results", Json::s(&results)),
            (
                "slow_calls",
                Json::i(FLAG_SLOW.load(Ordering::SeqCst) as i64),
            ),
            (
                "fast_calls",
                Json::i(FLAG_FAST.load(Ordering::SeqCst) as i64),
            ),
        ]),
    )
}

fn allow_shared_execution_check() -> CheckOutcome {
    SHARED_FAST.store(0, Ordering::SeqCst);
    SHARED_SLOW.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = v8::FunctionTemplate::builder(slow_allow_shared)
        .build_fast(scope, ALLOW_SHARED_OVERLOADS)
        .get_function(scope)
        .unwrap();
    set_function(scope, context, "allowShared", function);
    let results = rust_string(
        scope,
        r#"function a0(x){return allowShared(x)}
           function a1(x){return allowShared(x)}
           function a2(x){return allowShared(x)}
           function exerciseShared(f,value) {
             %PrepareFunctionForOptimization(f); f(value);
             %OptimizeFunctionOnNextCall(f); return f(value);
           }
           [exerciseShared(a0,new ArrayBuffer(4)),
            exerciseShared(a1,new SharedArrayBuffer(4)),
            exerciseShared(a2,{})].join(',')"#,
    );
    pass(
        "fast-api-residual/ctype_info/allow_shared_v8value_semantics",
        Json::obj(vec![
            ("results", Json::s(&results)),
            (
                "slow_calls",
                Json::i(SHARED_SLOW.load(Ordering::SeqCst) as i64),
            ),
            (
                "fast_calls",
                Json::i(SHARED_FAST.load(Ordering::SeqCst) as i64),
            ),
            ("generic_v8value_restricts_input", Json::b(false)),
        ]),
    )
}

fn pin_check() -> CheckOutcome {
    pass(
        "fast-api-residual/pin_and_public_surface",
        Json::obj(vec![
            ("crate", Json::s("v8=152.2.0")),
            ("v8", Json::s(v8::V8::get_version())),
            ("target", Json::s("x86_64-pc-windows-msvc")),
            (
                "callback_options_size",
                Json::i(std::mem::size_of::<FastApiCallbackOptions>() as i64),
            ),
            (
                "callback_options_align",
                Json::i(std::mem::align_of::<FastApiCallbackOptions>() as i64),
            ),
            ("callback_options_has_isolate", Json::b(true)),
            ("callback_options_has_data", Json::b(true)),
            ("callback_options_has_fallback", Json::b(false)),
            (
                "supported_callback_abis",
                Json::arr(vec![
                    Json::s("mutable_pointer"),
                    Json::s("shared_reference"),
                ]),
            ),
            ("options_excluded_from_js_arity", Json::b(true)),
        ]),
    )
}

extern "C" fn invalid_options_middle(
    _receiver: v8::Local<v8::Object>,
    _options: *mut FastApiCallbackOptions,
    _value: u32,
) -> u32 {
    FLAG_FAST.fetch_add(1, Ordering::SeqCst);
    0
}

const OPTIONS_MIDDLE_CALL: CFunction = CFunction::new(
    invalid_options_middle as *const c_void,
    &CFunctionInfo::new(
        Type::Uint32.as_info(),
        &[
            Type::V8Value.as_info(),
            Type::CallbackOptions.as_info(),
            Type::Uint32.as_info(),
        ],
        Int64Representation::Number,
    ),
);
const OPTIONS_MIDDLE_OVERLOADS: &[CFunction] = &[OPTIONS_MIDDLE_CALL];

extern "C" fn invalid_clamp_bool(_receiver: v8::Local<v8::Object>, value: bool) -> bool {
    FLAG_FAST.fetch_add(1, Ordering::SeqCst);
    value
}

const CLAMP_BOOL_CALL: CFunction = CFunction::new(
    invalid_clamp_bool as *const c_void,
    &CFunctionInfo::new(
        Type::Bool.as_info(),
        &[
            Type::V8Value.as_info(),
            CTypeInfo::new(Type::Bool, Flags::Clamp),
        ],
        Int64Representation::Number,
    ),
);
const CLAMP_BOOL_OVERLOADS: &[CFunction] = &[CLAMP_BOOL_CALL];

fn invalid_signature(overloads: &'static [CFunction], two_js_arguments: bool) {
    FLAG_FAST.store(0, Ordering::SeqCst);
    FLAG_SLOW.store(0, Ordering::SeqCst);
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = v8::FunctionTemplate::builder(slow_flag)
        .build_fast(scope, overloads)
        .get_function(scope)
        .unwrap();
    set_function(scope, context, "invalid", function);
    let source = if two_js_arguments {
        "function bad(a,b){return invalid(a,b)};\
         %PrepareFunctionForOptimization(bad); bad(1,2);\
         %OptimizeFunctionOnNextCall(bad); bad(1,2)"
    } else {
        "function bad(a){return invalid(a)};\
         %PrepareFunctionForOptimization(bad); bad(true);\
         %OptimizeFunctionOnNextCall(bad); bad(true)"
    };
    let result = run_script(scope, source).integer_value(scope);
    println!(
        "invalid_signature_survived=result={result:?},slow={},fast={}",
        FLAG_SLOW.load(Ordering::SeqCst),
        FLAG_FAST.load(Ordering::SeqCst)
    );
}

fn main() -> std::process::ExitCode {
    v8::V8::set_flags_from_string("--allow-natives-syntax");
    oracle::ensure_v8();
    let mode = std::env::args().nth(1);
    if mode.is_some() {
        suppress_windows_fatal_dialogs();
    }
    match mode.as_deref() {
        Some("mode=options-middle") => {
            invalid_signature(OPTIONS_MIDDLE_OVERLOADS, true);
            return std::process::ExitCode::FAILURE;
        }
        Some("mode=clamp-bool") => {
            invalid_signature(CLAMP_BOOL_OVERLOADS, false);
            return std::process::ExitCode::FAILURE;
        }
        Some(mode) => panic!("unknown mode: {mode}"),
        None => {}
    }

    let mut outcomes = vec![pin_check()];
    outcomes.extend(callback_options_checks());
    outcomes.push(direct_one_byte_checks());
    outcomes.push(optimized_one_byte_check());
    outcomes.push(ctype_matrix_check());
    outcomes.push(flag_execution_check());
    outcomes.push(allow_shared_execution_check());
    let passed = outcomes.iter().filter(|outcome| outcome.passed()).count();
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!(
        "{}",
        summary_line(outcomes.len(), passed, outcomes.len() - passed)
    );
    if passed == outcomes.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
