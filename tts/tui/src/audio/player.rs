use anyhow::{Context, Result};
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use std::collections::VecDeque;
use std::sync::{Arc, Mutex};

pub struct AudioPlayer {
    /// Shared ring buffer of i16 samples.
    buffer: Arc<Mutex<VecDeque<i16>>>,
    /// cpal stream kept alive for as long as the player exists.
    _stream: Option<cpal::Stream>,
    #[allow(dead_code)]
    pub sample_rate: u32,
}

impl AudioPlayer {
    /// Create a new player and open the default output device at 24 kHz mono.
    pub fn new() -> Result<Self> {
        Self::with_sample_rate(24000)
    }

    pub fn with_sample_rate(sample_rate: u32) -> Result<Self> {
        let buffer: Arc<Mutex<VecDeque<i16>>> = Arc::new(Mutex::new(VecDeque::new()));
        let buf_clone = Arc::clone(&buffer);

        let host = cpal::default_host();
        let device = host
            .default_output_device()
            .context("no default output device")?;

        let config = cpal::StreamConfig {
            channels: 1,
            sample_rate: cpal::SampleRate(sample_rate),
            buffer_size: cpal::BufferSize::Default,
        };

        let stream = device
            .build_output_stream(
                &config,
                move |data: &mut [i16], _info: &cpal::OutputCallbackInfo| {
                    let mut buf = buf_clone.lock().unwrap();
                    for sample in data.iter_mut() {
                        *sample = buf.pop_front().unwrap_or(0);
                    }
                },
                |err| eprintln!("audio stream error: {}", err),
                None,
            )
            .context("building output stream")?;

        stream.play().context("starting audio stream")?;

        Ok(AudioPlayer {
            buffer,
            _stream: Some(stream),
            sample_rate,
        })
    }

    /// Push raw i16 LE bytes into the playback buffer.
    pub fn push_bytes(&self, bytes: &[u8]) {
        let samples = bytes_to_i16(bytes);
        let mut buf = self.buffer.lock().unwrap();
        buf.extend(samples);
    }

    /// Push i16 samples directly.
    #[allow(dead_code)]
    pub fn push_samples(&self, samples: &[i16]) {
        let mut buf = self.buffer.lock().unwrap();
        buf.extend(samples.iter().copied());
    }

    /// Number of samples currently queued.
    #[allow(dead_code)]
    pub fn buffered_samples(&self) -> usize {
        self.buffer.lock().unwrap().len()
    }

    /// Clear the playback buffer (instant stop).
    pub fn stop(&self) {
        self.buffer.lock().unwrap().clear();
    }

    /// Approximate remaining playback duration in seconds.
    #[allow(dead_code)]
    pub fn remaining_seconds(&self) -> f64 {
        self.buffered_samples() as f64 / self.sample_rate as f64
    }
}

/// Convert raw little-endian i16 bytes to i16 samples.
pub fn bytes_to_i16(bytes: &[u8]) -> Vec<i16> {
    bytes
        .chunks_exact(2)
        .map(|c| i16::from_le_bytes([c[0], c[1]]))
        .collect()
}

/// Convert i16 samples to little-endian bytes.
pub fn i16_to_bytes(samples: &[i16]) -> Vec<u8> {
    samples.iter().flat_map(|s| s.to_le_bytes()).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Buffer-only test — does not open an audio device.
    struct MockBuffer {
        buf: Arc<Mutex<VecDeque<i16>>>,
    }

    impl MockBuffer {
        fn new() -> Self {
            MockBuffer {
                buf: Arc::new(Mutex::new(VecDeque::new())),
            }
        }

        fn push_samples(&self, samples: &[i16]) {
            self.buf.lock().unwrap().extend(samples.iter().copied());
        }

        fn push_bytes(&self, bytes: &[u8]) {
            let samples = bytes_to_i16(bytes);
            self.buf.lock().unwrap().extend(samples);
        }

        fn drain(&self, n: usize) -> Vec<i16> {
            let mut buf = self.buf.lock().unwrap();
            (0..n).map(|_| buf.pop_front().unwrap_or(0)).collect()
        }

        fn len(&self) -> usize {
            self.buf.lock().unwrap().len()
        }

        fn clear(&self) {
            self.buf.lock().unwrap().clear();
        }
    }

    #[test]
    fn push_and_drain_samples() {
        let b = MockBuffer::new();
        b.push_samples(&[100, 200, 300]);
        assert_eq!(b.len(), 3);
        let out = b.drain(3);
        assert_eq!(out, vec![100, 200, 300]);
        assert_eq!(b.len(), 0);
    }

    #[test]
    fn underrun_fills_silence() {
        let b = MockBuffer::new();
        b.push_samples(&[42]);
        let out = b.drain(5);
        assert_eq!(out[0], 42);
        assert_eq!(out[1], 0); // silence
        assert_eq!(out[4], 0);
    }

    #[test]
    fn push_bytes_roundtrip() {
        let samples: Vec<i16> = vec![-32768, 0, 32767, 100, -100];
        let bytes = i16_to_bytes(&samples);
        let b = MockBuffer::new();
        b.push_bytes(&bytes);
        let out = b.drain(samples.len());
        assert_eq!(out, samples);
    }

    #[test]
    fn clear_empties_buffer() {
        let b = MockBuffer::new();
        b.push_samples(&[1, 2, 3, 4, 5]);
        b.clear();
        assert_eq!(b.len(), 0);
    }

    #[test]
    fn bytes_to_i16_odd_bytes_ignored() {
        // 5 bytes → only 2 samples (last byte ignored)
        let bytes: Vec<u8> = vec![0x01, 0x00, 0x02, 0x00, 0xFF];
        let samples = bytes_to_i16(&bytes);
        assert_eq!(samples.len(), 2);
        assert_eq!(samples[0], 1);
        assert_eq!(samples[1], 2);
    }

    #[test]
    fn bytes_i16_conversion_endianness() {
        // 0x0100 in LE = 256
        let bytes: Vec<u8> = vec![0x00, 0x01];
        let samples = bytes_to_i16(&bytes);
        assert_eq!(samples[0], 256);
    }

    #[test]
    fn i16_to_bytes_little_endian() {
        let samples = vec![256i16];
        let bytes = i16_to_bytes(&samples);
        assert_eq!(bytes, vec![0x00, 0x01]);
    }

    #[test]
    fn multiple_pushes_accumulate() {
        let b = MockBuffer::new();
        b.push_samples(&[1, 2]);
        b.push_samples(&[3, 4]);
        assert_eq!(b.len(), 4);
        let out = b.drain(4);
        assert_eq!(out, vec![1, 2, 3, 4]);
    }
}
