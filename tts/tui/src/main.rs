mod app;
mod audio;
mod client;
mod clipboard;
mod config;
mod tui;
mod ui;

use anyhow::Result;
use app::{AppState, Mode};
use base64::Engine;
use bytes::Bytes;
use config::Preferences;
use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyModifiers};
use futures_util::StreamExt;
use std::time::Duration;
use tokio::sync::mpsc;

#[derive(Debug)]
enum AppEvent {
    Key(KeyEvent),
    Clip(String),
    ServerStatus {
        online: bool,
        device: Option<String>,
    },
    VoicesLoaded(Vec<client::Voice>),
    LanguageVoicesLoaded(client::LanguageVoicesResponse),
    AudioChunk(Bytes),
    /// How many messages are waiting. The count only: the queue is drained at the
    /// moment a message can be spoken, not when it is noticed.
    NoticesWaiting(usize),
    /// A message that was just spoken, for the notice area.
    Notice(String),
    StreamDone,
    Error(String),
}

#[tokio::main]
async fn main() -> Result<()> {
    // Load preferences
    let prefs = Preferences::load();

    // Init state
    let mut app = AppState::from_prefs(&prefs);

    // Set up clipboard monitor
    let (clip_monitor, mut clip_rx) = clipboard::ClipboardMonitor::start();

    // App event channel — unbounded so background producers never block and
    // key events can always be enqueued even when audio chunks burst in.
    let (tx, mut rx) = mpsc::unbounded_channel::<AppEvent>();

    // Watch channel so the health-check task picks up URL changes immediately.
    let (url_tx, url_rx) = tokio::sync::watch::channel(app.server_url.clone());

    // And one for reachability, so the announcement poll stays quiet while the
    // server is down instead of collecting refused connections every two seconds.
    let (online_tx, online_rx) = tokio::sync::watch::channel(false);

    // A second receiver for the announcement poll: the health check below takes
    // ownership of the first one.
    let notice_url_rx = url_rx.clone();

    // Background server health check
    {
        let tx2 = tx.clone();
        let online_tx = online_tx.clone();
        tokio::spawn(async move {
            loop {
                let url = url_rx.borrow().clone();
                let client = client::TtsClient::new(&url);
                let online = client.health_check().await;
                // Asked in the same tick: the header should say where the
                // backend computes, not merely that it is up.
                let device = if online {
                    client.health_device().await
                } else {
                    None
                };
                let _ = tx2.send(AppEvent::ServerStatus { online, device });
                let _ = online_tx.send(online);
                tokio::time::sleep(Duration::from_secs(3)).await;
            }
        });
    }

    // Announcement poll. It *peeks* rather than consuming: the queue is drained at
    // the moment a message can actually be spoken, not when it is noticed, so a
    // notice arriving while the user is busy is not read into the void.
    {
        let tx2 = tx.clone();
        let mut url_rx = notice_url_rx;
        let mut online_rx = online_rx;
        tokio::spawn(async move {
            loop {
                // Nothing to ask a server that is not there, and the health check
                // already owns that question.
                if !*online_rx.borrow_and_update() {
                    let _ = online_rx.changed().await;
                    continue;
                }
                let url = url_rx.borrow().clone();
                let client = client::TtsClient::new(&url);
                if let Ok(items) = client.notifications(false).await {
                    if !items.is_empty() {
                        let _ = tx2.send(AppEvent::NoticesWaiting(items.len()));
                    }
                }
                // Wake early if the server URL changes, so a repointed client does
                // not keep polling the old one for the rest of the interval.
                tokio::select! {
                    _ = url_rx.changed() => {}
                    _ = online_rx.changed() => {}
                    _ = tokio::time::sleep(Duration::from_secs(2)) => {}
                }
            }
        });
    }

    // Clipboard events → app channel
    {
        let tx2 = tx.clone();
        tokio::spawn(async move {
            while let Some(text) = clip_rx.recv().await {
                let _ = tx2.send(AppEvent::Clip(text));
            }
        });
    }

    // Open audio player
    let audio_player = audio::AudioPlayer::new().ok();

    // Init TUI
    let mut terminal = tui::init()?;

    // Event loop
    'main: loop {
        // Update text area width from current terminal size
        if let Ok((w, _)) = crossterm::terminal::size() {
            app.text_area_width = w.saturating_sub(2);
        }

        // Draw
        terminal.draw(|f| {
            ui::main_view::render(f, &app);
            if app.mode == Mode::VoiceMenu {
                ui::voice_menu::render(f, &app);
            }
            if app.mode == Mode::Settings {
                ui::settings::render(f, &app);
            }
            if app.mode == Mode::Help {
                ui::help_view::render(f, &app);
            }
        })?;

        // Gather events (non-blocking key poll + channel drain)
        if event::poll(Duration::from_millis(50))? {
            if let Event::Key(key) = event::read()? {
                let _ = tx.send(AppEvent::Key(key));
            }
        }

        // Process all pending app events
        while let Ok(evt) = rx.try_recv() {
            match evt {
                AppEvent::Key(key) => {
                    if handle_key(&mut app, key, &tx, &audio_player).await? {
                        break 'main;
                    }
                }
                AppEvent::Clip(text) => {
                    app.push_tts_undo();
                    app.tts_cursor = text.chars().count();
                    app.text = text;
                    if app.auto_play_on_copy
                        && app.server_online
                        && !matches!(app.mode, Mode::VoiceMenu)
                    {
                        app.cancel();
                        if let Some(ref player) = audio_player {
                            player.stop();
                        }
                        start_generation(&mut app, &tx, &audio_player).await?;
                    } else {
                        app.status = format!("Clipboard: {} chars", app.text.len());
                    }
                }
                AppEvent::ServerStatus { online, device } => {
                    let was_online = app.server_online;
                    app.server_online = online;
                    app.server_device = device;
                    if online && !was_online {
                        app.status = "Online".into();
                        let url = app.server_url.clone();
                        let tx2 = tx.clone();
                        tokio::spawn(async move {
                            let c = client::TtsClient::new(&url);
                            if let Ok(voices) = c.get_voices().await {
                                let _ = tx2.send(AppEvent::VoicesLoaded(voices));
                            }
                            if let Ok(lang) = c.get_language_voices().await {
                                let _ = tx2.send(AppEvent::LanguageVoicesLoaded(lang));
                            }
                        });
                    } else if !online {
                        app.server_online = false;
                        app.voices_confirmed = false;
                        app.voices = vec![];
                        app.status = "Server offline".into();
                    }
                }
                AppEvent::VoicesLoaded(voices) => {
                    let ids: Vec<String> = voices.iter().map(|v| v.id.clone()).collect();
                    app.voices = voices;
                    app.voices_confirmed = true;
                    let best = app
                        .favorites
                        .iter()
                        .find(|f| ids.contains(f))
                        .cloned()
                        .or_else(|| {
                            if !app.voice.is_empty() && ids.contains(&app.voice) {
                                Some(app.voice.clone())
                            } else {
                                None
                            }
                        })
                        .or_else(|| {
                            app.voices
                                .iter()
                                .max_by_key(|v| match v.grade.as_str() {
                                    "A" => 2,
                                    "B" => 1,
                                    _ => 0,
                                })
                                .map(|v| v.id.clone())
                        });
                    if let Some(id) = best {
                        if id != app.voice {
                            app.switch_voice(id);
                        }
                    }
                    app.status = format!("Online — {} voices", app.voices.len());
                }
                AppEvent::LanguageVoicesLoaded(resp) => {
                    for (lang, group) in resp.languages {
                        app.language_engines.insert(lang.clone(), group.engine);
                        app.language_groups.insert(lang, group.voices);
                    }
                }
                AppEvent::AudioChunk(bytes) => {
                    if let Some(ref player) = audio_player {
                        player.push_bytes(&bytes);
                    }
                }
                AppEvent::NoticesWaiting(count) => {
                    // Spoken only when nothing else is happening. A notice that
                    // interrupts is worse than one that waits, and the queue keeps
                    // it either way.
                    if app.ready_for_notice() {
                        app.announcing = true;
                        app.mode = Mode::Streaming;
                        app.status = format!("Reading {count} notice(s)…");
                        let url = app.server_url.clone();
                        let voice = app.notice_voice_id();
                        let speed = app.speed;
                        let quality = app.stretch_quality.clone();
                        let (cancel_tx, mut cancel_rx) = tokio::sync::oneshot::channel::<()>();
                        app.cancel_tx = Some(cancel_tx);
                        let tx2 = tx.clone();
                        tokio::spawn(async move {
                            let c = client::TtsClient::new(&url);
                            let items = match c.notifications(true).await {
                                Ok(items) => items,
                                Err(e) => {
                                    let _ = tx2.send(AppEvent::Error(e.to_string()));
                                    let _ = tx2.send(AppEvent::StreamDone);
                                    return;
                                }
                            };
                            for item in items {
                                let _ = tx2.send(AppEvent::Notice(item.display_line()));
                                // Streamed, not synthesized in one go: a long
                                // notice starts speaking before it is finished.
                                let res = tokio::select! {
                                    _ = &mut cancel_rx => break,
                                    res = forward_stream(&c, &tx2, &item.text, &voice, speed, quality_arg(&quality)) => res,
                                };
                                if let Err(e) = res {
                                    let _ = tx2.send(AppEvent::Error(e.to_string()));
                                    break;
                                }
                            }
                            let _ = tx2.send(AppEvent::StreamDone);
                        });
                    }
                }
                AppEvent::Notice(line) => {
                    app.push_notice(line.clone());
                    app.status = line;
                }
                AppEvent::StreamDone => {
                    app.mode = Mode::Normal;
                    app.announcing = false;
                    app.status = "Done".into();
                }
                AppEvent::Error(e) => {
                    app.mode = Mode::Error(e);
                }
            }
        }

        // If the server URL was changed via settings, notify the health-check task
        // and reset connection state so it reconnects to the new URL.
        if app.url_changed {
            app.url_changed = false;
            app.server_online = false;
            app.voices_confirmed = false;
            app.voices = vec![];
            let _ = url_tx.send(app.server_url.clone());
        }
    }

    clip_monitor.stop();
    tui::restore()?;

    save_prefs(&app);

    Ok(())
}

