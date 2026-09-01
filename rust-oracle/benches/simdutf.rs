//! Comparative benchmarks for the pinned public simdutf bindings.

mod common;

use common::{MEASUREMENT_TIME, SAMPLE_SIZE, WARM_UP_TIME};
use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use std::hint::black_box;

const INPUT_SIZE: usize = 4096;

fn validation(c: &mut Criterion) {
    let mut input = "Aé€😀".repeat(INPUT_SIZE / 10).into_bytes();
    input.resize(INPUT_SIZE, b'A');
    assert!(v8::simdutf::validate_utf8(&input));
    let mut group = c.benchmark_group("simdutf/validate_utf8");
    group.throughput(Throughput::Bytes(input.len() as u64));
    group.bench_function("mixed_4k", |b| {
        common::banner();
        b.iter(|| assert!(v8::simdutf::validate_utf8(black_box(&input))))
    });
    group.finish();
}

fn utf8_utf16(c: &mut Criterion) {
    let mut input = "Aé€😀".repeat(INPUT_SIZE / 10).into_bytes();
    input.resize(INPUT_SIZE, b'A');
    let expected_units = v8::simdutf::utf16_length_from_utf8(&input);
    let mut utf16 = vec![0_u16; input.len()];
    assert_eq!(
        unsafe { v8::simdutf::convert_utf8_to_utf16le(&input, &mut utf16) },
        expected_units
    );
    utf16.truncate(expected_units);

    let mut group = c.benchmark_group("simdutf/transcode");
    group.throughput(Throughput::Bytes(input.len() as u64));
    let mut to_utf16 = vec![0_u16; input.len()];
    group.bench_function("utf8_to_utf16le_4k", |b| {
        common::banner();
        b.iter(|| {
            let written =
                unsafe { v8::simdutf::convert_utf8_to_utf16le(black_box(&input), &mut to_utf16) };
            assert_eq!(written, expected_units);
            black_box(&to_utf16[..written]);
        })
    });
    let mut to_utf8 = vec![0_u8; utf16.len() * 3];
    group.bench_function("utf16le_to_utf8_4k", |b| {
        common::banner();
        b.iter(|| {
            let written =
                unsafe { v8::simdutf::convert_utf16le_to_utf8(black_box(&utf16), &mut to_utf8) };
            assert_eq!(written, input.len());
            black_box(&to_utf8[..written]);
        })
    });
    group.finish();
}

fn base64_decode(c: &mut Criterion) {
    let binary = vec![0x5a_u8; 3072];
    let encoded_len =
        v8::simdutf::base64_length_from_binary(binary.len(), v8::simdutf::Base64Options::Default);
    let mut encoded = vec![0_u8; encoded_len];
    assert_eq!(
        unsafe {
            v8::simdutf::binary_to_base64(
                &binary,
                &mut encoded,
                v8::simdutf::Base64Options::Default,
            )
        },
        encoded_len
    );
    let mut output = vec![0_u8; v8::simdutf::maximal_binary_length_from_base64(&encoded)];
    let probe = unsafe {
        v8::simdutf::base64_to_binary(
            &encoded,
            &mut output,
            v8::simdutf::Base64Options::Default,
            v8::simdutf::LastChunkHandling::Strict,
        )
    };
    assert!(probe.is_ok());
    assert_eq!(probe.count, binary.len());

    let mut group = c.benchmark_group("simdutf/base64_decode");
    group.throughput(Throughput::Bytes(encoded.len() as u64));
    group.bench_function("standard_4k", |b| {
        common::banner();
        b.iter(|| {
            let result = unsafe {
                v8::simdutf::base64_to_binary(
                    black_box(&encoded),
                    &mut output,
                    v8::simdutf::Base64Options::Default,
                    v8::simdutf::LastChunkHandling::Strict,
                )
            };
            assert!(result.is_ok());
            assert_eq!(result.count, binary.len());
            black_box(&output[..result.count]);
        })
    });
    group.finish();
}

criterion_group! {
    name = simdutf_benches;
    config = Criterion::default()
        .warm_up_time(WARM_UP_TIME)
        .measurement_time(MEASUREMENT_TIME)
        .sample_size(SAMPLE_SIZE);
    targets = validation, utf8_utf16, base64_decode
}

criterion_main!(simdutf_benches);
