use crate::client::LanguageVoice;
use crate::client::Voice;
use crate::config::Preferences;
use std::collections::HashMap;
use std::collections::VecDeque;

#[derive(Debug, Clone, PartialEq)]
pub enum Mode {
    Normal,
    Help,
    VoiceMenu,
    Settings,
    Generating,
    Streaming,
    Error(String),
}

pub struct AppState {
    pub text: String,
    pub voice: String,
    pub speed: f32,
    pub mode: Mode,
    pub voices: Vec<Voice>,
    pub status: String,
    pub server_url: String,
    pub server_online: bool,
    /// Where the backend is computing: "gpu", "cpu", or unknown until it answers.
    pub server_device: Option<String>,
    /// Mirrors Preferences::stretch_quality so a request can consult it.
    pub stretch_quality: String,

    /// Voice that reads announcements. Empty means the voice in use.
    pub notice_voice: String,
    /// Messages from the queue that have been said, newest last. Bounded: this is
    /// a place to look, not a log file.
    pub notices: VecDeque<String>,
    /// True while an announcement is being spoken, so a poll tick cannot start a
    /// second one on top of the first.
    pub announcing: bool,

    // Voice menu navigation
    pub voice_menu_cursor: usize,
    pub voice_menu_category: usize,

    // Settings navigation
    pub settings_cursor: usize,
    pub settings_editing_url: bool,
    pub settings_url_input: String,
    /// Set true when server_url is changed via settings; cleared by main loop after notifying health-check.
    pub url_changed: bool,

    // Favorites cycling
    pub favorites: Vec<String>,

    // Mirrored from prefs
    pub recent_voices: Vec<String>,
    pub voice_ratings: HashMap<String, u8>,

    /// Cancel sender for in-progress generation.
    pub cancel_tx: Option<tokio::sync::oneshot::Sender<()>>,

    pub auto_play_on_copy: bool,

    /// Per-voice speed memory (in-memory mirror of prefs.voice_speeds).
    pub voice_speeds: HashMap<String, f32>,
    /// Fallback speed when a voice has no saved speed.
    pub default_speed: f32,

    /// True once the server has confirmed the actual voice list via VoicesLoaded.
    /// False on startup and whenever the server goes offline.
    pub voices_confirmed: bool,

    /// Char index into text; always kept ≤ text.chars().count().
    pub tts_cursor: usize,
    /// "auto" = detect, "sv"/"en" = force
    pub lang_mode: String,
    /// Voice per language
    pub lang_voices: HashMap<String, String>,
    /// Language-grouped voices from /v1/voices/languages
    pub language_groups: HashMap<String, Vec<LanguageVoice>>,
    /// Engine per language
    pub language_engines: HashMap<String, String>,
    /// Inner width of text panes in columns; updated each frame from terminal size.
    pub text_area_width: u16,

    // Undo/redo stacks: each entry is (text_snapshot, cursor_snapshot).
    pub tts_undo: Vec<(String, usize)>,
    pub tts_redo: Vec<(String, usize)>,
}

impl AppState {
    pub fn from_prefs(prefs: &Preferences) -> Self {
        AppState {
            text: String::new(),
            voice: prefs.primary_voice.clone(),
            speed: prefs.speed_for_voice(&prefs.primary_voice),
            mode: Mode::Normal,
            voices: vec![],
            status: "Connecting...".into(),
            server_url: prefs.server_url.clone(),
            server_online: false,
            server_device: None,
            stretch_quality: prefs.stretch_quality.clone(),
            notice_voice: prefs.notice_voice.clone(),
            notices: VecDeque::new(),
            announcing: false,
            voice_menu_cursor: 0,
            voice_menu_category: 0,
            settings_cursor: 0,
            settings_editing_url: false,
            settings_url_input: String::new(),
            url_changed: false,
            favorites: prefs.favorite_voices.clone(),
            recent_voices: prefs.recent_voices.clone(),
            voice_ratings: prefs.voice_ratings.clone(),
            cancel_tx: None,
            auto_play_on_copy: prefs.auto_play_on_copy,
            voice_speeds: prefs.voice_speeds.clone(),
            default_speed: prefs.default_speed,
            voices_confirmed: false,
            tts_cursor: 0,
            lang_mode: prefs.lang_mode.clone(),
            lang_voices: prefs.lang_voices.clone(),
            language_groups: HashMap::new(),
            language_engines: HashMap::new(),
            text_area_width: 78,
            tts_undo: vec![],
            tts_redo: vec![],
        }
    }

