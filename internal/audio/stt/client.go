package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// Client POSTs WAV audio to a whisper-server /inference endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

type inferenceResponse struct {
	Text string `json:"text"`
}

// Transcribe POSTs the given WAV bytes to /inference and returns the decoded text.
func (c *Client) Transcribe(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="utt.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, bytes.NewReader(wav)); err != nil {
		return "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		tail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("whisper http %d: %s", resp.StatusCode, bytes.TrimSpace(tail))
	}

	var out inferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("whisper json decode: %w", err)
	}
	return out.Text, nil
}
