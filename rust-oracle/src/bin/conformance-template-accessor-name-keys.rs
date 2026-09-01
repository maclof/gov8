//! Arbitrary `Name` keys for template accessor APIs.
//!
//! Pinned to rusty_v8 152.2.0 / V8 15.2.124.1-rusty. The Rust wrappers named
//! `ObjectTemplate::{set_accessor,set_accessor_with_setter,
//! set_accessor_with_configuration}` all bind V8's SetNativeDataProperty.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};
use std::cell::{Cell, RefCell};

const SEM_FAILCRITICALERRORS: u32 = 0x0001;
const SEM_NOGPFAULTERRORBOX: u32 = 0x0002;
const SEM_NOOPENFILEERRORBOX: u32 = 0x8000;

#[link(name = "kernel32")]
unsafe extern "system" {
    #[link_name = "SetErrorMode"]
    fn set_error_mode(mode: u32) -> u32;
}

thread_local! {
    static FUNCTION_GETS: Cell<u32> = const { Cell::new(0) };
    static FUNCTION_SETS: Cell<u32> = const { Cell::new(0) };
    static FUNCTION_LAST_SET: Cell<i64> = const { Cell::new(-1) };
    static NATIVE_LOG: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    static NATIVE_STATE: Cell<i64> = const { Cell::new(17) };
}

fn suppress_windows_fatal_dialogs() {
    unsafe {
        set_error_mode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
    }
}

fn reset_state() {
    FUNCTION_GETS.set(0);
    FUNCTION_SETS.set(0);
    FUNCTION_LAST_SET.set(-1);
    NATIVE_LOG.with_borrow_mut(Vec::clear);
    NATIVE_STATE.set(17);
}

fn name<'s>(scope: &v8::PinScope<'s, '_, ()>, text: &str) -> v8::Local<'s, v8::Name> {
    v8::String::new(scope, text).unwrap().into()
}

fn symbol<'s>(scope: &v8::PinScope<'s, '_, ()>, text: &str) -> v8::Local<'s, v8::Symbol> {
    let description = v8::String::new(scope, text).unwrap();
    v8::Symbol::new(scope, Some(description))
}

fn set_global(
    scope: &v8::PinScope<'_, '_>,
    context: v8::Local<v8::Context>,
    key: &str,
    value: v8::Local<v8::Value>,
) {
    context
        .global(scope)
        .set(scope, name(scope, key).into(), value)
        .unwrap();
}

fn eval<'s>(scope: &v8::PinScope<'s, '_>, source: &str) -> v8::Local<'s, v8::Value> {
    let source = v8::String::new(scope, source).unwrap();
    v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
}

fn eval_text(scope: &v8::PinScope<'_, '_>, source: &str) -> String {
    eval(scope, source).to_rust_string_lossy(scope)
}

fn attributes(value: v8::PropertyAttribute) -> Json {
    Json::obj(vec![
        ("bits", Json::i(i64::from(value.as_u32()))),
        ("read_only", Json::b(value.is_read_only())),
        ("dont_enum", Json::b(value.is_dont_enum())),
        ("dont_delete", Json::b(value.is_dont_delete())),
    ])
}

fn accessor_getter(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    FUNCTION_GETS.set(FUNCTION_GETS.get() + 1);
    rv.set_int32(41);
}

fn accessor_getter_alt(
    _scope: &mut v8::PinScope<'_, '_>,
    _args: v8::FunctionCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    rv.set_int32(42);
}

fn accessor_setter(
    scope: &mut v8::PinScope<'_, '_>,
    args: v8::FunctionCallbackArguments<'_>,
    _rv: v8::ReturnValue<v8::Value>,
) {
    FUNCTION_SETS.set(FUNCTION_SETS.get() + 1);
    FUNCTION_LAST_SET.set(args.get(0).integer_value(scope).unwrap_or(-1));
}

