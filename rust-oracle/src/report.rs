//! Conformance check outcomes and their normalized JSON-lines encoding.
//!
//! Every check emits exactly one line:
//! - pass: `{"check":"<id>","ok":true,"value":<normalized value>}`
//! - fail: `{"check":"<id>","ok":false,"expected":<E>,"actual":<A>}`
//!
//! The runner appends a final summary line:
//! `{"summary":{"total":N,"passed":P,"failed":F}}`

use crate::json::Json;

pub struct CheckOutcome {
    pub id: &'static str,
    pub result: Outcome,
}

pub enum Outcome {
    /// Check succeeded; `value` is the normalized observation worth recording.
    Pass(Json),
    /// Check failed; both sides are recorded so mismatches are diffable.
    Fail { expected: Json, actual: Json },
}

pub fn pass(id: &'static str, value: Json) -> CheckOutcome {
    CheckOutcome {
        id,
        result: Outcome::Pass(value),
    }
}

/// Records an equality check between a fixed expectation and an observation.
pub fn expect_eq(id: &'static str, expected: Json, actual: Json) -> CheckOutcome {
    let outcome = if expected == actual {
        Outcome::Pass(actual)
    } else {
        Outcome::Fail { expected, actual }
    };
    CheckOutcome {
        id,
        result: outcome,
    }
}

impl CheckOutcome {
    pub fn passed(&self) -> bool {
        matches!(self.result, Outcome::Pass(_))
    }

    pub fn to_line(&self) -> String {
        let mut out = String::from("{\"check\":");
        crate::json::Json::s(self.id).write_json_into(&mut out);
        match &self.result {
            Outcome::Pass(value) => {
                out.push_str(",\"ok\":true,\"value\":");
                value.write_json_into(&mut out);
            }
            Outcome::Fail { expected, actual } => {
                out.push_str(",\"ok\":false,\"expected\":");
                expected.write_json_into(&mut out);
                out.push_str(",\"actual\":");
                actual.write_json_into(&mut out);
            }
        }
        out.push('}');
        out
    }
}

pub fn summary_line(total: usize, passed: usize, failed: usize) -> String {
    let value = Json::obj(vec![
        ("total", Json::i(total as i64)),
        ("passed", Json::i(passed as i64)),
        ("failed", Json::i(failed as i64)),
    ]);
    let mut out = String::from("{\"summary\":");
    value.write_json_into(&mut out);
    out.push('}');
    out
}
