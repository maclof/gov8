//! Process-global ICU API conformance for pinned `v8` 152.2.0.

use oracle::json::Json;
use oracle::report::{pass, summary_line, CheckOutcome};

#[repr(align(16))]
struct AlignedInvalid([u8; 16]);

static INVALID_COMMON_DATA: AlignedInvalid =
    AlignedInvalid([1, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);

struct RestoreLocale(String);

impl Drop for RestoreLocale {
    fn drop(&mut self) {
        v8::icu::set_default_locale(&self.0);
    }
}

struct RestoreTimeZone(String);

impl Drop for RestoreTimeZone {
    fn drop(&mut self) {
        if !v8::icu::set_default_time_zone(&self.0) {
            let _ = v8::icu::set_default_time_zone("UTC");
        }
    }
}

fn result_json(result: Result<(), i32>) -> Json {
    match result {
        Ok(()) => Json::obj(vec![("ok", Json::b(true)), ("error", Json::Null)]),
        Err(error) => Json::obj(vec![
            ("ok", Json::b(false)),
            ("error", Json::i(i64::from(error))),
        ]),
    }
}

fn common_data_invalid() -> Vec<CheckOutcome> {
    let bytes: &'static [u8] = &INVALID_COMMON_DATA.0;
    let first = v8::icu::set_common_data_78(bytes);
    let second = v8::icu::set_common_data_78(bytes);
    vec![pass(
        "icu/common_data_invalid",
        Json::obj(vec![
            (
                "pointer_mod_16",
                Json::i((bytes.as_ptr() as usize % 16) as i64),
            ),
            ("length", Json::i(bytes.len() as i64)),
            ("first", result_json(first)),
            ("repeat", result_json(second)),
        ]),
    )]
}

fn locale_roundtrip_and_restore() -> Vec<CheckOutcome> {
    let original = v8::icu::get_language_tag();
    let _restore = RestoreLocale(original.clone());

    v8::icu::set_default_locale("nb_NO");
    let underscore = v8::icu::get_language_tag();
    v8::icu::set_default_locale("en_US_POSIX");
    let posix = v8::icu::get_language_tag();
    v8::icu::set_default_locale("fr-FR");
    let bcp47 = v8::icu::get_language_tag();
    v8::icu::set_default_locale("");
    let empty = v8::icu::get_language_tag();
    v8::icu::set_default_locale("zz_ZZ");
    let unknown_but_well_formed = v8::icu::get_language_tag();
    v8::icu::set_default_locale("@");
    let malformed = v8::icu::get_language_tag();
    v8::icu::set_default_locale(&"a".repeat(2048));
    let overlong = v8::icu::get_language_tag();

    v8::icu::set_default_locale(&original);
    let restored = v8::icu::get_language_tag();
    vec![pass(
        "icu/locale_roundtrip_and_restore",
        Json::obj(vec![
            (
                "host_original_normalized",
                Json::obj(vec![
                    ("nonempty", Json::b(!original.is_empty())),
                    ("contains_nul", Json::b(original.contains('\0'))),
                ]),
            ),
            ("nb_NO", Json::s(&underscore)),
            ("en_US_POSIX", Json::s(&posix)),
            ("fr-FR", Json::s(&bcp47)),
            ("empty", Json::s(&empty)),
            ("zz_ZZ", Json::s(&unknown_but_well_formed)),
            ("malformed", Json::s(&malformed)),
            ("overlong", Json::s(&overlong)),
            ("restored_exactly", Json::b(restored == original)),
        ]),
    )]
}

fn time_zone_validation_and_restore() -> Vec<CheckOutcome> {
    let original = v8::icu::get_default_time_zone();
    let _restore = RestoreTimeZone(original.clone());
    let before = v8::icu::get_default_time_zone();

    let invalid = v8::icu::set_default_time_zone("Not/AZone");
    let unchanged_after_invalid = v8::icu::get_default_time_zone() == before;
    let empty = v8::icu::set_default_time_zone("");
    let unchanged_after_empty = v8::icu::get_default_time_zone() == before;
    let nul = v8::icu::set_default_time_zone("America/New\0_York");
    let unchanged_after_nul = v8::icu::get_default_time_zone() == before;
    let unknown = v8::icu::set_default_time_zone("Etc/Unknown");
    let unchanged_after_unknown = v8::icu::get_default_time_zone() == before;

    let utc_set = v8::icu::set_default_time_zone("UTC");
    let utc = v8::icu::get_default_time_zone();
    let iana_set = v8::icu::set_default_time_zone("America/New_York");
    let iana = v8::icu::get_default_time_zone();
    let offset_set = v8::icu::set_default_time_zone("GMT+05:00");
    let offset = v8::icu::get_default_time_zone();
    let overlong_set = v8::icu::set_default_time_zone(&"A".repeat(2048));
    let unchanged_after_overlong = v8::icu::get_default_time_zone() == offset;

    let direct_restore = v8::icu::set_default_time_zone(&original);
    if !direct_restore {
        let _ = v8::icu::set_default_time_zone("UTC");
    }
    let restored = v8::icu::get_default_time_zone();
    let restore_matches = if direct_restore {
        restored == original
    } else {
        restored == "UTC"
    };

    vec![pass(
        "icu/time_zone_validation_and_restore",
        Json::obj(vec![
            (
                "host_original_normalized",
                Json::obj(vec![
                    ("nonempty", Json::b(!original.is_empty())),
                    ("contains_nul", Json::b(original.contains('\0'))),
                ]),
            ),
            (
                "invalid",
                Json::obj(vec![
                    ("accepted", Json::b(invalid)),
                    ("unchanged", Json::b(unchanged_after_invalid)),
                ]),
            ),
            (
                "empty",
                Json::obj(vec![
                    ("accepted", Json::b(empty)),
                    ("unchanged", Json::b(unchanged_after_empty)),
                ]),
            ),
            (
                "interior_nul",
                Json::obj(vec![
                    ("accepted", Json::b(nul)),
                    ("unchanged", Json::b(unchanged_after_nul)),
                ]),
            ),
            (
                "icu_unknown",
                Json::obj(vec![
                    ("accepted", Json::b(unknown)),
                    ("unchanged", Json::b(unchanged_after_unknown)),
                ]),
            ),
            (
                "utc",
                Json::obj(vec![
                    ("accepted", Json::b(utc_set)),
                    ("value", Json::s(&utc)),
                ]),
            ),
            (
                "iana",
                Json::obj(vec![
                    ("accepted", Json::b(iana_set)),
                    ("value", Json::s(&iana)),
                ]),
            ),
            (
                "custom_offset",
                Json::obj(vec![
                    ("accepted", Json::b(offset_set)),
                    ("value", Json::s(&offset)),
                ]),
            ),
            (
                "overlong_invalid",
                Json::obj(vec![
                    ("accepted", Json::b(overlong_set)),
                    ("unchanged", Json::b(unchanged_after_overlong)),
                ]),
            ),
            ("direct_restore_accepted", Json::b(direct_restore)),
            ("restore_matches_normalized", Json::b(restore_matches)),
        ]),
    )]
}

fn main() -> std::process::ExitCode {
    let mut checks = common_data_invalid();
    oracle::ensure_v8();
    checks.extend(locale_roundtrip_and_restore());
    checks.extend(time_zone_validation_and_restore());
    let passed = checks.iter().filter(|check| check.passed()).count();
    for check in &checks {
        println!("{}", check.to_line());
    }
    println!(
        "{}",
        summary_line(checks.len(), passed, checks.len() - passed)
    );
    if passed == checks.len() {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