fn key_kind_and_text(
    scope: &v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
) -> (&'static str, String) {
    let value: v8::Local<v8::Value> = key.into();
    if value.is_symbol() {
        let symbol = value.try_cast::<v8::Symbol>().unwrap();
        (
            "symbol",
            symbol.description(scope).to_rust_string_lossy(scope),
        )
    } else {
        ("string", value.to_rust_string_lossy(scope))
    }
}

fn native_getter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
    args: v8::PropertyCallbackArguments<'_>,
    mut rv: v8::ReturnValue<v8::Value>,
) {
    let (kind, text) = key_kind_and_text(scope, key);
    let data = args.data();
    let (data_kind, result) = if data.is_undefined() {
        (
            "undefined",
            if text == "native-simple" {
                61
            } else {
                NATIVE_STATE.get()
            },
        )
    } else {
        ("int32", data.integer_value(scope).unwrap_or(-1))
    };
    NATIVE_LOG.with_borrow_mut(|log| {
        log.push(format!("get:{kind}:{text}:data={data_kind}"));
    });
    rv.set_double(result as f64);
}

fn native_setter(
    scope: &mut v8::PinScope<'_, '_>,
    key: v8::Local<v8::Name>,
    value: v8::Local<v8::Value>,
    args: v8::PropertyCallbackArguments<'_>,
    _rv: v8::ReturnValue<()>,
) {
    let (kind, text) = key_kind_and_text(scope, key);
    let data_kind = if args.data().is_undefined() {
        "undefined"
    } else {
        "int32"
    };
    let value = value.integer_value(scope).unwrap_or(-1);
    NATIVE_LOG.with_borrow_mut(|log| {
        log.push(format!("set:{kind}:{text}:{value}:data={data_kind}"));
    });
    NATIVE_STATE.set(value);
}

