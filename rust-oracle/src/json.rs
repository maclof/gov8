//! Minimal canonical JSON value used for deterministic normalized output.
//!
//! Cross-language normalization rules (Go must implement the same rules):
//! - Object keys are emitted in insertion order; no reordering, no whitespace.
//! - Strings: minimal escaping (`\"`, `\\`, `\n`, `\r`, `\t`, `\b`, `\f`,
//!   `\u00XX` for control characters below 0x20); all other code points are
//!   emitted as raw UTF-8.
//! - Integers: plain decimal.
//! - Floats: shortest round-trip plain-decimal notation, never exponent
//!   notation, no trailing `.0` (Rust `Display for f64` semantics). Checks
//!   must only use floats with magnitude in [1e-4, 1e15) so that plain-decimal
//!   and shortest representations agree across languages; non-finite values
//!   must be encoded as the strings "NaN", "Infinity", "-Infinity".

use std::fmt::Write as _;

#[derive(Debug, Clone, PartialEq)]
pub enum Json {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    Str(String),
    Array(Vec<Json>),
    /// Key order is preserved; keys are static so output is deterministic.
    Object(Vec<(&'static str, Json)>),
}

impl Json {
    pub fn obj(pairs: Vec<(&'static str, Json)>) -> Json {
        Json::Object(pairs)
    }

    pub fn arr(items: Vec<Json>) -> Json {
        Json::Array(items)
    }

    pub fn s(value: &str) -> Json {
        Json::Str(value.to_owned())
    }

    pub fn i(value: i64) -> Json {
        Json::Int(value)
    }

    pub fn f(value: f64) -> Json {
        debug_assert!(
            value.is_finite() && (value == 0.0 || !(value.abs() < 1e-4 || value.abs() >= 1e15)),
            "float outside the documented plain-decimal range: {value}"
        );
        Json::Float(value)
    }

    pub fn b(value: bool) -> Json {
        Json::Bool(value)
    }

    /// Serializes to canonical JSON text.
    pub fn to_json_string(&self) -> String {
        let mut out = String::new();
        self.write_json_into(&mut out);
        out
    }

    /// Appends the canonical JSON text to an existing buffer.
    pub fn write_json_into(&self, out: &mut String) {
        self.write(out);
    }

    fn write(&self, out: &mut String) {
        match self {
            Json::Null => out.push_str("null"),
            Json::Bool(true) => out.push_str("true"),
            Json::Bool(false) => out.push_str("false"),
            Json::Int(v) => {
                let _ = write!(out, "{v}");
            }
            Json::Float(v) => {
                let _ = write!(out, "{v}");
            }
            Json::Str(s) => write_escaped(s, out),
            Json::Array(items) => {
                out.push('[');
                for (idx, item) in items.iter().enumerate() {
                    if idx > 0 {
                        out.push(',');
                    }
                    item.write(out);
                }
                out.push(']');
            }
            Json::Object(pairs) => {
                out.push('{');
                for (idx, (key, value)) in pairs.iter().enumerate() {
                    if idx > 0 {
                        out.push(',');
                    }
                    write_escaped(key, out);
                    out.push(':');
                    value.write(out);
                }
                out.push('}');
            }
        }
    }
}

fn write_escaped(value: &str, out: &mut String) {
    out.push('"');
    for ch in value.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{08}' => out.push_str("\\b"),
            '\u{0C}' => out.push_str("\\f"),
            c if (c as u32) < 0x20 => {
                let _ = write!(out, "\\u{:04x}", c as u32);
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

#[cfg(test)]
mod tests {
    use super::Json;

    #[test]
    fn scalars() {
        assert_eq!(Json::Null.to_json_string(), "null");
        assert_eq!(Json::Bool(true).to_json_string(), "true");
        assert_eq!(Json::Bool(false).to_json_string(), "false");
        assert_eq!(Json::Int(0).to_json_string(), "0");
        assert_eq!(Json::Int(-42).to_json_string(), "-42");
        assert_eq!(Json::Int(i64::MIN).to_json_string(), "-9223372036854775808");
    }

    #[test]
    fn floats_use_plain_shortest_decimal() {
        assert_eq!(Json::f(2.5).to_json_string(), "2.5");
        assert_eq!(Json::f(42.0).to_json_string(), "42");
        assert_eq!(Json::f(-1234.5).to_json_string(), "-1234.5");
        assert_eq!(Json::f(0.5).to_json_string(), "0.5");
        assert_eq!(Json::f(12345.678901234).to_json_string(), "12345.678901234");
    }

    #[test]
    fn string_escaping() {
        assert_eq!(Json::s("plain").to_json_string(), "\"plain\"");
        assert_eq!(Json::s("a\"b").to_json_string(), "\"a\\\"b\"");
        assert_eq!(Json::s("a\\b").to_json_string(), "\"a\\\\b\"");
        assert_eq!(Json::s("a\nb").to_json_string(), "\"a\\nb\"");
        assert_eq!(Json::s("a\tb").to_json_string(), "\"a\\tb\"");
        assert_eq!(Json::s("\u{01}").to_json_string(), "\"\\u0001\"");
        // Non-ASCII is emitted as raw UTF-8, not \u escapes.
        assert_eq!(
            Json::s("h\u{e9}llo \u{1f980}").to_json_string(),
            "\"h\u{e9}llo \u{1f980}\""
        );
    }

    #[test]
    fn containers_preserve_order_without_whitespace() {
        let value = Json::obj(vec![
            ("z", Json::i(1)),
            ("a", Json::arr(vec![Json::Null, Json::b(true)])),
        ]);
        assert_eq!(value.to_json_string(), "{\"z\":1,\"a\":[null,true]}");
    }
}
