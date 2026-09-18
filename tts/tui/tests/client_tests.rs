use mockito::{Matcher, Server};
use ratts_lib::client::TtsClient;
use serde_json::json;

// Re-export the library so tests can use it.
// Since this is a binary crate we expose a lib target in Cargo.toml
// (see integration-test feature gate in Cargo.toml).

#[tokio::test]
async fn health_check_returns_true_on_200() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(200)
        .with_body(r#"{"status":"healthy","model_loaded":true,"engine":"kokoro"}"#)
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    assert!(client.health_check().await);
    mock.assert_async().await;
}

#[tokio::test]
async fn health_check_returns_false_on_503() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(503)
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    assert!(!client.health_check().await);
    mock.assert_async().await;
}

#[tokio::test]
async fn generate_parses_response() {
    let mut server = Server::new_async().await;
    let body = json!({
        "audio": "SGVsbG8=",  // base64 "Hello"
        "text": "Hello world",
        "voice": "voice_a",
        "speed": 1.0,
        "sampling_rate": 24000,
        "duration": 1.5
    });
    let mock = server
        .mock("POST", "/v1/tts")
        .match_header("content-type", Matcher::Regex("application/json".into()))
        .with_status(200)
        .with_body(body.to_string())
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    let resp = client
        .generate("Hello world", "voice_a", 1.0)
        .await
        .unwrap();
    assert_eq!(resp.text, "Hello world");
    assert_eq!(resp.voice, "voice_a");
    assert_eq!(resp.sampling_rate, 24000);
    mock.assert_async().await;
}

#[tokio::test]
async fn generate_errors_on_500() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("POST", "/v1/tts")
        .with_status(500)
        .with_body(r#"{"detail":"model error"}"#)
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    let result = client.generate("Hello", "voice_a", 1.0).await;
    assert!(result.is_err());
    mock.assert_async().await;
}

#[tokio::test]
async fn stream_pcm_receives_bytes() {
    let mut server = Server::new_async().await;
    // Fake PCM: 4 bytes = 2 i16 samples
    let fake_pcm: Vec<u8> = vec![0x00, 0x01, 0xFF, 0x7F];
    let mock = server
        .mock("POST", "/v1/tts/stream/pcm")
        .with_status(200)
        .with_header("Content-Type", "audio/pcm")
        .with_header("X-Sample-Rate", "24000")
        .with_body(fake_pcm.clone())
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    let mut stream = client
        .stream_pcm("Hello", "voice_a", 1.0, None)
        .await
        .unwrap();

    use futures_util::StreamExt;
    let mut total_bytes = Vec::new();
    while let Some(chunk) = stream.next().await {
        total_bytes.extend_from_slice(&chunk.unwrap());
    }

    assert_eq!(total_bytes, fake_pcm);
    mock.assert_async().await;
}

#[tokio::test]
async fn health_detail_parses_json() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(200)
        .with_body(r#"{"status":"healthy","model_loaded":true,"engine":"kokoro"}"#)
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    let detail = client.health_detail().await.unwrap();
    assert_eq!(detail.status, "healthy");
    assert!(detail.model_loaded);
    assert_eq!(detail.engine, "kokoro");
    mock.assert_async().await;
}

#[tokio::test]
async fn get_voices_parses_rich_objects() {
    let mut server = Server::new_async().await;
    let body = json!({
        "voices": [
            {"id": "voice_a", "name": "Voice A", "lang": "en-us", "gender": "f", "grade": "A"},
            {"id": "voice_b", "name": "Voice B", "lang": "en-gb", "gender": "m", "grade": "B"}
        ]
    });
    let mock = server
        .mock("GET", "/v1/voices")
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(body.to_string())
        .create_async()
        .await;

    let client = TtsClient::new(server.url());
    let voices = client.get_voices().await.unwrap();
    assert_eq!(voices.len(), 2);
    assert_eq!(voices[0].id, "voice_a");
    assert_eq!(voices[0].grade, "A");
    assert_eq!(voices[1].lang, "en-gb");
    assert_eq!(voices[1].gender, "m");
    mock.assert_async().await;
}

#[test]
fn health_reports_which_device_the_backend_uses() {
    let json = r#"{"status":"healthy","model_loaded":true,"engine":"kokoro","device":"gpu","gpu_name":"NVIDIA GeForce RTX 4090"}"#;
    let parsed: ratts_lib::client::types::HealthResponse = serde_json::from_str(json).unwrap();
    assert_eq!(parsed.device.as_deref(), Some("gpu"));
    assert_eq!(parsed.gpu_name.as_deref(), Some("NVIDIA GeForce RTX 4090"));
}

#[test]
fn health_without_a_device_still_parses() {
    // An older backend does not send it, and the field is optional.
    let json = r#"{"status":"healthy","model_loaded":true,"engine":"kokoro"}"#;
    let parsed: ratts_lib::client::types::HealthResponse = serde_json::from_str(json).unwrap();
    assert!(parsed.device.is_none());
}

#[tokio::test]
async fn notifications_are_asked_for_without_consuming() {
    // Polling must not take the message: a notice read while the user is busy
    // would be gone, and the queue holds the only copy.
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/v1/notifications")
        .match_query(Matcher::UrlEncoded("consume".into(), "false".into()))
        .with_status(200)
        .with_body(
            r#"{"items":[{"id":3,"text":"the tests are green","kind":"summary","source":"ci"}],
                "pending":0,"capacity":50}"#,
        )
        .create_async()
        .await;

    let items = TtsClient::new(server.url())
        .notifications(false)
        .await
        .expect("the read should succeed");
    assert_eq!(items.len(), 1);
    assert_eq!(
        items[0].display_line(),
        "[summary] the tests are green (ci)"
    );
    mock.assert_async().await;
}

#[tokio::test]
async fn taking_notifications_says_so_on_the_wire() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/v1/notifications")
        .match_query(Matcher::UrlEncoded("consume".into(), "true".into()))
        .with_status(200)
        .with_body(r#"{"items":[]}"#)
        .create_async()
        .await;

    let items = TtsClient::new(server.url())
        .notifications(true)
        .await
        .unwrap();
    assert!(items.is_empty());
    mock.assert_async().await;
}

#[tokio::test]
async fn a_failed_notification_read_is_an_error_not_silence() {
    // An empty list means "nothing waiting" and a failure must not: told they are
    // the same, the client would quietly stop announcing anything.
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/v1/notifications")
        .match_query(Matcher::Any)
        .with_status(503)
        .create_async()
        .await;

    assert!(TtsClient::new(server.url())
        .notifications(false)
        .await
        .is_err());
    mock.assert_async().await;
}