    // ── Speed controls ────────────────────────────────────────────────────────

    pub fn increase_speed(&mut self) {
        self.speed = clamp_speed(self.speed + 0.1);
        self.voice_speeds.insert(self.voice.clone(), self.speed);
    }

    pub fn decrease_speed(&mut self) {
        self.speed = clamp_speed(self.speed - 0.1);
        self.voice_speeds.insert(self.voice.clone(), self.speed);
    }

    /// Switch to a new voice, saving current speed and loading the new voice's saved speed.
    pub fn switch_voice(&mut self, id: String) {
        self.voice_speeds.insert(self.voice.clone(), self.speed);
        self.voice = id.clone();
        self.speed = self
            .voice_speeds
            .get(&id)
            .copied()
            .unwrap_or(self.default_speed);
    }

    #[allow(dead_code)]
    pub fn set_speed(&mut self, speed: f32) {
        self.speed = clamp_speed(speed);
    }

    // ── Voice menu ────────────────────────────────────────────────────────────

    pub fn voice_menu_up(&mut self) {
        if self.voice_menu_cursor > 0 {
            self.voice_menu_cursor -= 1;
        }
    }

    pub fn voice_menu_down(&mut self) {
        let max = self.voices_in_current_category().len().saturating_sub(1);
        if self.voice_menu_cursor < max {
            self.voice_menu_cursor += 1;
        }
    }

    pub fn voice_menu_prev_category(&mut self) {
        if self.voice_menu_category > 0 {
            self.voice_menu_category -= 1;
            self.voice_menu_cursor = 0;
        }
    }

    pub fn voice_menu_next_category(&mut self) {
        if self.voice_menu_category < self.voice_category_count().saturating_sub(1) {
            self.voice_menu_category += 1;
            self.voice_menu_cursor = 0;
        }
    }

    pub fn select_voice_from_menu(&mut self) -> Option<String> {
        let voices = self.voices_in_current_category();
        voices.get(self.voice_menu_cursor).map(|v| v.id.clone())
    }

    /// Get language names for voice menu tabs
    pub fn language_tab_names(&self) -> Vec<String> {
        let mut names: Vec<String> = self.language_groups.keys().cloned().collect();
        names.sort();
        names.insert(0, "Favorites".to_string());
        names.push("All".to_string());
        names
    }

    /// Voices for the currently selected language tab
    fn voice_category_count(&self) -> usize {
        self.language_tab_names().len()
    }

    pub fn voices_in_current_category(&self) -> Vec<&Voice> {
        let lang_names = self.language_tab_names();
        let selected = lang_names.get(self.voice_menu_category).map(|s| s.as_str());

        match selected {
            Some("Favorites") => self
                .voices
                .iter()
                .filter(|v| self.favorites.contains(&v.id))
                .collect(),
            Some("All") => self.voices.iter().collect(),
            // A language tab shows that language. It used to show everything,
            // which made the tabs decorative: the Swedish tab listed the same
            // English voices as every other tab, so a Swedish voice could not be
            // found by looking under Swedish.
            Some(lang) => self.voices.iter().filter(|v| v.lang == lang).collect(),
            None => self.voices.iter().collect(),
        }
    }

    // ── Favorites cycling ─────────────────────────────────────────────────────