/// Stream one utterance to the player, splitting on the byte the previous chunk
/// left behind.
///
/// Shared by what the user asked for and by announcements, so a notice cannot take
/// a different path through the audio than a sentence does — the same alignment
/// bug would have to be fixed twice otherwise.
async fn forward_stream(
    client: &client::TtsClient,
    tx: &mpsc::UnboundedSender<AppEvent>,
    text: &str,
    voice: &str,
    speed: f32,
    quality: Option<&str>,
) -> Result<()> {
    let mut stream = client.stream_pcm(text, voice, speed, quality).await?;
    let mut carry: Option<u8> = None;
    while let Some(chunk) = stream.next().await {
        let bytes = chunk.map_err(anyhow::Error::from)?;
        let data: Vec<u8> = if let Some(c) = carry.take() {
            let mut v = Vec::with_capacity(bytes.len() + 1);
            v.push(c);
            v.extend_from_slice(&bytes);
            v
        } else {
            bytes.to_vec()
        };
        // i16 samples do not respect chunk boundaries: an odd byte at the end of
        // one chunk is the low half of the first sample of the next.
        let aligned = data.len() & !1;
        if data.len() % 2 != 0 {
            carry = Some(data[data.len() - 1]);
        }
        if aligned > 0 {
            let _ = tx.send(AppEvent::AudioChunk(Bytes::copy_from_slice(
                &data[..aligned],
            )));
        }
    }
    Ok(())
}

