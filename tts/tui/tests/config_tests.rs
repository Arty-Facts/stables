use ratts_lib::config::Preferences;
use std::path::PathBuf;
use tempfile::TempDir;

fn tmp_path(dir: &TempDir) -> PathBuf {
    dir.path().join("config.toml")
}

#[test]
fn default_values() {
    let p = Preferences::default();
    assert_eq!(p.primary_voice, "");
    assert!((p.default_speed - 1.0).abs() < f32::EPSILON);
    assert_eq!(p.server_url, "http://127.0.0.1:17493");
    assert!(p.favorite_voices.is_empty());
}

#[test]
fn save_and_load_roundtrip() {
    let dir = TempDir::new().unwrap();
    let path = tmp_path(&dir);

    let mut p = Preferences::default();
    p.primary_voice = "voice_b".into();
    p.default_speed = 1.8;
    p.favorite_voices = vec!["voice_a".into(), "voice_b".into()];
    p.set_rating("voice_a", 4);
    p.record_recent("voice_b");

    p.save_to(path.clone()).unwrap();

    let loaded = Preferences::load_from(path).unwrap();
    assert_eq!(loaded.primary_voice, "voice_b");
    assert!((loaded.default_speed - 1.8).abs() < f32::EPSILON);
    assert_eq!(loaded.favorite_voices, vec!["voice_a", "voice_b"]);
    assert_eq!(loaded.rating("voice_a"), 4);
    assert_eq!(loaded.recent_voices[0], "voice_b");
}

#[test]
fn load_missing_file_errors() {
    let result = Preferences::load_from(PathBuf::from("/does/not/exist.toml"));
    assert!(result.is_err());
}

#[test]
fn partial_config_fills_defaults() {
    let dir = TempDir::new().unwrap();
    let path = tmp_path(&dir);
    std::fs::write(&path, "primary_voice = \"voice_b\"\n").unwrap();

    let p = Preferences::load_from(path).unwrap();
    assert_eq!(p.primary_voice, "voice_b");
    assert_eq!(p.server_url, "http://127.0.0.1:17493"); // default
    assert!((p.default_speed - 1.0).abs() < f32::EPSILON); // default
}

#[test]
fn favorite_dedup() {
    let mut p = Preferences::default();
    p.add_favorite("voice_a");
    p.add_favorite("voice_a");
    assert_eq!(p.favorite_voices.len(), 1);
}

#[test]
fn remove_favorite() {
    let mut p = Preferences::default();
    p.favorite_voices = vec!["voice_a".into(), "voice_b".into()];
    p.remove_favorite("voice_a");
    assert!(!p.is_favorite("voice_a"));
    assert!(p.is_favorite("voice_b"));
}

#[test]
fn recent_voices_capped_at_10() {
    let mut p = Preferences::default();
    p.recent_voices.clear();
    for i in 0..15 {
        p.record_recent(&format!("voice_{}", i));
    }
    assert_eq!(p.recent_voices.len(), 10);
}

#[test]
fn recent_voices_most_recent_first() {
    let mut p = Preferences::default();
    p.recent_voices.clear();
    p.record_recent("first");
    p.record_recent("second");
    assert_eq!(p.recent_voices[0], "second");
}

#[test]
fn rating_clamped_at_5() {
    let mut p = Preferences::default();
    p.set_rating("voice_a", 99);
    assert_eq!(p.rating("voice_a"), 5);
}

#[test]
fn unknown_rating_zero() {
    let p = Preferences::default();
    assert_eq!(p.rating("never_set_voice"), 0);
}

#[test]
fn creates_parent_dirs_on_save() {
    let dir = TempDir::new().unwrap();
    let nested = dir.path().join("a").join("b").join("config.toml");
    let p = Preferences::default();
    p.save_to(nested.clone()).unwrap();
    assert!(nested.exists());
}
