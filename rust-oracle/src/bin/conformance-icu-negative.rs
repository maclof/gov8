//! Potentially fatal or panicking ICU inputs, invoked only in subprocesses.

#[repr(align(16))]
struct Aligned([u8; 16]);

static DATA: Aligned = Aligned([1, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);

#[repr(align(16))]
struct AlignedIcuData([u8; 1_806_192]);

static VALID_ICU_DATA: AlignedIcuData = AlignedIcuData(*include_bytes!(
    "../../tests/fixtures/icu/icudtl-flutter-icu78.dat"
));

fn valid_common_data() {
    let data: &'static [u8] = &VALID_ICU_DATA.0;
    let result = v8::icu::set_common_data_78(data);
    if result.is_err() {
        println!("common={result:?};align={}", data.as_ptr() as usize % 16);
        return;
    }
    oracle::ensure_v8();
    v8::icu::set_default_locale("nb_NO");
    let locale = v8::icu::get_language_tag();
    let time_zone_set = v8::icu::set_default_time_zone("UTC");
    let time_zone = v8::icu::get_default_time_zone();

    let isolate = &mut v8::Isolate::new(Default::default());
    v8::scope!(let scope, isolate);
    let context = v8::Context::new(scope, Default::default());
    let scope = &mut v8::ContextScope::new(scope, context);
    let source = v8::String::new(scope, "new Intl.NumberFormat('en-US').format(1234.5)").unwrap();
    let intl = v8::Script::compile(scope, source, None)
        .unwrap()
        .run(scope)
        .unwrap()
        .to_rust_string_lossy(scope);
    println!(
        "common={result:?};align={};locale={locale};timezone_set={time_zone_set};timezone={time_zone};intl={intl}",
        data.as_ptr() as usize % 16
    );
}

fn main() {
    match std::env::args().nth(1).as_deref() {
        Some("common-data-valid") => valid_common_data(),
        Some("locale-interior-nul") => {
            oracle::ensure_v8();
            v8::icu::set_default_locale("en\0US");
        }
        Some("locale-overlong") => {
            oracle::ensure_v8();
            v8::icu::set_default_locale(&"a".repeat(2048));
            println!("{}", v8::icu::get_language_tag());
        }
        Some("locale-malformed") => {
            oracle::ensure_v8();
            v8::icu::set_default_locale("@");
            println!("{}", v8::icu::get_language_tag());
        }
        Some("common-data-empty") => {
            println!("{:?}", v8::icu::set_common_data_78(&[]));
        }
        Some("common-data-misaligned") => {
            println!("{:?}", v8::icu::set_common_data_78(&DATA.0[1..]));
        }
        mode => panic!("unknown ICU negative mode: {mode:?}"),
    }
}