/// The quality to send, or nothing when the server should choose.
fn quality_arg(quality: &str) -> Option<&str> {
    match quality {
        "" | "auto" => None,
        chosen => Some(chosen),
    }
}

fn save_prefs(app: &AppState) {
    let mut p = Preferences::load();
    p.stretch_quality = app.stretch_quality.clone();
    p.notice_voice = app.notice_voice.clone();
    p.primary_voice = app.voice.clone();
    p.default_speed = app.speed;
    p.favorite_voices = app.favorites.clone();
    p.auto_play_on_copy = app.auto_play_on_copy;
    p.voice_speeds = app.voice_speeds.clone();
    p.recent_voices = app.recent_voices.clone();
    p.voice_ratings = app.voice_ratings.clone();
    p.server_url = app.server_url.clone();
    p.lang_mode = app.lang_mode.clone();
    p.lang_voices = app.lang_voices.clone();
    let _ = p.save();
}

/// Handle a key event; returns `true` to quit.
async fn handle_key(
    app: &mut AppState,
    key: KeyEvent,
    tx: &mpsc::UnboundedSender<AppEvent>,
    audio_player: &Option<audio::AudioPlayer>,
) -> Result<bool> {
    match app.mode {
        Mode::Help => {
            app.mode = Mode::Normal;
            return Ok(false);
        }
        Mode::VoiceMenu => handle_voice_menu_key(app, key),
        Mode::Settings => handle_settings_key(app, key),
        Mode::Normal => handle_normal_key(app, key, tx, audio_player).await,
        Mode::Generating | Mode::Streaming => {
            if key.code == KeyCode::Esc {
                app.cancel();
                if let Some(ref player) = audio_player {
                    player.stop();
                }
            }
            Ok(false)
        }
        Mode::Error(_) => {
            // Any key clears error
            app.mode = Mode::Normal;
            app.status = String::new();
            Ok(false)
        }
    }
}

