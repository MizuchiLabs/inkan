package license

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var refreshClient = &http.Client{Timeout: 15 * time.Second}

// Refresh asks a worker for the key a license is owed now. endpoint is the
// refresh URL of the worker, e.g. https://keys.example.com/refresh. app and
// publicKeys are the ones [Verify] takes.
//
// After a renewal the result is a new key with a later expiry. If nothing new
// was paid it is the same key again, and a hand issued key always comes back
// as it is. The answer is checked like in [Verify] before it is returned, so
// a broken or hostile server cannot hand the app anything but a real key of
// this app. The key can still be expired or bound to another machine,
// [Verify] tells.
//
// On an error the app keeps the key it has. That key works offline until it
// expires, a failed refresh takes nothing away.
func Refresh(ctx context.Context, endpoint, rawKey, app, publicKeys string) (string, error) {
	body := strings.NewReader(Clean(rawKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("refreshing license: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := refreshClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("refreshing license: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", fmt.Errorf("refreshing license: %w", err)
	}
	answer := strings.TrimSpace(string(raw))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("refreshing license: %s: %s", resp.Status, answer[:min(len(answer), 200)])
	}

	if _, err := Verify(answer, app, publicKeys, ""); err != nil && !errors.Is(err, ErrExpired) {
		return "", fmt.Errorf("refreshing license: the answer is not a key of this app: %w", err)
	}
	return Clean(answer), nil
}
