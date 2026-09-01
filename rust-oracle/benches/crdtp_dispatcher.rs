//! CRDTP synchronous dispatcher success-path benchmark.
//!
//! Channel, dispatcher, domain wiring, JSON-to-CBOR conversion, and
//! Dispatchable construction are setup and remain outside measurement. Each
//! timed iteration dispatches the same parsed request, invokes the Rust domain
//! callback, creates a success response, synchronously delivers it to the Rust
//! channel, serializes it to CBOR, converts it to JSON for exact validation,
//! increments both callback counters, and verifies one delivery of each kind.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion};
use std::cell::Cell;
use std::hint::black_box;
use std::rc::Rc;

const REQUEST_JSON: &[u8] = br#"{"id":1,"method":"Bench.ok","params":{}}"#;
const RESPONSE_JSON: &[u8] = br#"{"id":1,"result":{}}"#;

struct ValidatingChannel {
    deliveries: Rc<Cell<u64>>,
}

impl v8::crdtp::FrontendChannelImpl for ValidatingChannel {
    fn send_protocol_response(&mut self, call_id: i32, message: v8::crdtp::Serializable) {
        assert_eq!(call_id, 1);
        let cbor = message.to_bytes();
        let json = v8::crdtp::cbor_to_json(&cbor).expect("response must be valid CRDTP CBOR");
        assert_eq!(json, RESPONSE_JSON);
        self.deliveries.set(self.deliveries.get() + 1);
    }

    fn send_protocol_notification(&mut self, _message: v8::crdtp::Serializable) {
        panic!("success response must not be delivered as a notification");
    }

    fn flush_protocol_notifications(&mut self) {
        panic!("success response must not flush notifications");
    }
}

struct SuccessDomain {
    callbacks: Rc<Cell<u64>>,
}

impl v8::crdtp::DomainDispatcherImpl for SuccessDomain {
    fn dispatch(
        &mut self,
        command: &[u8],
        dispatchable: &v8::crdtp::Dispatchable,
        handle: &v8::crdtp::DomainDispatcherHandle,
    ) -> bool {
        assert_eq!(command, b"ok");
        let call_id = dispatchable.call_id();
        assert_eq!(call_id, 1);
        self.callbacks.set(self.callbacks.get() + 1);
        handle.send_response(call_id, v8::crdtp::DispatchResponse::success(), None);
        true
    }
}

fn dispatch_success(c: &mut Criterion) {
    let callbacks = Rc::new(Cell::new(0));
    let deliveries = Rc::new(Cell::new(0));
    let channel = v8::crdtp::FrontendChannel::new(Box::new(ValidatingChannel {
        deliveries: deliveries.clone(),
    }));
    let mut dispatcher = v8::crdtp::UberDispatcher::new(&channel);
    v8::crdtp::DomainDispatcher::wire(
        &mut dispatcher,
        "Bench",
        Box::new(SuccessDomain {
            callbacks: callbacks.clone(),
        }),
    );

    let cbor = v8::crdtp::json_to_cbor(REQUEST_JSON).expect("request JSON must convert to CBOR");
    let mut dispatchable = v8::crdtp::Dispatchable::new(&cbor);
    assert!(dispatchable.ok());
    assert_eq!(dispatchable.call_id(), 1);
    assert_eq!(dispatchable.method(), b"Bench.ok");

    c.bench_function("crdtp_dispatcher/dispatch_success", |b| {
        common::banner();
        b.iter(|| {
            let callbacks_before = callbacks.get();
            let deliveries_before = deliveries.get();

            dispatcher.dispatch(black_box(&mut dispatchable));

            assert_eq!(callbacks.get(), callbacks_before + 1);
            assert_eq!(deliveries.get(), deliveries_before + 1);
        });
    });

    assert!(callbacks.get() > 0);
    assert_eq!(callbacks.get(), deliveries.get());
}

criterion_group! {
    name = crdtp_dispatcher_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = dispatch_success
}

criterion_main!(crdtp_dispatcher_benches);
