use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Preferences {
    #[serde(default = "default_voice")]
    pub primary_voice: String,

    #[serde(default)]
    pub favorite_voices: Vec<String>,

    #[serde(default = "default_speed")]
    pub default_speed: f32,

    #[serde(default = "default_server_url")]
    pub server_url: String,

    #[serde(default)]
    pub recent_voices: Vec<String>,

    #[serde(default)]
    pub voice_ratings: HashMap<String, u8>,

    #[serde(default = "default_auto_play_on_copy")]
    pub auto_play_on_copy: bool,

    /// Stretch quality: "auto" lets the server choose by its own hardware, which
    /// means high where there is a GPU and medium where there is not. Any other
    /// value is sent as-is, so the choice is the user's when they want it.
    #[serde(default = "default_stretch_quality")]
    pub stretch_quality: String,

    /// Voice that reads announcements. Empty means the voice already in use, so a
    /// default install sounds consistent without a second choice to make.
    #[serde(default)]
    pub notice_voice: String,

    #[serde(default)]
    pub voice_speeds: HashMap<String, f32>,

    /// "auto" = detect language, "sv" = force Swedish, "en" = force English
    #[serde(default = "default_lang_mode")]
    pub lang_mode: String,

    /// Voice per language: { "sv": "sv_SE-lisa-medium", "en": "af_heart" }
    #[serde(default)]
    pub lang_voices: HashMap<String, String>,
}

fn default_lang_mode() -> String {
    "auto".to_string()
}

fn default_voice() -> String {
    String::new()
}

fn default_speed() -> f32 {
    1.0
}

fn default_server_url() -> String {
    "http://127.0.0.1:17493".to_string()
}

fn default_auto_play_on_copy() -> bool {
    true
}

/// What an unset quality means. Read as "auto" everywhere, including when an
/// older preferences file has no such field and serde leaves it empty.
pub fn default_stretch_quality() -> String {
    "auto".to_string()
}

impl Default for Preferences {
    fn default() -> Self {
        Preferences {
            primary_voice: default_voice(),
            favorite_voices: vec![],
            default_speed: default_speed(),
            server_url: default_server_url(),
            recent_voices: vec![],
            voice_ratings: HashMap::new(),
            auto_play_on_copy: true,
            stretch_quality: default_stretch_quality(),
            notice_voice: String::new(),
            voice_speeds: HashMap::new(),
            lang_mode: "auto".to_string(),
            lang_voices: HashMap::new(),
        }
    }
}

impl Preferences {
    /// Load from `~/.stables/tts/config.toml`, falling back to defaults.
    pub fn load() -> Self {
        Self::load_from(Self::config_path()).unwrap_or_default()
    }

    /// Load from an explicit path (useful for tests).
    pub fn load_from(path: PathBuf) -> Result<Self> {
        let content = std::fs::read_to_string(&path)
            .with_context(|| format!("reading {}", path.display()))?;
        toml::from_str(&content).with_context(|| format!("parsing {}", path.display()))
    }

    /// Save to `~/.stables/tts/config.toml`.
    pub fn save(&self) -> Result<()> {
        self.save_to(Self::config_path())
    }

