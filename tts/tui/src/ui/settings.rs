use crate::app::AppState;
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Clear, List, ListItem, ListState, Paragraph},
    Frame,
};

pub fn render(f: &mut Frame, app: &AppState) {
    let area = centered_rect(55, 80, f.area());
    f.render_widget(Clear, area);

    let outer = Block::default()
        .title(" Settings ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));
    f.render_widget(outer, area);

    let inner = Layout::default()
        .direction(Direction::Vertical)
        .margin(1)
        .constraints([
            Constraint::Length(13), // info section (11 rows + padding)
            Constraint::Length(1),  // divider
            Constraint::Min(7),     // actions
            Constraint::Length(1),  // help
        ])
        .split(area);

    render_info(f, app, inner[0]);
    render_divider(f, inner[1]);
    render_actions(f, app, inner[2]);
    render_help(f, app, inner[3]);
}

fn render_info(f: &mut Frame, app: &AppState, area: Rect) {
    let label = Style::default().fg(Color::DarkGray);
    let value = Style::default().fg(Color::White);
    let yes = Style::default().fg(Color::Green);
    let no = Style::default().fg(Color::Red);
    let num = Style::default().fg(Color::Yellow);

    let auto_style = if app.auto_play_on_copy { yes } else { no };
    let auto_val = if app.auto_play_on_copy { "ON" } else { "OFF" };
    let voice_val = if app.voice.is_empty() {
        "none".to_string()
    } else {
        app.voice.clone()
    };

    // Server URL row: show editable input when editing, plain value otherwise.
    let url_row = if app.settings_editing_url {
        Line::from(vec![
            Span::styled("  Server URL     ", label),
            Span::styled(
                app.settings_url_input.clone(),
                Style::default().fg(Color::Yellow),
            ),
            Span::styled("█", Style::default().fg(Color::Yellow)),
        ])
    } else {
        Line::from(vec![
            Span::styled("  Server URL     ", label),
            Span::styled(app.server_url.clone(), value),
        ])
    };

    let lines = vec![
        url_row,
        Line::from(vec![
            Span::styled("  Auto-play      ", label),
            Span::styled(auto_val, auto_style),
        ]),
        Line::from(vec![
            Span::styled("  Default Speed  ", label),
            Span::styled(format!("{:.1}x", app.default_speed), num),
        ]),
        Line::from(vec![
            Span::styled("  Current Voice  ", label),
            Span::styled(voice_val, value),
        ]),
        Line::from(vec![
            Span::styled("  Favorites      ", label),
            Span::styled(format!("{} saved", app.favorites.len()), num),
        ]),
        Line::from(vec![
            Span::styled("  Voice Speeds   ", label),
            Span::styled(format!("{} saved", app.voice_speeds.len()), num),
        ]),
        Line::from(vec![
            Span::styled("  Recent Voices  ", label),
            Span::styled(format!("{} saved", app.recent_voices.len()), num),
        ]),
        Line::from(vec![
            Span::styled("  Voice Ratings  ", label),
            Span::styled(format!("{} saved", app.voice_ratings.len()), num),
        ]),
        Line::from(vec![
            Span::styled("  Language       ", label),
            Span::styled(&app.lang_mode, value),
        ]),
    ];

    f.render_widget(Paragraph::new(lines), area);
}

fn render_divider(f: &mut Frame, area: Rect) {
    let line = Line::from(vec![Span::styled(
        "  ── Actions ",
        Style::default().fg(Color::DarkGray),
    )]);
    f.render_widget(Paragraph::new(line), area);
}

fn render_actions(f: &mut Frame, app: &AppState, area: Rect) {
    let gray = Style::default().fg(Color::DarkGray);
    let white = Style::default().fg(Color::White);
    let cyan = Style::default().fg(Color::Cyan);
    let red = Style::default().fg(Color::Red);

    // (label, count, style)
    // The second element is whatever is worth showing on the row: a count for the
    // clearing actions, the current level for the quality one.
    let rows: &[(&str, Option<String>, Style)] = &[
        ("Edit Server URL", None, cyan),
        (
            "Clear Favorites",
            Some(app.favorites.len().to_string()),
            white,
        ),
        (
            "Clear Voice Speeds",
            Some(app.voice_speeds.len().to_string()),
            white,
        ),
        (
            "Clear Recent Voices",
            Some(app.recent_voices.len().to_string()),
            white,
        ),
        (
            "Clear Voice Ratings",
            Some(app.voice_ratings.len().to_string()),
            white,
        ),
        ("Reset All to Defaults", None, red),
        ("Toggle Language", None, cyan),
        // Higher quality means more reconstruction passes: cleaner, and slower.
        ("Stretch Quality", Some(app.quality_label()), cyan),
        // The voice notices are read in, so they can sound distinct from
        // answers without changing the voice answers use.
        ("Notice Voice", Some(app.notice_voice_label()), cyan),
    ];

    let items: Vec<ListItem> = rows
        .iter()
        .map(|(label, count, style)| {
            let mut spans = vec![Span::raw("  "), Span::styled(*label, *style)];
            if let Some(n) = count {
                spans.push(Span::styled(format!("  ({})", n), gray));
            }
            ListItem::new(Line::from(spans))
        })
        .collect();

    let mut state = ListState::default();
    // Don't highlight action list while editing URL — the edit cursor is in the info section.
    if !app.settings_editing_url {
        state.select(Some(app.settings_cursor));
    }

    let list = List::new(items).highlight_style(
        Style::default()
            .bg(Color::DarkGray)
            .add_modifier(Modifier::BOLD),
    );

    f.render_stateful_widget(list, area, &mut state);
}

fn render_help(f: &mut Frame, app: &AppState, area: Rect) {
    let help = if app.settings_editing_url {
        Line::from(vec![
            Span::styled("  Enter", Style::default().fg(Color::Yellow)),
            Span::raw(" Confirm  "),
            Span::styled("Esc", Style::default().fg(Color::Yellow)),
            Span::raw(" Cancel"),
        ])
    } else {
        Line::from(vec![
            Span::styled("  Enter", Style::default().fg(Color::Yellow)),
            Span::raw(" Execute  "),
            Span::styled("Esc", Style::default().fg(Color::Yellow)),
            Span::raw(" Back"),
        ])
    };
    f.render_widget(Paragraph::new(help), area);
}

fn centered_rect(percent_x: u16, percent_y: u16, r: Rect) -> Rect {
    let popup_layout = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Percentage((100 - percent_y) / 2),
            Constraint::Percentage(percent_y),
            Constraint::Percentage((100 - percent_y) / 2),
        ])
        .split(r);

    Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage((100 - percent_x) / 2),
            Constraint::Percentage(percent_x),
            Constraint::Percentage((100 - percent_x) / 2),
        ])
        .split(popup_layout[1])[1]
}
