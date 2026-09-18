use crate::app::AppState;
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Clear, List, ListItem, ListState, Paragraph, Tabs},
    Frame,
};

pub fn render(f: &mut Frame, app: &AppState) {
    let area = centered_rect(60, 70, f.area());
    f.render_widget(Clear, area);

    let (title, border_color) = if app.voices_confirmed {
        (" Voices ", Color::Cyan)
    } else {
        (" Voices — not available ", Color::DarkGray)
    };
    let outer = Block::default()
        .title(title)
        .borders(Borders::ALL)
        .border_style(Style::default().fg(border_color));
    f.render_widget(outer, area);

    let inner = Layout::default()
        .direction(Direction::Vertical)
        .margin(1)
        .constraints([
            Constraint::Length(2), // tabs
            Constraint::Min(3),    // voice list
            Constraint::Length(1), // help
        ])
        .split(area);

    render_tabs(f, app, inner[0]);
    render_voice_list(f, app, inner[1]);
    render_voice_help(f, inner[2]);
}

fn render_tabs(f: &mut Frame, app: &AppState, area: Rect) {
    let tab_names = app.language_tab_names();
    let tab_refs: Vec<&str> = tab_names.iter().map(|s| s.as_str()).collect();
    let titles: Vec<Line> = tab_refs.iter().map(|c| Line::from(*c)).collect();
    let tabs = Tabs::new(titles)
        .select(app.voice_menu_category)
        .style(Style::default().fg(Color::Gray))
        .highlight_style(
            Style::default()
                .fg(Color::Yellow)
                .add_modifier(Modifier::BOLD),
        )
        .divider("|");
    f.render_widget(tabs, area);
}

fn render_voice_list(f: &mut Frame, app: &AppState, area: Rect) {
    let voices = app.voices_in_current_category();
    let items: Vec<ListItem> = voices
        .iter()
        .map(|v| {
            let is_fav = app.favorites.contains(&v.id);
            let is_current = v.id == app.voice;
            let cur_icon = if is_current { "►" } else { " " };
            let fav_icon = if is_fav { "♥" } else { "·" };
            let name_color = if !app.voices_confirmed {
                Color::DarkGray
            } else if v.grade == "A" {
                Color::Yellow
            } else {
                Color::White
            };

            let mut spans = vec![
                Span::raw(format!("{} {} ", cur_icon, fav_icon)),
                Span::raw(format!("{} ", v.grade_icon())),
                Span::styled(
                    format!("{:<16}", v.name),
                    Style::default().fg(if is_current { Color::Cyan } else { name_color }),
                ),
                Span::raw(format!("{} {} ", v.region_label(), v.gender_icon())),
            ];
            // Which engine speaks it. Dimmed, because it is context rather than a
            // choice — the voice decides it (see the design note).
            let engine = engine_label(&v.engine, app.server_device.as_deref());
            if !engine.is_empty() {
                spans.push(Span::styled(engine, Style::default().fg(Color::DarkGray)));
            }
            ListItem::new(Line::from(spans))
        })
        .collect();

    let mut state = ListState::default();
    state.select(Some(app.voice_menu_cursor));

    let list = List::new(items).highlight_style(
        Style::default()
            .bg(Color::DarkGray)
            .add_modifier(Modifier::BOLD),
    );

    f.render_stateful_widget(list, area, &mut state);
}

/// The engine that speaks a voice, ready for the menu row.
///
/// Worth showing because it answers "why is this one slow". Kokoro and Piper are
/// fractions of a second either way; a cloned voice runs on Qwen, which is
/// minutes per sentence without a GPU, so on a CPU backend that is said out loud.
///
/// Empty for an engine the backend did not name, so a row does not gain a stray
/// separator.
fn engine_label(engine: &str, device: Option<&str>) -> String {
    let name = match engine {
        "" => return String::new(),
        "kokoro" => "Kokoro",
        "piper" => "Piper",
        "qwen" => "Qwen",
        other => other,
    };
    if engine == "qwen" && device == Some("cpu") {
        return format!("{name} (slow on CPU)");
    }
    name.to_string()
}

fn render_voice_help(f: &mut Frame, area: Rect) {
    let help = Line::from(vec![
        Span::styled("Enter", Style::default().fg(Color::Yellow)),
        Span::raw(" Select  "),
        Span::styled("←→", Style::default().fg(Color::Yellow)),
        Span::raw(" Lang  "),
        Span::styled("Esc", Style::default().fg(Color::Yellow)),
        Span::raw(" Back"),
    ]);
    f.render_widget(Paragraph::new(help), area);
}

/// Returns a centered `Rect` of the given percentage of the container.
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

#[cfg(test)]
mod tests {
    use super::engine_label;

    #[test]
    fn names_the_engine() {
        assert_eq!(engine_label("kokoro", Some("gpu")), "Kokoro");
        assert_eq!(engine_label("piper", Some("cpu")), "Piper");
        assert_eq!(engine_label("qwen", Some("gpu")), "Qwen");
    }

    #[test]
    fn warns_when_cloning_is_the_slow_one() {
        // Qwen on a CPU is minutes per sentence, so the row says so.
        assert_eq!(engine_label("qwen", Some("cpu")), "Qwen (slow on CPU)");
    }

    #[test]
    fn says_nothing_rather_than_a_guess() {
        // An older backend does not send an engine, and unknown means unknown.
        assert_eq!(engine_label("", Some("gpu")), "");
        assert_eq!(engine_label("espeak", Some("gpu")), "espeak");
    }
}