async fn handle_normal_key(
    app: &mut AppState,
    key: KeyEvent,
    tx: &mpsc::UnboundedSender<AppEvent>,
    audio_player: &Option<audio::AudioPlayer>,
) -> Result<bool> {
    match (key.modifiers, key.code) {
        (KeyModifiers::CONTROL, KeyCode::Char('q')) => return Ok(true),
        (KeyModifiers::CONTROL, KeyCode::Char('a')) => app.toggle_auto_play(),
        (KeyModifiers::CONTROL, KeyCode::Char('v')) => {
            app.mode = Mode::VoiceMenu;
        }
        (KeyModifiers::CONTROL, KeyCode::Char('s')) => {
            app.mode = Mode::Settings;
        }
        (KeyModifiers::CONTROL, KeyCode::Char('h')) => {
            app.mode = Mode::Help;
        }
        (KeyModifiers::CONTROL, KeyCode::Char('p')) | (KeyModifiers::NONE, KeyCode::Enter) => {
            if !app.text.is_empty() && app.server_online {
                start_generation(app, tx, audio_player).await?;
            }
        }
        (KeyModifiers::CONTROL, KeyCode::Right) => app.increase_speed(),
        (KeyModifiers::CONTROL, KeyCode::Left) => app.decrease_speed(),
        (KeyModifiers::CONTROL, KeyCode::Down) => app.cycle_favorites_next(),
        (KeyModifiers::CONTROL, KeyCode::Up) => app.cycle_favorites_prev(),
        (KeyModifiers::CONTROL, KeyCode::Char('y')) => {
            if let Ok(mut board) = arboard::Clipboard::new() {
                let _ = board.set_text(app.text.clone());
                app.status = "Copied to clipboard".into();
            }
        }
        (KeyModifiers::CONTROL, KeyCode::Char('z')) => {
            app.undo_tts();
        }
        (KeyModifiers::CONTROL, KeyCode::Char('n')) => {
            app.redo_tts();
        }
        (KeyModifiers::CONTROL, KeyCode::Char('d')) => {
            app.push_tts_undo();
            delete_current_line(&mut app.text, &mut app.tts_cursor);
        }
        (KeyModifiers::CONTROL, KeyCode::Char('l')) => {
            app.push_tts_undo();
            app.text.clear();
            app.tts_cursor = 0;
        }
        (KeyModifiers::NONE, KeyCode::Char(c)) => {
            app.push_tts_undo();
            let byte_pos = char_to_byte(&app.text, app.tts_cursor);
            app.text.insert(byte_pos, c);
            app.tts_cursor += 1;
        }
        (KeyModifiers::NONE, KeyCode::Backspace) => {
            if app.tts_cursor > 0 {
                app.push_tts_undo();
                let end = char_to_byte(&app.text, app.tts_cursor);
                let start = char_to_byte(&app.text, app.tts_cursor - 1);
                app.text.drain(start..end);
                app.tts_cursor -= 1;
            }
        }
        (KeyModifiers::NONE, KeyCode::Up) => {
            app.tts_cursor =
                cursor_move_up(&app.text, app.tts_cursor, app.text_area_width as usize);
        }
        (KeyModifiers::NONE, KeyCode::Down) => {
            app.tts_cursor =
                cursor_move_down(&app.text, app.tts_cursor, app.text_area_width as usize);
        }
        (KeyModifiers::NONE, KeyCode::Left) => {
            if app.tts_cursor > 0 {
                app.tts_cursor -= 1;
            }
        }
        (KeyModifiers::NONE, KeyCode::Right) => {
            if app.tts_cursor < app.text.chars().count() {
                app.tts_cursor += 1;
            }
        }
        _ => {}
    }
    Ok(false)
}