    pub fn cycle_favorites_next(&mut self) {
        if self.favorites.is_empty() {
            return;
        }
        let pos = self
            .favorites
            .iter()
            .position(|v| *v == self.voice)
            .map(|i| (i + 1) % self.favorites.len())
            .unwrap_or(0);
        let name = self.favorites[pos].clone();
        self.switch_voice(name);
    }

    pub fn cycle_favorites_prev(&mut self) {
        if self.favorites.is_empty() {
            return;
        }
        let pos = self
            .favorites
            .iter()
            .position(|v| *v == self.voice)
            .map(|i| {
                if i == 0 {
                    self.favorites.len() - 1
                } else {
                    i - 1
                }
            })
            .unwrap_or(0);
        let name = self.favorites[pos].clone();
        self.switch_voice(name);
    }

    // ── Auto-play ─────────────────────────────────────────────────────────────

    pub fn toggle_auto_play(&mut self) {
        self.auto_play_on_copy = !self.auto_play_on_copy;
        self.status = if self.auto_play_on_copy {
            "Auto-play on copy: ON".into()
        } else {
            "Auto-play on copy: OFF".into()
        };
    }

    // ── Cancellation ─────────────────────────────────────────────────────────

    pub fn cancel(&mut self) {
        if let Some(tx) = self.cancel_tx.take() {
            let _ = tx.send(());
        }
        self.mode = Mode::Normal;
        self.status = "Cancelled".into();
    }

    // ── Settings ──────────────────────────────────────────────────────────────

    pub const SETTINGS_ACTION_COUNT: usize = 9;

    /// How many notices stay on screen. Enough to see what just happened, few
    /// enough never to crowd out the text being spoken.
    pub const NOTICE_ROWS: usize = 3;

