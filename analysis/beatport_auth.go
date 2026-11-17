package analysis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// bpToken is a Beatport OAuth token response as stored in the token file,
// plus the time the access token expires and the client it was issued to
// (a refresh must name the same client).
type bpToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // unix seconds; 0: unknown, refresh first
	ClientID     string `json:"client_id,omitempty"`
}

// beatportToken returns an access token: BeatportToken when set, else the
// one in BeatportTokenFile, refreshed first when it has expired or when
// force is set (Beatport refused it). A refresh writes the new pair back,
// since Beatport may rotate the refresh token.
func (a *Analyzer) beatportToken(ctx context.Context, force bool) (string, error) {
	if a.opts.BeatportToken != "" {
		return a.opts.BeatportToken, nil
	}
	path := a.opts.BeatportTokenFile
	if path == "" {
		return "", ErrNoBeatportToken
	}
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoBeatportToken
	}
	if err != nil {
		return "", err
	}
	var tok bpToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("Beatport token file %s: %w", path, err)
	}
	if !force && tok.AccessToken != "" && time.Now().Add(30*time.Second).Unix() < tok.ExpiresAt {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("Beatport token expired and %s has no refresh_token", path)
	}
	if tok.ClientID == "" {
		tok.ClientID = jwtClientID(tok.AccessToken)
	}
	if tok.ClientID == "" {
		return "", fmt.Errorf("Beatport token file %s: no client_id, and the access token does not name one", path)
	}
	fresh, err := a.refreshBeatport(ctx, tok)
	if err != nil {
		return "", fmt.Errorf("%w: put a fresh token response in %s", err, path)
	}
	if err := writeToken(path, fresh); err != nil {
		return "", fmt.Errorf("saving the refreshed Beatport token: %w", err)
	}
	return fresh.AccessToken, nil
}

// refreshBeatport trades tok's refresh token for a new token.
func (a *Analyzer) refreshBeatport(ctx context.Context, tok bpToken) (bpToken, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {tok.ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.api+"/auth/o/token/", strings.NewReader(form.Encode()))
	if err != nil {
		return bpToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return bpToken{}, fmt.Errorf("Beatport token refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return bpToken{}, fmt.Errorf("Beatport token refresh: %s", resp.Status)
	}
	var fresh bpToken
	if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
		return bpToken{}, fmt.Errorf("Beatport token refresh: bad response: %w", err)
	}
	if fresh.AccessToken == "" {
		return bpToken{}, errors.New("Beatport token refresh: no access_token in the response")
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = tok.RefreshToken
	}
	fresh.ClientID = tok.ClientID
	if fresh.ExpiresIn > 0 {
		fresh.ExpiresAt = time.Now().Unix() + fresh.ExpiresIn
	}
	return fresh, nil
}

// writeToken replaces the token file in one rename, readable by the owner
// only.
func writeToken(path string, tok bpToken) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".beatport-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// jwtClientID reads the client_id claim from a JWT access token without
// checking its signature; "" if it has none.
func jwtClientID(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		ClientID string `json:"client_id"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.ClientID
}