fn handle_voice_menu_key(app: &mut AppState, key: KeyEvent) -> Result<bool> {
    match key.code {
        KeyCode::Esc => {
            app.mode = Mode::Normal;
        }
        KeyCode::Up => app.voice_menu_up(),
        KeyCode::Down => app.voice_menu_down(),
        KeyCode::Left => app.voice_menu_prev_category(),
        KeyCode::Right => app.voice_menu_next_category(),
        KeyCode::Enter => {
            if let Some(name) = app.select_voice_from_menu() {
                app.switch_voice(name);
                app.mode = Mode::Normal;
            }
        }
        KeyCode::Char('f') => {
            if let Some(name) = app.select_voice_from_menu() {
                if app.favorites.contains(&name) {
                    app.favorites.retain(|v| *v != name);
                } else {
                    app.favorites.push(name);
                }
            }
        }
        KeyCode::Char('p') => {
            if let Some(name) = app.select_voice_from_menu() {
                if !app.favorites.contains(&name) {
                    app.favorites.push(name.clone());
                }
                app.switch_voice(name);
                app.mode = Mode::Normal;
            }
        }
        _ => {}
    }
    Ok(false)
}

fn handle_settings_key(app: &mut AppState, key: KeyEvent) -> Result<bool> {
    if app.settings_editing_url {
        match key.code {
            KeyCode::Esc => {
                app.settings_editing_url = false;
            }
            KeyCode::Enter => {
                let new_url = app.settings_url_input.trim().to_string();
                if !new_url.is_empty() {
                    app.server_url = new_url;
                    app.url_changed = true;
                    app.status = "Server URL updated — reconnecting...".into();
                    save_prefs(app);
                }
                app.settings_editing_url = false;
            }
            KeyCode::Backspace => {
                app.settings_url_input.pop();
            }
            KeyCode::Char(c) => {
                app.settings_url_input.push(c);
            }
            _ => {}
        }
    } else {
        match key.code {
            KeyCode::Esc => {
                app.mode = Mode::Normal;
            }
            KeyCode::Up => app.settings_up(),
            KeyCode::Down => app.settings_down(),
            KeyCode::Enter => {
                if app.settings_cursor == 0 {
                    app.settings_editing_url = true;
                    app.settings_url_input = app.server_url.clone();
                } else if let Some(msg) = app.execute_settings_action() {
                    app.status = msg;
                    save_prefs(app);
                }
            }
            _ => {}
        }
    }
    Ok(false)
}

async fn start_generation(
    app: &mut AppState,
    tx: &mpsc::UnboundedSender<AppEvent>,
    audio_player: &Option<audio::AudioPlayer>,
) -> Result<()> {
    if let Some(ref player) = audio_player {
        player.stop();
    }

    let (cancel_tx, cancel_rx) = tokio::sync::oneshot::channel::<()>();
    app.cancel_tx = Some(cancel_tx);

    let text = app.text.clone();
    let voice = app.voice.clone();
    // "auto" is sent as nothing, letting the server decide; anything else is the
    // user's override.
    let quality = app.stretch_quality.clone();
    let speed = app.speed;
    let server_url = app.server_url.clone();
    let tx2 = tx.clone();
    let use_streaming = app.use_streaming();

    // Streaming path: skip lang detection (stream_pcm doesn't support it yet)
    if use_streaming {
        app.mode = Mode::Streaming;
        app.status = "Streaming...".into();

        tokio::spawn(async move {
            let c = client::TtsClient::new(&server_url);
            // Whole lifecycle (initial POST + chunk loop) races cancel_rx so Esc
            // aborts even when the server is stalled before sending headers.
            tokio::select! {
                _ = cancel_rx => {
                    let _ = tx2.send(AppEvent::StreamDone);
                }
                res = forward_stream(&c, &tx2, &text, &voice, speed, quality_arg(&quality)) => {
                    match res {
                        Ok(()) => { let _ = tx2.send(AppEvent::StreamDone); }
                        Err(e) => { let _ = tx2.send(AppEvent::Error(e.to_string())); }
                    }
                }
            }
        });
    } else {
        app.mode = Mode::Generating;
        app.status = "Generating...".into();

        let lang_mode = app.lang_mode.clone();
        let lang_voices = app.lang_voices.clone();
        // Which engine speaks this is not sent: the server picks it from the
        // voice, which is the only thing that knows.
        let (tts_voice, tts_lang) = if lang_mode != "auto" {
            let v = lang_voices
                .get(&lang_mode)
                .cloned()
                .unwrap_or(voice.clone());
            (v, Some(lang_mode.clone()))
        } else {
            ("auto".to_string(), None::<String>)
        };

        tokio::spawn(async move {
            let client = client::TtsClient::new(&server_url);
            tokio::select! {
                _ = cancel_rx => {
                    let _ = tx2.send(AppEvent::StreamDone);
                }
                result = client.generate_opts(&text, &tts_voice, speed, tts_lang.as_deref(), quality_arg(&quality)) => {
                    match result {
                        Ok(resp) => {
                            match decode_wav_response(&resp.audio) {
                                Ok(pcm) => {
                                    let bytes = Bytes::from(audio::i16_to_bytes(&pcm));
                                    let _ = tx2.send(AppEvent::AudioChunk(bytes));
                                    let _ = tx2.send(AppEvent::StreamDone);
                                }
                                Err(e) => {
                                    let _ = tx2.send(AppEvent::Error(e.to_string()));
                                }
                            }
                        }
                        Err(e) => {
                            let _ = tx2.send(AppEvent::Error(e.to_string()));
                        }
                    }
                }
            }
        });
    }

    Ok(())
}

