use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

/// Spawns an OS thread that polls the clipboard every 500 ms.
/// New (non-duplicate, non-trivial) clips are sent through `tx`.
pub struct ClipboardMonitor {
    stop: Arc<AtomicBool>,
}

pub fn preprocess_clipboard_text(text: &str) -> String {
    let mut text = text.replace("-\n", "");
    text = text.replace("\n", " ");
    text.trim().to_string()
}

impl ClipboardMonitor {
    /// Start the monitor. Returns `(monitor, receiver)`.
    pub fn start() -> (Self, mpsc::Receiver<String>) {
        let (tx, rx) = mpsc::channel::<String>(32);
        let stop = Arc::new(AtomicBool::new(false));
        let stop_clone = Arc::clone(&stop);

        std::thread::spawn(move || {
            let mut last = String::new();
            // arboard::Clipboard requires being created on the thread that uses it.
            let mut board = match arboard::Clipboard::new() {
                Ok(b) => b,
                Err(e) => {
                    eprintln!("clipboard unavailable: {}", e);
                    return;
                }
            };

            while !stop_clone.load(Ordering::Relaxed) {
                if let Ok(text) = board.get_text() {
                    let processed = preprocess_clipboard_text(&text);
                    if processed.len() >= 3 && processed != last {
                        last = processed.clone();
                        let _ = tx.blocking_send(processed);
                    }
                }
                std::thread::sleep(Duration::from_millis(500));
            }
        });

        (ClipboardMonitor { stop }, rx)
    }

    /// Signal the polling thread to stop.
    pub fn stop(&self) {
        self.stop.store(true, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    /// Clipboard deduplication helper for unit tests.
    struct ClipFilter {
        last: String,
    }

    impl ClipFilter {
        fn new() -> Self {
            ClipFilter {
                last: String::new(),
            }
        }

        /// Returns `Some(text)` if it should be forwarded, `None` if it should be skipped.
        fn check(&mut self, text: &str) -> Option<String> {
            let trimmed = text.trim().to_string();
            if trimmed.len() >= 3 && trimmed != self.last {
                self.last = trimmed.clone();
                Some(trimmed)
            } else {
                None
            }
        }
    }

    #[test]
    fn filter_accepts_new_long_text() {
        let mut f = ClipFilter::new();
        assert!(f.check("Hello world").is_some());
    }

    #[test]
    fn filter_rejects_duplicate() {
        let mut f = ClipFilter::new();
        f.check("Hello world");
        assert!(f.check("Hello world").is_none());
    }

    #[test]
    fn filter_rejects_short_text() {
        let mut f = ClipFilter::new();
        assert!(f.check("hi").is_none());
        assert!(f.check("ab").is_none());
    }

    #[test]
    fn filter_accepts_after_different() {
        let mut f = ClipFilter::new();
        f.check("Hello world");
        assert!(f.check("Different text").is_some());
    }

    #[test]
    fn filter_trims_whitespace() {
        let mut f = ClipFilter::new();
        // "  hi  " trimmed is "hi" (len 2) → rejected
        assert!(f.check("  hi  ").is_none());
        // "  hello  " trimmed is "hello" (len 5) → accepted
        assert!(f.check("  hello  ").is_some());
        // same content, different padding → duplicate
        assert!(f.check("hello").is_none());
    }

    #[test]
    fn filter_exactly_3_chars_accepted() {
        let mut f = ClipFilter::new();
        assert!(f.check("abc").is_some());
    }

    #[test]
    fn filter_2_chars_rejected() {
        let mut f = ClipFilter::new();
        assert!(f.check("ab").is_none());
    }
}
