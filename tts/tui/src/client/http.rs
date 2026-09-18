use std::time::Duration;

use anyhow::{Context, Result};
use bytes::Bytes;
use futures_util::Stream;
use reqwest::Client;

use super::types::{
    HealthResponse, LanguageVoicesResponse, Notification, NotificationsResponse, StreamTtsRequest,
    TtsRequest, TtsResponse, Voice, VoicesResponse,
};

pub struct TtsClient {
    client: Client,
    pub base_url: String,
}

impl TtsClient {
    pub fn new(base_url: impl Into<String>) -> Self {
        // connect_timeout fails fast when the server is down; no overall
        // timeout so long-running streams aren't truncated mid-flight.
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .build()
            .unwrap_or_else(|_| Client::new());
        TtsClient {
            client,
            base_url: base_url.into(),
        }
    }

    /// The device the backend is computing on, or None when it does not say
    /// (an older backend, or a request that failed).
    /// Messages waiting to be spoken to the user.
    ///
    /// `consume` takes them off the queue. Polling must not: a message read while
    /// the user is busy would be lost, and the queue holds the only copy.
    pub async fn notifications(&self, consume: bool) -> Result<Vec<Notification>> {
        let url = format!("{}/v1/notifications", self.base_url.trim_end_matches('/'));
        let resp = self
            .client
            .get(&url)
            .query(&[("consume", consume)])
            .send()
            .await
            .context("asking for notifications")?;
        if !resp.status().is_success() {
            anyhow::bail!("notifications: server answered {}", resp.status());
        }
        let body: NotificationsResponse = resp.json().await.context("reading notifications")?;
        Ok(body.items)
    }

    pub async fn health_device(&self) -> Option<String> {
        match self.health_detail().await {
            Ok(health) => health.device,
            Err(_) => None,
        }
    }

    pub async fn health_check(&self) -> bool {
        let url = format!("{}/health", self.base_url);
        match self.client.get(&url).send().await {
            Ok(resp) => resp.status().is_success(),
            Err(_) => false,
        }
    }

    #[allow(dead_code)]
    pub async fn health_detail(&self) -> Result<HealthResponse> {
        let url = format!("{}/health", self.base_url);
        let resp = self
            .client
            .get(&url)
            .send()
            .await
            .context("GET /health failed")?;
        resp.json::<HealthResponse>()
            .await
            .context("deserializing health response")
    }

    /// Fetch list of voices from the server.
    pub async fn get_voices(&self) -> Result<Vec<Voice>> {
        let url = format!("{}/v1/voices", self.base_url);
        let resp = self
            .client
            .get(&url)
            .send()
            .await
            .context("GET /v1/voices failed")?;
        let body = resp
            .json::<VoicesResponse>()
            .await
            .context("deserializing voices response")?;
        Ok(body.voices)
    }

    /// Fetch voices grouped by language from the server.
    pub async fn get_language_voices(&self) -> Result<LanguageVoicesResponse> {
        let url = format!("{}/v1/voices/languages", self.base_url);
        let resp = self
            .client
            .get(&url)
            .send()
            .await
            .context("GET /v1/voices/languages failed")?;
        resp.json::<LanguageVoicesResponse>()
            .await
            .context("deserializing language voices response")
    }

    /// Generate audio for short text, returns WAV base64 as TtsResponse.
    ///
    /// The TUI always calls `generate_opts`; this is the plain-request form the
    /// client tests exercise.
    #[allow(dead_code)]
    pub async fn generate(&self, text: &str, voice: &str, speed: f32) -> Result<TtsResponse> {
        self.generate_opts(text, voice, speed, None, None).await
    }

    /// Generate with an explicit language. The engine is the server's business:
    /// the voice decides it, and a caller naming one could only be wrong.
    pub async fn generate_opts(
        &self,
        text: &str,
        voice: &str,
        speed: f32,
        lang: Option<&str>,
        quality: Option<&str>,
    ) -> Result<TtsResponse> {
        let url = format!("{}/v1/tts", self.base_url);
        let body = TtsRequest {
            text: text.to_string(),
            voice: voice.to_string(),
            speed,
            lang: lang.map(|s| s.to_string()),
            quality: quality.map(|s| s.to_string()),
        };
        let resp = self
            .client
            .post(&url)
            .json(&body)
            .send()
            .await
            .context("POST /v1/tts failed")?;

        if !resp.status().is_success() {
            let status = resp.status();
            let text = resp.text().await.unwrap_or_default();
            anyhow::bail!("TTS error {}: {}", status, text);
        }

        resp.json::<TtsResponse>()
            .await
            .context("deserializing TTS response")
    }

    /// Stream raw PCM bytes from `/v1/tts/stream/pcm`.
    /// Yields `Bytes` chunks of i16 LE mono samples at 24 kHz.
    pub async fn stream_pcm(
        &self,
        text: &str,
        voice: &str,
        speed: f32,
        quality: Option<&str>,
    ) -> Result<impl Stream<Item = Result<Bytes, reqwest::Error>>> {
        let url = format!("{}/v1/tts/stream/pcm", self.base_url);
        let body = StreamTtsRequest {
            text: text.to_string(),
            voice: voice.to_string(),
            speed,
            chunk_size: None,

            quality: quality.map(|s| s.to_string()),
        };
        let resp = self
            .client
            .post(&url)
            .json(&body)
            .send()
            .await
            .context("POST /v1/tts/stream/pcm failed")?;

        if !resp.status().is_success() {
            let status = resp.status();
            anyhow::bail!("PCM stream error {}", status);
        }

        Ok(resp.bytes_stream())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn client_new_stores_url() {
        let c = TtsClient::new("http://localhost:17493");
        assert_eq!(c.base_url, "http://localhost:17493");
    }
}
