use crate::app::AppState;
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Clear, Paragraph},
    Frame,
};

pub fn render(f: &mut Frame, _app: &AppState) {
    let area = centered_rect(65, 80, f.area());
    f.render_widget(Clear, area);

    let outer = Block::default()
        .title(" Key Bindings ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));
    f.render_widget(outer, area);

    let inner = Layout::default()
        .direction(Direction::Vertical)
        .margin(1)
        .constraints([
            Constraint::Min(1),    // content
            Constraint::Length(1), // footer
        ])
        .split(area);

    render_content(f, inner[0]);
    render_footer(f, inner[1]);
}

fn render_content(f: &mut Frame, area: Rect) {
    let key = Style::default().fg(Color::Yellow);
    let desc = Style::default().fg(Color::White);
    let head = Style::default().fg(Color::DarkGray);

    let lines = vec![
        Line::from(vec![
            Span::styled(" Text Editing", head),
            Span::raw("                      "),
            Span::styled("Audio / Playback", head),
        ]),
        Line::from(vec![
            Span::styled(" ─────────────────────────────", head),
            Span::raw("   "),
            Span::styled("───────────────────────────", head),
        ]),
        Line::from(vec![
            Span::styled(" ←→↑↓     ", key),
            Span::styled("Move cursor           ", desc),
            Span::styled("Ctrl+P / Enter  ", key),
            Span::styled("Play TTS", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+Z    ", key),
            Span::styled("Undo                  ", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+N    ", key),
            Span::styled("Redo                  ", desc),
            Span::styled("Ctrl+←/→        ", key),
            Span::styled("Speed −/+", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+D    ", key),
            Span::styled("Delete line           ", desc),
            Span::styled("Ctrl+↑/↓        ", key),
            Span::styled("Prev/Next voice", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+L    ", key),
            Span::styled("Clear all", desc),
        ]),
        Line::from(Span::raw("")),
        Line::from(vec![Span::styled(" Interface", head)]),
        Line::from(vec![Span::styled(" ─────────────────────────────", head)]),
        Line::from(vec![
            Span::styled(" Ctrl+A    ", key),
            Span::styled("Toggle auto-play on copy", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+V    ", key),
            Span::styled("Voice browser", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+S    ", key),
            Span::styled("Settings", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+Y    ", key),
            Span::styled("Copy text", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+H    ", key),
            Span::styled("This help", desc),
        ]),
        Line::from(vec![
            Span::styled(" Ctrl+Q    ", key),
            Span::styled("Quit", desc),
        ]),
    ];

    f.render_widget(Paragraph::new(lines), area);
}

fn render_footer(f: &mut Frame, area: Rect) {
    let line = Line::from(vec![Span::styled(
        " Press any key to close",
        Style::default().fg(Color::DarkGray),
    )]);
    f.render_widget(Paragraph::new(line), area);
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
