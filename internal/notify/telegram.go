package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/trips"
)

const telegramBaseURL = "https://api.telegram.org"

// Telegram sends messages to a Telegram chat via the Bot API.
type Telegram struct {
	baseURL  string
	botToken string
	chatID   string
	http     *http.Client
	geo      *geocode.Client
}

// NewTelegram creates a Telegram notifier using the official API base URL.
func NewTelegram(botToken, chatID string, geo *geocode.Client) *Telegram {
	return NewTelegramWithBaseURL(telegramBaseURL, botToken, chatID, geo)
}

// NewTelegramWithBaseURL creates a Telegram notifier with a custom base URL
// (used in tests to point at a local httptest server).
func NewTelegramWithBaseURL(baseURL, botToken, chatID string, geo *geocode.Client) *Telegram {
	return &Telegram{
		baseURL:  baseURL,
		botToken: botToken,
		chatID:   chatID,
		http:     &http.Client{Timeout: 10 * time.Second},
		geo:      geo,
	}
}

// Send posts a trip summary message to the configured Telegram chat.
func (tg *Telegram) Send(ctx context.Context, t trips.Trip) error {
	var text string
	if t.StartLocation != "" && t.EndLocation != "" {
		text = fmt.Sprintf("🚗 %s: %s → %s, %.1f km", t.Date, t.StartLocation, t.EndLocation, t.DistanceKm)
	} else {
		text = fmt.Sprintf(
			"🚗 %s: %.4f,%.4f → %.4f,%.4f, %.1f km",
			t.Date,
			t.StartLat, t.StartLon,
			t.EndLat, t.EndLon,
			t.DistanceKm,
		)
	}

	payload := map[string]string{
		"chat_id": tg.chatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", tg.baseURL, tg.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tg.http.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}
	return nil
}