    /// Save to an explicit path (useful for tests).
    pub fn save_to(&self, path: PathBuf) -> Result<()> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("creating dir {}", parent.display()))?;
        }
        let content = toml::to_string_pretty(self).context("serializing preferences")?;
        std::fs::write(&path, content).with_context(|| format!("writing {}", path.display()))
    }

    /// `~/.stables/tts/config.toml`, so everything the voice stack owns —
    /// preferences, the model cache — lives under one directory.
    pub fn config_path() -> PathBuf {
        dirs::home_dir()
            .unwrap_or_else(|| PathBuf::from("."))
            .join(".stables")
            .join("tts")
            .join("config.toml")
    }

    pub fn speed_for_voice(&self, voice: &str) -> f32 {
        self.voice_speeds
            .get(voice)
            .copied()
            .unwrap_or(self.default_speed)
    }

    #[allow(dead_code)]
    pub fn set_voice_speed(&mut self, voice: &str, speed: f32) {
        self.voice_speeds.insert(voice.to_string(), speed);
    }

    #[allow(dead_code)]
    pub fn add_favorite(&mut self, voice: &str) {
        if !self.favorite_voices.contains(&voice.to_string()) {
            self.favorite_voices.push(voice.to_string());
        }
    }

    #[allow(dead_code)]
    pub fn remove_favorite(&mut self, voice: &str) {
        self.favorite_voices.retain(|v| v != voice);
    }

    #[allow(dead_code)]
    pub fn is_favorite(&self, voice: &str) -> bool {
        self.favorite_voices.contains(&voice.to_string())
    }

    /// Record a recently used voice (keeps up to 10, most-recent first).
    #[allow(dead_code)]
    pub fn record_recent(&mut self, voice: &str) {
        self.recent_voices.retain(|v| v != voice);
        self.recent_voices.insert(0, voice.to_string());
        self.recent_voices.truncate(10);
    }

    #[allow(dead_code)]
    pub fn set_rating(&mut self, voice: &str, rating: u8) {
        let clamped = rating.min(5);
        self.voice_ratings.insert(voice.to_string(), clamped);
    }

    #[allow(dead_code)]
    pub fn rating(&self, voice: &str) -> u8 {
        *self.voice_ratings.get(voice).unwrap_or(&0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    fn tmp_path() -> PathBuf {
        NamedTempFile::new().unwrap().path().with_extension("toml")
    }

    #[test]
    fn save_and_load_roundtrip() {
        let mut prefs = Preferences::default();
        prefs.primary_voice = "voice_b".into();
        prefs.default_speed = 1.5;
        prefs.favorite_voices = vec!["voice_a".into(), "voice_b".into()];
        prefs.set_rating("voice_a", 5);

        let path = tmp_path();
        prefs.save_to(path.clone()).unwrap();

        let loaded = Preferences::load_from(path).unwrap();
        assert_eq!(loaded.primary_voice, "voice_b");
        assert!((loaded.default_speed - 1.5).abs() < f32::EPSILON);
        assert_eq!(loaded.favorite_voices, vec!["voice_a", "voice_b"]);
        assert_eq!(loaded.rating("voice_a"), 5);
    }

    #[test]
    fn load_missing_file_returns_default() {
        let result = Preferences::load_from(PathBuf::from("/nonexistent/path.toml"));
        assert!(result.is_err());
        // load() (not load_from) falls back to defaults
        // We test default explicitly:
        let d = Preferences::default();
        assert_eq!(d.primary_voice, "");
        assert!((d.default_speed - 1.0).abs() < f32::EPSILON);
        assert_eq!(d.server_url, "http://127.0.0.1:17493");
    }

    #[test]
    fn add_remove_favorite() {
        let mut prefs = Preferences::default();
        prefs.add_favorite("voice_a");
        prefs.add_favorite("voice_a"); // duplicate ignored
        assert_eq!(prefs.favorite_voices.len(), 1);
        prefs.add_favorite("voice_b");
        assert_eq!(prefs.favorite_voices.len(), 2);
        prefs.remove_favorite("voice_a");
        assert_eq!(prefs.favorite_voices.len(), 1);
        assert!(!prefs.is_favorite("voice_a"));
        assert!(prefs.is_favorite("voice_b"));
    }

    #[test]
    fn record_recent_dedupes_and_caps() {
        let mut prefs = Preferences::default();
        prefs.recent_voices.clear();
        for i in 0..12 {
            prefs.record_recent(&format!("voice_{}", i));
        }
        assert_eq!(prefs.recent_voices.len(), 10);
        // most recent is last added
        assert_eq!(prefs.recent_voices[0], "voice_11");

        // adding existing moves to front
        prefs.record_recent("voice_5");
        assert_eq!(prefs.recent_voices[0], "voice_5");
        assert_eq!(prefs.recent_voices.len(), 10);
    }

    #[test]
    fn rating_clamped_to_5() {
        let mut prefs = Preferences::default();
        prefs.set_rating("voice_a", 10);
        assert_eq!(prefs.rating("voice_a"), 5);
    }

    #[test]
    fn unknown_voice_rating_is_zero() {
        let prefs = Preferences::default();
        assert_eq!(prefs.rating("unknown_voice"), 0);
    }

    #[test]
    fn partial_toml_uses_defaults() {
        let path = tmp_path();
        std::fs::write(&path, "primary_voice = \"voice_c\"\n").unwrap();
        let prefs = Preferences::load_from(path).unwrap();
        assert_eq!(prefs.primary_voice, "voice_c");
        // server_url defaults
        assert_eq!(prefs.server_url, "http://127.0.0.1:17493");
        assert!((prefs.default_speed - 1.0).abs() < f32::EPSILON);
    }

    #[test]
    fn speed_for_voice_falls_back_to_default() {
        let prefs = Preferences::default();
        assert!(
            (prefs.speed_for_voice("unknown_voice") - prefs.default_speed).abs() < f32::EPSILON
        );
    }

    #[test]
    fn set_voice_speed_then_speed_for_voice_roundtrip() {
        let mut prefs = Preferences::default();
        prefs.set_voice_speed("voice_b", 1.5);
        assert!((prefs.speed_for_voice("voice_b") - 1.5).abs() < f32::EPSILON);
    }

    #[test]
    fn voice_speeds_toml_roundtrip() {
        let mut prefs = Preferences::default();
        prefs.set_voice_speed("voice_a", 1.2);
        prefs.set_voice_speed("voice_b", 0.8);

        let path = tmp_path();
        prefs.save_to(path.clone()).unwrap();
        let loaded = Preferences::load_from(path).unwrap();
        assert!((loaded.speed_for_voice("voice_a") - 1.2).abs() < f32::EPSILON);
        assert!((loaded.speed_for_voice("voice_b") - 0.8).abs() < f32::EPSILON);
    }
}