    /// The quality levels, in the order the settings row cycles them.
    pub const QUALITY_LEVELS: [&'static str; 4] = ["auto", "low", "medium", "high"];

    /// Step to the next quality level, wrapping round.
    ///
    /// "auto" comes first and means the server decides by its own hardware, which
    /// is the honest default: it knows whether it has a GPU and the user should
    /// not have to. The levels after it are the manual override.
    pub fn cycle_stretch_quality(&mut self) -> String {
        let current = Self::QUALITY_LEVELS
            .iter()
            .position(|q| *q == self.stretch_quality)
            .unwrap_or(0);
        self.stretch_quality =
            Self::QUALITY_LEVELS[(current + 1) % Self::QUALITY_LEVELS.len()].into();
        format!("Stretch quality: {}", self.quality_label())
    }

    /// How the quality reads in the settings list, with the cost behind it so the
    /// trade is visible rather than a word to look up.
    pub fn quality_label(&self) -> String {
        match self.stretch_quality.as_str() {
            "low" => "low · 8 passes".into(),
            "medium" => "medium · 16 passes".into(),
            "high" => "high · 32 passes".into(),
            _ => "auto, by device".into(),
        }
    }

    /// Whether a waiting message may be spoken now: nothing is being generated or
    /// played, no menu is open, and one announcement is not already running.
    ///
    /// A notice that interrupts is worse than one that waits, and the queue keeps
    /// it either way. This is why the client polls without consuming.
    pub fn ready_for_notice(&self) -> bool {
        !self.announcing
            && self.server_online
            && !self.settings_editing_url
            && matches!(self.mode, Mode::Normal)
    }

    /// Show a notice, keeping the newest `NOTICE_ROWS` of them.
    pub fn push_notice(&mut self, line: String) {
        self.notices.push_back(line);
        while self.notices.len() > Self::NOTICE_ROWS {
            self.notices.pop_front();
        }
    }

    /// The voice a notice is read in: the chosen one, or the voice in use.
    pub fn notice_voice_id(&self) -> String {
        if self.notice_voice.is_empty() {
            self.voice.clone()
        } else {
            self.notice_voice.clone()
        }
    }

    pub fn notice_voice_label(&self) -> String {
        if self.notice_voice.is_empty() {
            return "current voice".into();
        }
        self.voices
            .iter()
            .find(|v| v.id == self.notice_voice)
            .map(|v| v.name.clone())
            // A voice the server has stopped offering is still named by its id
            // rather than silently becoming something else.
            .unwrap_or_else(|| self.notice_voice.clone())
    }

    /// Step through the notice voice: the voice in use first, then each voice the
    /// server offers.
    pub fn cycle_notice_voice(&mut self) -> String {
        let mut options = vec![String::new()];
        options.extend(self.voices.iter().map(|v| v.id.clone()));
        if !self.notice_voice.is_empty() && !options.contains(&self.notice_voice) {
            options.push(self.notice_voice.clone());
        }
        let current = options
            .iter()
            .position(|v| *v == self.notice_voice)
            .unwrap_or(0);
        self.notice_voice = options[(current + 1) % options.len()].clone();
        format!("Notice voice: {}", self.notice_voice_label())
    }

    pub fn settings_up(&mut self) {
        if self.settings_cursor > 0 {
            self.settings_cursor -= 1;
        }
    }

    pub fn settings_down(&mut self) {
        if self.settings_cursor < Self::SETTINGS_ACTION_COUNT - 1 {
            self.settings_cursor += 1;
        }
    }

    /// Execute action at current cursor.
    /// Cursor 0 = Edit Server URL (caller must enter edit mode instead of calling this).
    /// Returns a status message on success for cursor 1-5.
    pub fn execute_settings_action(&mut self) -> Option<String> {
        match self.settings_cursor {
            0 => None, // "Edit Server URL" — handled by key handler
            1 => {
                self.favorites.clear();
                Some("Favorites cleared".into())
            }
            2 => {
                self.voice_speeds.clear();
                Some("Voice speeds cleared".into())
            }
            3 => {
                self.recent_voices.clear();
                Some("Recent voices cleared".into())
            }
            4 => {
                self.voice_ratings.clear();
                Some("Voice ratings cleared".into())
            }
            7 => Some(self.cycle_stretch_quality()),
            8 => Some(self.cycle_notice_voice()),
            5 => {
                self.favorites.clear();
                self.voice_speeds.clear();
                self.recent_voices.clear();
                self.voice_ratings.clear();
                self.auto_play_on_copy = true;
                self.default_speed = 1.0;
                self.speed = 1.0;
                self.voice = String::new();
                Some("All preferences reset to defaults".into())
            }
            6 => {
                // Toggle Language: auto → sv → en → auto
                self.lang_mode = match self.lang_mode.as_str() {
                    "auto" => "sv".to_string(),
                    "sv" => "en".to_string(),
                    _ => "auto".to_string(),
                };
                Some(format!("Language: {}", self.lang_mode))
            }
            _ => None,
        }
    }

    // ── Undo / redo ───────────────────────────────────────────────────────────

    const UNDO_LIMIT: usize = 100;

    /// Snapshot the text before an edit. The stack is capped: a session that
    /// edits for hours holds at most 100 snapshots, not one per keystroke.
    pub fn push_tts_undo(&mut self) {
        self.push_snapshot();
        self.tts_redo.clear();
    }

    fn push_snapshot(&mut self) {
        self.tts_undo.push((self.text.clone(), self.tts_cursor));
        if self.tts_undo.len() > Self::UNDO_LIMIT {
            self.tts_undo.remove(0);
        }
    }

    pub fn undo_tts(&mut self) {
        if let Some((text, cursor)) = self.tts_undo.pop() {
            if self.tts_redo.len() < Self::UNDO_LIMIT {
                self.tts_redo.push((self.text.clone(), self.tts_cursor));
            }
            self.text = text;
            self.tts_cursor = cursor;
        }
    }

    pub fn redo_tts(&mut self) {
        if let Some((text, cursor)) = self.tts_redo.pop() {
            self.push_snapshot();
            self.text = text;
            self.tts_cursor = cursor;
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    pub fn use_streaming(&self) -> bool {
        self.text.len() > 300
    }
}

fn clamp_speed(s: f32) -> f32 {
    s.clamp(0.1, 4.0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Preferences;

    #[test]
    fn a_language_tab_lists_only_that_language() {
        // The menu's tabs come from /v1/voices/languages. Every tab used to show
        // every voice, so the Swedish voices were unreachable by looking under
        // Swedish — which is how this was reported.
        let mut app = make_app();
        app.voices = vec![
            Voice {
                id: "af_heart".into(),
                name: "Heart".into(),
                lang: "en".into(),
                gender: "f".into(),
                grade: "A".into(),
                engine: "kokoro".into(),
            },
            Voice {
                id: "bm_george".into(),
                name: "George".into(),
                lang: "en-gb".into(),
                gender: "m".into(),
                grade: "B".into(),
                engine: "kokoro".into(),
            },
            Voice {
                id: "sv_SE-lisa-medium".into(),
                name: "Lisa".into(),
                lang: "sv".into(),
                gender: "f".into(),
                grade: "B".into(),
                engine: "piper".into(),
            },
        ];
        app.language_groups.clear();
        app.language_groups.insert("en".into(), Default::default());
        app.language_groups
            .insert("en-gb".into(), Default::default());
        app.language_groups.insert("sv".into(), Default::default());

        let tabs = app.language_tab_names();
        let sv_tab = tabs
            .iter()
            .position(|t| t == "sv")
            .expect("sv tab must exist");
        app.voice_menu_category = sv_tab;

        let shown: Vec<&str> = app
            .voices_in_current_category()
            .iter()
            .map(|v| v.id.as_str())
            .collect();
        assert_eq!(
            shown,
            vec!["sv_SE-lisa-medium"],
            "the sv tab must not list English voices"
        );

        // And All still shows everything, so nothing became unreachable.
        app.voice_menu_category = tabs.iter().position(|t| t == "All").unwrap();
        assert_eq!(app.voices_in_current_category().len(), 3);
    }

    #[test]
    fn notice_voice_cycles_from_the_current_voice_through_the_available_ones() {
        let mut app = make_app();
        app.notice_voice = String::new();
        let seen: Vec<String> = (0..5)
            .map(|_| {
                app.cycle_notice_voice();
                app.notice_voice.clone()
            })
            .collect();
        // The voice in use comes first, so a default install needs no choice.
        assert_eq!(seen, vec!["voice_a", "voice_b", "voice_c", "", "voice_a"]);
    }

    #[test]
    fn notice_voice_reads_as_the_current_voice_when_unset() {
        let mut app = make_app();
        app.notice_voice = String::new();
        assert_eq!(app.notice_voice_label(), "current voice");
        assert_eq!(app.notice_voice_id(), app.voice);
    }

    #[test]
    fn a_chosen_notice_voice_is_named_rather_than_shown_as_an_id() {
        let mut app = make_app();
        app.notice_voice = "voice_b".into();
        assert_eq!(app.notice_voice_label(), "Voice B");
        assert_eq!(app.notice_voice_id(), "voice_b");
    }

    #[test]
    fn a_notice_voice_the_server_stopped_offering_is_still_named() {
        let mut app = make_app();
        app.notice_voice = "gone".into();
        assert_eq!(app.notice_voice_label(), "gone");
    }

    #[test]
    fn notices_are_bounded() {
        let mut app = make_app();
        for i in 0..10 {
            app.push_notice(format!("notice {i}"));
        }
        assert_eq!(app.notices.len(), AppState::NOTICE_ROWS);
        assert_eq!(app.notices.front().unwrap(), "notice 7");
        assert_eq!(app.notices.back().unwrap(), "notice 9");
    }

    #[test]
    fn a_notice_waits_while_something_else_is_happening() {
        let mut app = make_app();
        app.mode = Mode::Normal;
        app.server_online = true;
        app.announcing = false;
        assert!(app.ready_for_notice());

        for mode in [
            Mode::Generating,
            Mode::Streaming,
            Mode::Settings,
            Mode::VoiceMenu,
            Mode::Help,
        ] {
            app.mode = mode.clone();
            assert!(!app.ready_for_notice(), "must not interrupt {mode:?}");
        }

        // Not on top of a notice already being spoken, and not while the server
        // cannot answer at all.
        app.mode = Mode::Normal;
        app.announcing = true;
        assert!(!app.ready_for_notice());
        app.announcing = false;
        app.server_online = false;
        assert!(!app.ready_for_notice());
    }

    #[test]
    fn quality_cycles_from_auto_through_the_levels_and_back() {
        let mut app = make_app();
        app.stretch_quality = "auto".into();
        let seen: Vec<String> = (0..4)
            .map(|_| {
                app.cycle_stretch_quality();
                app.stretch_quality.clone()
            })
            .collect();
        assert_eq!(seen, vec!["low", "medium", "high", "auto"]);
    }

    #[test]
    fn quality_reads_with_its_cost() {
        let mut app = make_app();
        for (level, expected) in [
            ("auto", "auto, by device"),
            ("low", "low · 8 passes"),
            ("medium", "medium · 16 passes"),
            ("high", "high · 32 passes"),
        ] {
            app.stretch_quality = level.into();
            assert_eq!(app.quality_label(), expected);
        }
    }

    #[test]
    fn an_unset_quality_reads_as_automatic() {
        let mut app = make_app();
        app.stretch_quality = String::new();
        assert_eq!(app.quality_label(), "auto, by device");
    }

    fn make_app() -> AppState {
        let mut prefs = Preferences::default();
        prefs.favorite_voices = vec!["voice_a".into(), "voice_b".into(), "voice_c".into()];
        prefs.primary_voice = "voice_a".into();
        prefs.default_speed = 1.0;
        let mut app = AppState::from_prefs(&prefs);
        // Simulate VoicesLoaded so voice list is populated for menu tests
        app.voices = vec![
            Voice {
                id: "voice_a".into(),
                name: "Voice A".into(),
                lang: "en-us".into(),
                gender: "f".into(),
                grade: "A".into(),
                engine: "kokoro".into(),
            },
            Voice {
                id: "voice_b".into(),
                name: "Voice B".into(),
                lang: "en-us".into(),
                gender: "f".into(),
                grade: "A".into(),
                engine: "kokoro".into(),
            },
            Voice {
                id: "voice_c".into(),
                name: "Voice C".into(),
                lang: "en-us".into(),
                gender: "m".into(),
                grade: "B".into(),
                engine: "kokoro".into(),
            },
        ];
        app.voices_confirmed = true;
        app
    }

    #[test]
    fn speed_clamp_upper() {
        let mut app = make_app();
        app.speed = 3.95;
        app.increase_speed();
        app.increase_speed();
        assert!((app.speed - 4.0).abs() < 0.01);
    }

    #[test]
    fn speed_clamp_lower() {
        let mut app = make_app();
        app.speed = 0.15;
        app.decrease_speed();
        app.decrease_speed();
        assert!((app.speed - 0.1).abs() < 0.01);
    }

    #[test]
    fn set_speed_clamps() {
        let mut app = make_app();
        app.set_speed(10.0);
        assert!((app.speed - 4.0).abs() < f32::EPSILON);
        app.set_speed(-1.0);
        assert!((app.speed - 0.1).abs() < f32::EPSILON);
    }

    #[test]
    fn cycle_favorites_next() {
        let mut app = make_app();
        app.voice = "voice_a".into();
        app.cycle_favorites_next();
        assert_eq!(app.voice, "voice_b");
        app.cycle_favorites_next();
        assert_eq!(app.voice, "voice_c");
        app.cycle_favorites_next(); // wraps
        assert_eq!(app.voice, "voice_a");
    }

    #[test]
    fn cycle_favorites_prev() {
        let mut app = make_app();
        app.voice = "voice_a".into();
        app.cycle_favorites_prev(); // wraps to last
        assert_eq!(app.voice, "voice_c");
    }

    #[test]
    fn cycle_favorites_empty_is_noop() {
        let mut app = make_app();
        app.favorites.clear();
        app.voice = "voice_a".into();
        app.cycle_favorites_next();
        assert_eq!(app.voice, "voice_a"); // unchanged
    }

    #[test]
    fn mode_transitions() {
        let mut app = make_app();
        assert_eq!(app.mode, Mode::Normal);
        app.mode = Mode::VoiceMenu;
        assert_eq!(app.mode, Mode::VoiceMenu);
        app.mode = Mode::Streaming;
        app.cancel();
        assert_eq!(app.mode, Mode::Normal);
    }

    #[test]
    fn use_streaming_threshold() {
        let mut app = make_app();
        app.text = "x".repeat(300);
        assert!(!app.use_streaming());
        app.text = "x".repeat(301);
        assert!(app.use_streaming());
    }

    #[test]
    fn voice_menu_navigation_bounds() {
        let mut app = make_app();
        app.mode = Mode::VoiceMenu;
        app.voice_menu_category = app.voice_category_count().saturating_sub(1); // "All"
        app.voice_menu_cursor = 0;

        // Can't go above 0
        app.voice_menu_up();
        assert_eq!(app.voice_menu_cursor, 0);

        // Can navigate down to last
        let total = app.voices_in_current_category().len();
        for _ in 0..total + 5 {
            app.voice_menu_down();
        }
        assert_eq!(app.voice_menu_cursor, total - 1);
    }

    #[test]
    fn voice_menu_category_bounds() {
        let mut app = make_app();
        app.voice_menu_category = 0;
        app.voice_menu_prev_category(); // can't go below 0
        assert_eq!(app.voice_menu_category, 0);

        let max = app.voice_category_count().saturating_sub(1);
        app.voice_menu_category = max;
        app.voice_menu_next_category(); // can't exceed max
        assert_eq!(app.voice_menu_category, max);
    }

    #[test]
    fn select_voice_from_menu_returns_name() {
        let mut app = make_app();
        app.voice_menu_category = 3; // All voices
        app.voice_menu_cursor = 0;
        let selected = app.select_voice_from_menu();
        assert!(selected.is_some());
    }

    #[test]
    fn cancel_clears_cancel_tx() {
        let mut app = make_app();
        let (tx, _rx) = tokio::sync::oneshot::channel::<()>();
        app.cancel_tx = Some(tx);
        app.cancel();
        assert!(app.cancel_tx.is_none());
    }

    #[test]
    fn switch_voice_saves_old_speed_loads_new() {
        let mut app = make_app();
        app.voice = "voice_a".into();
        app.speed = 1.5;
        app.switch_voice("voice_b".into());
        // Old speed was saved
        assert!((app.voice_speeds["voice_a"] - 1.5).abs() < f32::EPSILON);
        // New voice has no saved speed → falls back to default_speed
        assert!((app.speed - app.default_speed).abs() < f32::EPSILON);
        assert_eq!(app.voice, "voice_b");
    }

    #[test]
    fn switch_voice_restores_saved_speed() {
        let mut app = make_app();
        app.voice_speeds.insert("voice_b".into(), 2.0);
        app.voice = "voice_a".into();
        app.speed = 1.0;
        app.switch_voice("voice_b".into());
        assert!((app.speed - 2.0).abs() < f32::EPSILON);
    }

    #[test]
    fn increase_speed_updates_voice_speeds_map() {
        let mut app = make_app();
        app.voice = "voice_a".into();
        app.speed = 1.0;
        app.increase_speed();
        assert!((app.voice_speeds["voice_a"] - app.speed).abs() < f32::EPSILON);
    }

    #[test]
    fn decrease_speed_updates_voice_speeds_map() {
        let mut app = make_app();
        app.voice = "voice_a".into();
        app.speed = 1.0;
        app.decrease_speed();
        assert!((app.voice_speeds["voice_a"] - app.speed).abs() < f32::EPSILON);
    }
}
