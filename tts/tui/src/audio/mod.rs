pub mod player;

#[allow(unused_imports)]
pub use player::bytes_to_i16;
pub use player::i16_to_bytes;
pub use player::AudioPlayer; // used by tests/audio_tests.rs via the lib target