fn function_template_accessor_property() -> CheckOutcome {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::FunctionTemplate::new(scope, accessor_getter);
    let string_key = name(scope, "string-accessor");
    let symbol_key = symbol(scope, "symbol-accessor");
    let getter = v8::FunctionTemplate::new(scope, accessor_getter);
    let setter = v8::FunctionTemplate::new(scope, accessor_setter);
    template.set_accessor_property(
        string_key,
        Some(getter),
        Some(setter),
        v8::PropertyAttribute::NONE,
    );
    template.set_accessor_property(
        symbol_key.into(),
        Some(getter),
        Some(setter),
        v8::PropertyAttribute::DONT_ENUM | v8::PropertyAttribute::DONT_DELETE,
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let function = template.get_function(scope).unwrap();
    set_global(scope, context, "fnValue", function.into());
    set_global(scope, context, "fnSymbol", symbol_key.into());
    let reads = eval_text(
        scope,
        "`${fnValue['string-accessor']}|${fnValue[fnSymbol]}`",
    );
    let writes = eval_text(
        scope,
        "`${Reflect.set(fnValue,'string-accessor',52)}|${Reflect.set(fnValue,fnSymbol,53)}`",
    );
    let descriptor = eval_text(
        scope,
        "(()=>{const d=Object.getOwnPropertyDescriptor(fnValue,fnSymbol);return `${typeof d.get}|${typeof d.set}|${d.enumerable}|${d.configurable}`})()",
    );
    let attrs = function
        .get_property_attributes(scope, symbol_key.into())
        .unwrap();

    pass(
        "template-accessor-name-keys/function/accessor_property",
        Json::obj(vec![
            ("reads", Json::s(&reads)),
            ("writes", Json::s(&writes)),
            ("descriptor", Json::s(&descriptor)),
            ("attributes", attributes(attrs)),
            ("getter_hits", Json::i(i64::from(FUNCTION_GETS.get()))),
            ("setter_hits", Json::i(i64::from(FUNCTION_SETS.get()))),
            ("last_set", Json::i(FUNCTION_LAST_SET.get())),
        ]),
    )
}

fn object_template_accessor_property() -> CheckOutcome {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let string_key = name(scope, "string-accessor");
    let symbol_key = symbol(scope, "symbol-accessor");
    let getter = v8::FunctionTemplate::new(scope, accessor_getter);
    let setter = v8::FunctionTemplate::new(scope, accessor_setter);
    template.set_accessor_property(
        string_key,
        Some(getter),
        Some(setter),
        v8::PropertyAttribute::NONE,
    );
    template.set_accessor_property(
        symbol_key.into(),
        Some(getter),
        Some(setter),
        v8::PropertyAttribute::DONT_ENUM | v8::PropertyAttribute::DONT_DELETE,
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = template.new_instance(scope).unwrap();
    set_global(scope, context, "objectValue", object.into());
    set_global(scope, context, "objectSymbol", symbol_key.into());
    let reads = eval_text(
        scope,
        "`${objectValue['string-accessor']}|${objectValue[objectSymbol]}`",
    );
    let writes = eval_text(scope, "`${Reflect.set(objectValue,'string-accessor',62)}|${Reflect.set(objectValue,objectSymbol,63)}`");
    let descriptor = eval_text(
        scope,
        "(()=>{const d=Object.getOwnPropertyDescriptor(objectValue,objectSymbol);return `${typeof d.get}|${typeof d.set}|${d.enumerable}|${d.configurable}`})()",
    );
    let attrs = object
        .get_property_attributes(scope, symbol_key.into())
        .unwrap();

    pass(
        "template-accessor-name-keys/object/accessor_property",
        Json::obj(vec![
            ("reads", Json::s(&reads)),
            ("writes", Json::s(&writes)),
            ("descriptor", Json::s(&descriptor)),
            ("attributes", attributes(attrs)),
            ("getter_hits", Json::i(i64::from(FUNCTION_GETS.get()))),
            ("setter_hits", Json::i(i64::from(FUNCTION_SETS.get()))),
            ("last_set", Json::i(FUNCTION_LAST_SET.get())),
        ]),
    )
}

fn object_template_native_data_properties() -> CheckOutcome {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let simple_symbol = symbol(scope, "native-simple");
    let control_key = name(scope, "native-control");
    let data_symbol = symbol(scope, "native-data");
    template.set_accessor(simple_symbol.into(), native_getter);
    template.set_accessor_with_setter(control_key, native_getter, native_setter);
    template.set_accessor_with_configuration(
        data_symbol.into(),
        v8::AccessorConfiguration::new(native_getter)
            .setter(native_setter)
            .data(v8::Integer::new(scope, 73).into())
            .property_attribute(
                v8::PropertyAttribute::DONT_ENUM | v8::PropertyAttribute::DONT_DELETE,
            ),
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = template.new_instance(scope).unwrap();
    set_global(scope, context, "nativeObject", object.into());
    set_global(scope, context, "simpleSymbol", simple_symbol.into());
    set_global(scope, context, "dataSymbol", data_symbol.into());
    let reads = eval_text(
        scope,
        "`${nativeObject[simpleSymbol]}|${nativeObject['native-control']}|${nativeObject[dataSymbol]}`",
    );
    let writes = eval_text(
        scope,
        "`${Reflect.set(nativeObject,'native-control',81)}|${Reflect.set(nativeObject,dataSymbol,82)}`",
    );
    let after_write = eval_text(
        scope,
        "`${nativeObject['native-control']}|${nativeObject[dataSymbol]}`",
    );
    let data_attrs = object
        .get_property_attributes(scope, data_symbol.into())
        .unwrap();
    let descriptors = eval_text(
        scope,
        "(()=>{const a=Object.getOwnPropertyDescriptor(nativeObject,simpleSymbol),b=Object.getOwnPropertyDescriptor(nativeObject,dataSymbol);return `${typeof a.get}|${typeof a.set}|${typeof b.get}|${typeof b.set}`})()",
    );

    pass(
        "template-accessor-name-keys/object/native_data_property_wrappers",
        Json::obj(vec![
            ("reads", Json::s(&reads)),
            ("writes", Json::s(&writes)),
            ("after_write", Json::s(&after_write)),
            ("descriptors", Json::s(&descriptors)),
            ("data_attributes", attributes(data_attrs)),
            (
                "callback_log",
                Json::arr(NATIVE_LOG.with_borrow(|log| log.iter().map(|v| Json::s(v)).collect())),
            ),
        ]),
    )
}

fn retention_and_post_publication() -> CheckOutcome {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    let held = {
        v8::scope!(let scope, isolate);
        let template = v8::ObjectTemplate::new(scope);
        let retained = symbol(scope, "retained-native");
        template.set_accessor_with_configuration(
            retained.into(),
            v8::AccessorConfiguration::new(native_getter).data(v8::Integer::new(scope, 91).into()),
        );
        let retained_accessor = symbol(scope, "retained-accessor");
        let getter = v8::FunctionTemplate::new(scope, accessor_getter);
        template.set_accessor_property(
            retained_accessor.into(),
            Some(getter),
            None,
            v8::PropertyAttribute::NONE,
        );
        v8::Global::new(scope, template)
    };
    isolate.low_memory_notification();

    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let template = v8::Local::new(scope, &held);
    let first = template.new_instance(scope).unwrap();
    let late_native = symbol(scope, "late-native");
    template.set_accessor(late_native.into(), native_getter);
    let getter = v8::FunctionTemplate::new(scope, accessor_getter);
    let late_accessor = symbol(scope, "late-accessor");
    template.set_accessor_property(
        late_accessor.into(),
        Some(getter),
        None,
        v8::PropertyAttribute::NONE,
    );
    let second = template.new_instance(scope).unwrap();
    set_global(scope, context, "retainedObject", second.into());
    let retained = eval_text(
        scope,
        "(()=>Object.getOwnPropertySymbols(retainedObject).sort((a,b)=>a.description.localeCompare(b.description)).map(s=>`${s.description}:${retainedObject[s]}`).join('|'))()",
    );

    let function_template = v8::FunctionTemplate::new(scope, accessor_getter);
    let function = function_template.get_function(scope).unwrap();
    let late_function = symbol(scope, "late-function");
    function_template.set_accessor_property(
        late_function.into(),
        Some(v8::FunctionTemplate::new(scope, accessor_getter)),
        None,
        v8::PropertyAttribute::NONE,
    );
    let repeated_function = function_template.get_function(scope).unwrap();

    pass(
        "template-accessor-name-keys/lifecycle/retention_post_publication",
        Json::obj(vec![
            ("retained", Json::s(&retained)),
            (
                "first_has_late_native",
                Json::b(first.has_own_property(scope, late_native.into()).unwrap()),
            ),
            (
                "second_has_late_native",
                Json::b(second.has_own_property(scope, late_native.into()).unwrap()),
            ),
            (
                "first_has_late_accessor",
                Json::b(first.has_own_property(scope, late_accessor.into()).unwrap()),
            ),
            (
                "second_has_late_accessor",
                Json::b(
                    second
                        .has_own_property(scope, late_accessor.into())
                        .unwrap(),
                ),
            ),
            (
                "function_has_late_accessor",
                Json::b(
                    function
                        .has_own_property(scope, late_function.into())
                        .unwrap(),
                ),
            ),
            (
                "repeated_function_same",
                Json::b(function.strict_equals(repeated_function.into())),
            ),
        ]),
    )
}

fn duplicate_accessor_names() -> CheckOutcome {
    reset_state();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    let accessor_key = symbol(scope, "duplicate-accessor");
    template.set_accessor_property(
        accessor_key.into(),
        Some(v8::FunctionTemplate::new(scope, accessor_getter)),
        None,
        v8::PropertyAttribute::DONT_ENUM,
    );
    template.set_accessor_property(
        accessor_key.into(),
        Some(v8::FunctionTemplate::new(scope, accessor_getter_alt)),
        None,
        v8::PropertyAttribute::DONT_DELETE,
    );
    let native_key = symbol(scope, "duplicate-native");
    template.set_accessor(native_key.into(), native_getter);
    template.set_accessor_with_configuration(
        native_key.into(),
        v8::AccessorConfiguration::new(native_getter)
            .data(v8::Integer::new(scope, 99).into())
            .property_attribute(v8::PropertyAttribute::DONT_ENUM),
    );

    let function_template = v8::FunctionTemplate::new(scope, accessor_getter);
    let function_key = symbol(scope, "duplicate-function");
    function_template.set_accessor_property(
        function_key.into(),
        Some(v8::FunctionTemplate::new(scope, accessor_getter)),
        None,
        v8::PropertyAttribute::DONT_ENUM,
    );
    function_template.set_accessor_property(
        function_key.into(),
        Some(v8::FunctionTemplate::new(scope, accessor_getter_alt)),
        None,
        v8::PropertyAttribute::DONT_DELETE,
    );

    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let object = template.new_instance(scope).unwrap();
    let function = function_template.get_function(scope).unwrap();
    let accessor_value = object
        .get(scope, accessor_key.into())
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let native_value = object
        .get(scope, native_key.into())
        .unwrap()
        .integer_value(scope)
        .unwrap();
    let function_value = function
        .get(scope, function_key.into())
        .unwrap()
        .integer_value(scope)
        .unwrap();

    pass(
        "template-accessor-name-keys/duplicate/replacement",
        Json::obj(vec![
            ("object_accessor_value", Json::i(accessor_value)),
            (
                "object_accessor_attributes",
                attributes(
                    object
                        .get_property_attributes(scope, accessor_key.into())
                        .unwrap(),
                ),
            ),
            ("object_native_value", Json::i(native_value)),
            (
                "object_native_attributes",
                attributes(
                    object
                        .get_property_attributes(scope, native_key.into())
                        .unwrap(),
                ),
            ),
            ("function_accessor_value", Json::i(function_value)),
            (
                "function_accessor_attributes",
                attributes(
                    function
                        .get_property_attributes(scope, function_key.into())
                        .unwrap(),
                ),
            ),
        ]),
    )
}

fn negative(mode: &str) {
    suppress_windows_fatal_dialogs();
    oracle::ensure_v8();
    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let template = v8::ObjectTemplate::new(scope);
    match mode {
        "none-none-object" => {
            template.set_accessor_property(
                symbol(scope, "none").into(),
                None,
                None,
                v8::PropertyAttribute::NONE,
            );
        }
        "none-none-function" => {
            let function_template = v8::FunctionTemplate::new(scope, accessor_getter);
            function_template.set_accessor_property(
                symbol(scope, "none").into(),
                None,
                None,
                v8::PropertyAttribute::NONE,
            );
        }
        _ => panic!("unknown negative mode: {mode}"),
    }
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    eprintln!("marker:before-instantiation:{mode}");
    let object = template.new_instance(scope);
    eprintln!(
        "marker:after-instantiation:{mode}:some={}",
        object.is_some()
    );
}

fn run_fixture() {
    oracle::ensure_v8();
    let outcomes = [
        function_template_accessor_property(),
        object_template_accessor_property(),
        object_template_native_data_properties(),
        retention_and_post_publication(),
        duplicate_accessor_names(),
    ];
    for outcome in &outcomes {
        println!("{}", outcome.to_line());
    }
    println!("{}", summary_line(outcomes.len(), outcomes.len(), 0));
}

fn main() {
    let args: Vec<_> = std::env::args().collect();
    if args.len() == 3 && args[1] == "--negative" {
        negative(&args[2]);
    } else {
        assert_eq!(
            args.len(),
            1,
            "usage: conformance-template-accessor-name-keys [--negative MODE]"
        );
        run_fixture();
    }
}
