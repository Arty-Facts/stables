use crate::app::{AppState, Mode};

fn char_to_byte(s: &str, char_idx: usize) -> usize {
    s.char_indices()
        .nth(char_idx)
        .map(|(b, _)| b)
        .unwrap_or(s.len())
}
use ratatui::{
    layout::{Alignment, Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

pub fn render(f: &mut Frame, app: &AppState) {
    // Announcements get their own rows rather than being reduced to the newest
    // one in a single line: the client keeps three, so it should show three.
    let notice_rows = app.notices.len().min(AppState::NOTICE_ROWS) as u16;
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),           // header
            Constraint::Min(3),              // text area
            Constraint::Length(notice_rows), // announcements, when there are any
            Constraint::Length(1),           // help bar
            Constraint::Length(1),           // status bar
        ])
        .split(f.area());

    render_header(f, app, chunks[0]);
    render_text_area(f, app, chunks[1]);
    if notice_rows > 0 {
        render_notices(f, app, chunks[2]);
    }
    render_help_bar(f, app, chunks[3]);
    render_status_bar(f, app, chunks[4]);
}

/// The header badge for where the backend computes.
///
/// "GPU" is shown only when the backend says so; a CPU fallback has to be
/// visible, because a GPU box quietly running on CPU is the failure nobody
/// notices until it is slow. Unknown (no answer yet, or an older backend) is
/// shown as such rather than assumed to be either.
fn device_badge(device: Option<&str>) -> (&'static str, Color) {
    match device {
        Some("gpu") => ("GPU", Color::Green),
        Some("cpu") => ("CPU", Color::DarkGray),
        _ => ("--", Color::DarkGray),
    }
}

fn render_header(f: &mut Frame, app: &AppState, area: ratatui::layout::Rect) {
    let online_indicator = if app.server_online { "●" } else { "○" };
    let online_color = if app.server_online {
        Color::Green
    } else {
        Color::Red
    };

    let (voice_label, voice_color) = if app.voices_confirmed {
        let label = app
            .voices
            .iter()
            .find(|v| v.id == app.voice)
            .map(|v| v.display_label())
            .unwrap_or_else(|| app.voice.clone());
        (label, Color::Cyan)
    } else {
        (format!("{} (not available)", app.voice), Color::DarkGray)
    };

    let header = Line::from(vec![
        Span::styled(" RATTS ", Style::default().add_modifier(Modifier::BOLD)),
        Span::raw("│ Voice: "),
        Span::styled(voice_label, Style::default().fg(voice_color)),
        Span::raw(" │ Speed: "),
        Span::styled(
            format!("{:.1}x", app.speed),
            Style::default().fg(Color::Yellow),
        ),
        Span::raw(" │ Server: "),
        Span::styled(online_indicator, Style::default().fg(online_color)),
        Span::raw(" "),
        {
            let (label, color) = device_badge(app.server_device.as_deref());
            Span::styled(label, Style::default().fg(color))
        },
    ]);

    let widget = Paragraph::new(header).style(Style::default().bg(Color::DarkGray));
    f.render_widget(widget, area);
}

fn render_text_area(f: &mut Frame, app: &AppState, area: ratatui::layout::Rect) {
    let border_color = match &app.mode {
        Mode::Generating | Mode::Streaming => Color::Yellow,
        Mode::Error(_) => Color::Red,
        _ => Color::White,
    };

    let title = match &app.mode {
        Mode::Generating => " Generating... ",
        Mode::Streaming => " Streaming... ",
        Mode::Error(_) => " Error ",
        _ => " Text ",
    };

    let focused = !matches!(app.mode, Mode::Generating | Mode::Streaming);

    let text = if app.text.is_empty() {
        if focused {
            Paragraph::new("█")
                .block(
                    Block::default()
                        .borders(Borders::ALL)
                        .title(title)
                        .border_style(Style::default().fg(border_color)),
                )
                .wrap(Wrap { trim: false })
        } else {
            Paragraph::new("Paste or type text here...")
                .style(Style::default().fg(Color::DarkGray))
                .block(
                    Block::default()
                        .borders(Borders::ALL)
                        .title(title)
                        .border_style(Style::default().fg(border_color)),
                )
                .wrap(Wrap { trim: false })
        }
    } else {
        let display = if focused {
            let mut s = app.text.clone();
            let byte_pos = char_to_byte(&s, app.tts_cursor);
            s.insert_str(byte_pos, "█");
            s
        } else {
            app.text.clone()
        };
        Paragraph::new(display)
            .block(
                Block::default()
                    .borders(Borders::ALL)
                    .title(title)
                    .border_style(Style::default().fg(border_color)),
            )
            .wrap(Wrap { trim: false })
    };

    f.render_widget(text, area);
}

fn render_help_bar(f: &mut Frame, app: &AppState, area: ratatui::layout::Rect) {
    let (auto_label, auto_color) = if app.auto_play_on_copy {
        ("[ON] ", Color::Green)
    } else {
        ("[OFF]", Color::Red)
    };
    let spans = vec![
        Span::styled("Ctrl+P", Style::default().fg(Color::Yellow)),
        Span::raw(" Play  "),
        Span::styled("Ctrl+A", Style::default().fg(Color::Yellow)),
        Span::raw(" Auto:"),
        Span::styled(auto_label, Style::default().fg(auto_color)),
        Span::raw("  "),
        Span::styled("Ctrl+S", Style::default().fg(Color::Yellow)),
        Span::raw(" Settings  "),
        Span::styled("Ctrl+H", Style::default().fg(Color::Yellow)),
        Span::raw(" Help  "),
        Span::styled("Ctrl+Q", Style::default().fg(Color::Yellow)),
        Span::raw(" Quit "),
    ];
    let help = Line::from(spans);
    let widget = Paragraph::new(help)
        .style(Style::default().bg(Color::DarkGray))
        .alignment(Alignment::Left);
    f.render_widget(widget, area);
}

/// What agents and shells have asked the user to be told, oldest first.
///
/// Shown as well as spoken: a notice heard over headphones, or with the volume
/// down, would otherwise be lost, and the queue has already given it up.
fn render_notices(f: &mut Frame, app: &AppState, area: ratatui::layout::Rect) {
    let lines: Vec<Line> = app
        .notices
        .iter()
        .map(|notice| {
            Line::from(vec![
                Span::styled("  ", Style::default()),
                Span::styled(notice.clone(), Style::default().fg(Color::Cyan)),
            ])
        })
        .collect();
    f.render_widget(Paragraph::new(lines), area);
}

fn render_status_bar(f: &mut Frame, app: &AppState, area: ratatui::layout::Rect) {
    let status_color = match &app.mode {
        Mode::Error(_) => Color::Red,
        Mode::Generating | Mode::Streaming => Color::Yellow,
        _ => Color::Gray,
    };

    let status_text = match &app.mode {
        Mode::Error(e) => format!(" Error: {}", e),
        _ => format!(" {}", app.status),
    };

    let widget = Paragraph::new(status_text)
        .style(Style::default().fg(status_color))
        .alignment(Alignment::Left);
    f.render_widget(widget, area);
}