fn delete_current_line(text: &mut String, cursor: &mut usize) {
    if text.is_empty() {
        return;
    }
    let chars: Vec<char> = text.chars().collect();
    let len = chars.len();
    let pos = (*cursor).min(len.saturating_sub(1));
    // Start of line: after the preceding \n (or 0)
    let line_start = chars[..pos]
        .iter()
        .rposition(|&c| c == '\n')
        .map(|i| i + 1)
        .unwrap_or(0);
    // End of line: up to and including the trailing \n (or end of text)
    let line_end = chars[pos..]
        .iter()
        .position(|&c| c == '\n')
        .map(|i| pos + i + 1)
        .unwrap_or(len);
    let byte_start = char_to_byte(text, line_start);
    let byte_end = char_to_byte(text, line_end);
    text.drain(byte_start..byte_end);
    *cursor = line_start.min(text.chars().count());
}

fn char_to_byte(s: &str, char_idx: usize) -> usize {
    s.char_indices()
        .nth(char_idx)
        .map(|(b, _)| b)
        .unwrap_or(s.len())
}

/// Returns char-index starts of every visual line (hard-wrap at `width`, split on '\n').
fn visual_line_starts(text: &str, width: usize) -> Vec<usize> {
    let mut starts = vec![0usize];
    let mut col = 0usize;
    let char_count = text.chars().count();
    for (i, c) in text.chars().enumerate() {
        if c == '\n' {
            starts.push(i + 1);
            col = 0;
        } else {
            col += 1;
            if col >= width {
                if i + 1 <= char_count {
                    starts.push(i + 1);
                }
                col = 0;
            }
        }
    }
    starts
}

fn cursor_move_up(text: &str, cursor: usize, width: usize) -> usize {
    let starts = visual_line_starts(text, width);
    let line_idx = starts.partition_point(|&s| s <= cursor).saturating_sub(1);
    if line_idx == 0 {
        return cursor;
    }
    let col = cursor - starts[line_idx];
    let prev_start = starts[line_idx - 1];
    let prev_len = starts[line_idx] - prev_start;
    prev_start + col.min(prev_len.saturating_sub(1))
}

fn cursor_move_down(text: &str, cursor: usize, width: usize) -> usize {
    let char_count = text.chars().count();
    let starts = visual_line_starts(text, width);
    let line_idx = starts.partition_point(|&s| s <= cursor).saturating_sub(1);
    if line_idx + 1 >= starts.len() {
        return cursor;
    }
    let col = cursor - starts[line_idx];
    let next_start = starts[line_idx + 1];
    let next_len = if line_idx + 2 < starts.len() {
        starts[line_idx + 2] - next_start
    } else {
        char_count - next_start
    };
    (next_start + col)
        .min(next_start + next_len)
        .min(char_count)
}

fn decode_wav_response(b64: &str) -> Result<Vec<i16>> {
    let wav_bytes = base64::engine::general_purpose::STANDARD.decode(b64)?;
    let cursor = std::io::Cursor::new(wav_bytes);
    let mut reader = hound::WavReader::new(cursor)?;
    let samples: Vec<i16> = reader.samples::<i16>().filter_map(|s| s.ok()).collect();
    Ok(samples)
}
