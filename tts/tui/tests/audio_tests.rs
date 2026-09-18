use ratts_lib::audio::{bytes_to_i16, i16_to_bytes};
use std::collections::VecDeque;

// ── Pure conversion tests (no audio device needed) ────────────────────────────

#[test]
fn bytes_to_i16_basic() {
    let bytes: Vec<u8> = vec![0x00, 0x00, 0x01, 0x00, 0xFF, 0x7F];
    let samples = bytes_to_i16(&bytes);
    assert_eq!(samples, vec![0, 1, 0x7FFF]);
}

#[test]
fn bytes_to_i16_negative() {
    // 0xFFFF in LE = -1 as i16
    let bytes: Vec<u8> = vec![0xFF, 0xFF];
    let samples = bytes_to_i16(&bytes);
    assert_eq!(samples[0], -1i16);
}

#[test]
fn bytes_to_i16_min_max() {
    let min = i16::MIN;
    let max = i16::MAX;
    let bytes: Vec<u8> = vec![
        (min as u16) as u8,
        ((min as u16) >> 8) as u8,
        (max as u16) as u8,
        ((max as u16) >> 8) as u8,
    ];
    let samples = bytes_to_i16(&bytes);
    assert_eq!(samples[0], min);
    assert_eq!(samples[1], max);
}

#[test]
fn i16_to_bytes_roundtrip() {
    let original: Vec<i16> = vec![-32768, -1, 0, 1, 32767];
    let bytes = i16_to_bytes(&original);
    let recovered = bytes_to_i16(&bytes);
    assert_eq!(recovered, original);
}

#[test]
fn i16_to_bytes_length() {
    let samples: Vec<i16> = vec![0, 1, 2, 3, 4];
    let bytes = i16_to_bytes(&samples);
    assert_eq!(bytes.len(), samples.len() * 2);
}

#[test]
fn bytes_to_i16_odd_input_truncated() {
    let bytes = vec![0x01u8, 0x00, 0x02]; // 3 bytes → 1 sample
    let samples = bytes_to_i16(&bytes);
    assert_eq!(samples.len(), 1);
    assert_eq!(samples[0], 1);
}

#[test]
fn empty_bytes_gives_empty_samples() {
    let bytes: Vec<u8> = vec![];
    let samples = bytes_to_i16(&bytes);
    assert!(samples.is_empty());
}

// ── Ring-buffer logic tests ───────────────────────────────────────────────────

struct MockRing {
    buf: VecDeque<i16>,
}

impl MockRing {
    fn new() -> Self {
        MockRing {
            buf: VecDeque::new(),
        }
    }

    fn push_samples(&mut self, s: &[i16]) {
        self.buf.extend(s.iter().copied());
    }

    fn drain(&mut self, n: usize) -> Vec<i16> {
        (0..n).map(|_| self.buf.pop_front().unwrap_or(0)).collect()
    }
}

#[test]
fn ring_push_and_drain() {
    let mut r = MockRing::new();
    r.push_samples(&[10, 20, 30]);
    let out = r.drain(3);
    assert_eq!(out, vec![10, 20, 30]);
    assert!(r.buf.is_empty());
}

#[test]
fn ring_underrun_silence() {
    let mut r = MockRing::new();
    r.push_samples(&[42]);
    let out = r.drain(3);
    assert_eq!(out[0], 42);
    assert_eq!(out[1], 0);
    assert_eq!(out[2], 0);
}

#[test]
fn ring_multiple_pushes_ordered() {
    let mut r = MockRing::new();
    r.push_samples(&[1, 2]);
    r.push_samples(&[3, 4]);
    let out = r.drain(4);
    assert_eq!(out, vec![1, 2, 3, 4]);
}

#[test]
fn ring_drain_partial_then_more() {
    let mut r = MockRing::new();
    r.push_samples(&[1, 2, 3, 4, 5]);
    let first = r.drain(3);
    assert_eq!(first, vec![1, 2, 3]);
    r.push_samples(&[6, 7]);
    let second = r.drain(4);
    assert_eq!(second, vec![4, 5, 6, 7]);
}
