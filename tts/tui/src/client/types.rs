use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Language-grouped voice list from /v1/voices/languages
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LanguageVoicesResponse {
    pub languages: HashMap<String, LanguageVoiceGroup>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LanguageVoiceGroup {
    pub engine: String,
    pub voices: Vec<LanguageVoice>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LanguageVoice {
    pub id: String,
    pub name: String,
    pub gender: String,
}

/// One message from the JSON chunked stream (`/v1/tts/stream`). The TUI plays
/// the raw PCM endpoint instead, so this is exercised by tests rather than by
/// the render loop.
#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum StreamEvent {
    #[serde(rename = "metadata")]
    Metadata {
        total_chunks: u32,
        voice: String,
        speed: f32,
        original_text_length: usize,
    },
    #[serde(rename = "audio_chunk")]
    AudioChunk {
        chunk_index: u32,
        total_chunks: u32,
        text: String,
        audio: String, // base64 WAV
        sample_rate: u32,
        duration: f64,
        is_first_chunk: bool,
        is_last_chunk: bool,
    },
    #[serde(rename = "complete")]
    Complete {
        total_chunks_sent: u32,
        message: String,
    },
    #[serde(rename = "error")]
    Error { error: String },
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TtsRequest {
    pub text: String,
    pub voice: String,
    pub speed: f32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub lang: Option<String>,
    /// Stretch quality. Absent means the server picks by its own hardware, which
    /// is what "auto" sends: nothing.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub quality: Option<String>,
    // No engine field: a voice belongs to exactly one engine, so naming one is
    // not a choice a caller can meaningfully make. The server resolves it from
    // the voice.
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StreamTtsRequest {
    pub text: String,
    pub voice: String,
    pub speed: f32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub chunk_size: Option<u32>,
    /// As on TtsRequest: absent means the server's own default.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub quality: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TtsResponse {
    pub audio: String, // base64 WAV
    pub text: String,
    pub voice: String,
    pub speed: f32,
    pub sampling_rate: u32,
    pub duration: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthResponse {
    /// "gpu" or "cpu", as the backend actually resolved it. Absent when talking
    /// to an older backend, so the field is optional.
    #[serde(default)]
    pub device: Option<String>,
    /// The GPU's name when there is one, for the header's tooltip-ish detail.
    #[serde(default)]
    pub gpu_name: Option<String>,
    pub status: String,
    pub model_loaded: bool,
    pub engine: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Voice {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub lang: String,
    #[serde(default)]
    pub gender: String,
    #[serde(default)]
    pub grade: String,
    /// Which engine speaks it, for the "why is this slow" question. Absent from
    /// older backends.
    #[serde(default)]
    pub engine: String,
}

impl Voice {
    pub fn grade_icon(&self) -> &'static str {
        if self.grade == "A" {
            "🏆"
        } else {
            "🥈"
        }
    }

    pub fn gender_icon(&self) -> &'static str {
        if self.gender == "f" {
            "♀"
        } else {
            "♂"
        }
    }

    /// Where the voice comes from, as a short label.
    ///
    /// Read from the voice rather than assumed. This used to answer "US" for
    /// everything that was not British English, which labelled the Swedish voice
    /// "US" — the one thing the label exists to get right.
    pub fn region_label(&self) -> String {
        // Piper ids carry the locale: sv_SE-lisa-medium is Swedish (Sweden).
        if let Some((locale, _)) = self.id.split_once('-') {
            if let Some((_, region)) = locale.split_once('_') {
                if !region.is_empty() {
                    return region.to_ascii_uppercase();
                }
            }
        }
        // Kokoro ids say it in the first letter: af_* American, bf_*/bm_* British.
        match self.lang.as_str() {
            "en-gb" => "UK".to_string(),
            "en" | "en-us" => "US".to_string(),
            // A cloned voice is not from anywhere in particular.
            "auto" | "" => "—".to_string(),
            other => other.to_ascii_uppercase(),
        }
    }

    pub fn display_label(&self) -> String {
        format!("{} {}", self.grade_icon(), self.name)
    }
}

/// A message an agent or a shell left for the user to hear.
#[derive(Debug, Clone, Deserialize)]
pub struct Notification {
    #[serde(default)]
    pub text: String,
    /// say / summary / question / ping. Shown as it arrived, never interpreted:
    /// the client's job is to say what it was given.
    #[serde(default)]
    pub kind: String,
    /// Where it came from — an extension name, a hostname.
    #[serde(default)]
    pub source: String,
}

impl Notification {
    /// How it reads in the notice area. The source is worth knowing, and a notice
    /// without one is still worth showing.
    pub fn display_line(&self) -> String {
        let mut line = String::new();
        if !self.kind.is_empty() {
            line.push_str(&format!("[{}] ", self.kind));
        }
        line.push_str(&self.text);
        if !self.source.is_empty() {
            line.push_str(&format!(" ({})", self.source));
        }
        line
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct NotificationsResponse {
    #[serde(default)]
    pub items: Vec<Notification>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VoicesResponse {
    pub voices: Vec<Voice>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tts_request_roundtrip() {
        let req = TtsRequest {
            text: "Hello world".into(),
            voice: "voice_a".into(),
            speed: 1.0,
            lang: None,

            quality: None,
        };
        let json = serde_json::to_string(&req).unwrap();
        let parsed: TtsRequest = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.text, req.text);
        assert_eq!(parsed.voice, req.voice);
        assert!((parsed.speed - req.speed).abs() < f32::EPSILON);
    }

    #[test]
    fn stream_tts_request_omits_none_chunk_size() {
        let req = StreamTtsRequest {
            text: "Hello".into(),
            voice: "voice_a".into(),
            speed: 1.0,
            chunk_size: None,

            quality: None,
        };
        let json = serde_json::to_string(&req).unwrap();
        assert!(
            !json.contains("chunk_size"),
            "chunk_size should be omitted when None"
        );
    }

    #[test]
    fn stream_event_metadata_roundtrip() {
        let evt = StreamEvent::Metadata {
            total_chunks: 3,
            voice: "voice_a".into(),
            speed: 1.2,
            original_text_length: 100,
        };
        let json = serde_json::to_string(&evt).unwrap();
        let parsed: StreamEvent = serde_json::from_str(&json).unwrap();
        match parsed {
            StreamEvent::Metadata { total_chunks, .. } => assert_eq!(total_chunks, 3),
            _ => panic!("wrong variant"),
        }
    }

    #[test]
    fn stream_event_error_roundtrip() {
        let evt = StreamEvent::Error {
            error: "oops".into(),
        };
        let json = serde_json::to_string(&evt).unwrap();
        let parsed: StreamEvent = serde_json::from_str(&json).unwrap();
        match parsed {
            StreamEvent::Error { error } => assert_eq!(error, "oops"),
            _ => panic!("wrong variant"),
        }
    }

    #[test]
    fn region_label_comes_from_the_voice() {
        let voice = |id: &str, lang: &str| Voice {
            id: id.into(),
            name: "n".into(),
            lang: lang.into(),
            gender: "f".into(),
            grade: "B".into(),
            engine: "kokoro".into(),
        };

        // Piper ids carry their locale, and a Swedish one is not American.
        assert_eq!(voice("sv_SE-lisa-medium", "sv").region_label(), "SE");
        assert_eq!(voice("en_US-amy-low", "en").region_label(), "US");
        assert_eq!(voice("de_DE-thorsten-medium", "de").region_label(), "DE");

        // Kokoro ids say it in the first letter.
        assert_eq!(voice("af_heart", "en").region_label(), "US");
        assert_eq!(voice("bm_george", "en-gb").region_label(), "UK");

        // Anything else falls back to its own language code rather than to US.
        assert_eq!(voice("ja_JP-foo-medium", "ja").region_label(), "JP");
        assert_eq!(voice("plain", "ja").region_label(), "JA");

        // A clone is from nowhere in particular.
        assert_eq!(voice("morgan", "auto").region_label(), "—");
    }

    #[test]
    fn automatic_quality_sends_nothing_and_a_chosen_one_is_sent() {
        let req = TtsRequest {
            text: "hi".into(),
            voice: "af_heart".into(),
            speed: 1.0,
            lang: None,
            quality: None,
        };
        let json = serde_json::to_string(&req).unwrap();
        assert!(!json.contains("quality"), "{json}");

        let chosen = TtsRequest {
            quality: Some("high".into()),
            ..req
        };
        assert!(serde_json::to_string(&chosen).unwrap().contains("high"));
    }

    #[test]
    fn a_notification_reads_with_its_source_and_without_one() {
        let full = serde_json::from_str::<Notification>(
            r#"{"id":3,"text":"the tests are green","kind":"summary","source":"ci"}"#,
        )
        .unwrap();
        assert_eq!(full.display_line(), "[summary] the tests are green (ci)");

        // A minimal producer may send only text, and it must still be shown: a
        // notice that cannot be read is the same as no notice.
        let bare = serde_json::from_str::<Notification>(r#"{"text":"hello"}"#).unwrap();
        assert_eq!(bare.display_line(), "hello");
        assert_eq!(bare.kind, "");
    }

    #[test]
    fn a_tts_request_does_not_name_an_engine() {
        // The voice decides. Sending an engine would only be able to contradict it.
        let req = TtsRequest {
            text: "hi".into(),
            voice: "sv_SE-lisa-medium".into(),
            speed: 1.0,
            lang: Some("sv".into()),

            quality: None,
        };
        let json = serde_json::to_string(&req).unwrap();
        assert!(
            !json.contains("engine"),
            "request must not name an engine: {json}"
        );
        assert!(json.contains("\"voice\""));
    }

    #[test]
    fn health_response_roundtrip() {
        let resp = HealthResponse {
            status: "healthy".into(),
            model_loaded: true,
            engine: "kokoro".into(),
            device: Some("gpu".into()),
            gpu_name: Some("NVIDIA GeForce RTX 4090".into()),
        };
        let json = serde_json::to_string(&resp).unwrap();
        let parsed: HealthResponse = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.status, "healthy");
        assert!(parsed.model_loaded);
    }

    #[test]
    fn voices_response_roundtrip() {
        let resp = VoicesResponse {
            voices: vec![
                Voice {
                    id: "test_a".into(),
                    name: "Test A".into(),
                    lang: "en-us".into(),
                    gender: "f".into(),
                    grade: "A".into(),
                    engine: "kokoro".into(),
                },
                Voice {
                    id: "test_b".into(),
                    name: "Test B".into(),
                    lang: "en-us".into(),
                    gender: "m".into(),
                    grade: "B".into(),
                    engine: "kokoro".into(),
                },
            ],
        };
        let json = serde_json::to_string(&resp).unwrap();
        let parsed: VoicesResponse = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.voices.len(), 2);
        assert_eq!(parsed.voices[0].id, "test_a");
        assert_eq!(parsed.voices[1].grade, "B");
    }
}
