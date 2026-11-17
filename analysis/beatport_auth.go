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

// HasBeatportToken reports whether a Beatport token is saved, whether or
// not Beatport will still accept it.
func (a *Analyzer) HasBeatportToken() bool {
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	tok, err := a.readToken()
	return err == nil && (tok.AccessToken != "" || tok.RefreshToken != "")
}

// SaveBeatportToken saves s as the Beatport token: either a token response
// as JSON (access_token, refresh_token, expires_in), or a bare access token,
// which works until it expires.
func (a *Analyzer) SaveBeatportToken(s string) error {
	if a.opts.BeatportTokenFile == "" {
		return errors.New("no place to save a Beatport token")
	}
	s = strings.TrimSpace(s)
	var tok bpToken
	if strings.HasPrefix(s, "{") {
		if err := json.Unmarshal([]byte(s), &tok); err != nil {
			return fmt.Errorf("not a Beatport token response: %w", err)
		}
		if tok.ExpiresIn > 0 {
			tok.ExpiresAt = time.Now().Unix() + tok.ExpiresIn
		}
	} else {
		tok.AccessToken = s
		tok.ExpiresAt = jwtClaims(s).Exp
	}
	if tok.AccessToken == "" && tok.RefreshToken == "" {
		return errors.New("no access_token or refresh_token in what was pasted")
	}
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	return writeToken(a.opts.BeatportTokenFile, tok)
}

// readToken reads BeatportTokenFile; ErrNoBeatportToken when there is none.
func (a *Analyzer) readToken() (bpToken, error) {
	path := a.opts.BeatportTokenFile
	if path == "" {
		return bpToken{}, ErrNoBeatportToken
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bpToken{}, ErrNoBeatportToken
	}
	if err != nil {
		return bpToken{}, err
	}
	var tok bpToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return bpToken{}, fmt.Errorf("Beatport token file %s: %w", path, err)
	}
	return tok, nil
}

// beatportToken returns the access token in BeatportTokenFile, refreshed
// first when it has expired or when force is set (Beatport refused it). A
// refresh writes the new pair back, since Beatport rotates the refresh
// token.
func (a *Analyzer) beatportToken(ctx context.Context, force bool) (string, error) {
	path := a.opts.BeatportTokenFile
	if path == "" {
		return "", ErrNoBeatportToken
	}
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	tok, err := a.readToken()
	if err != nil {
		return "", err
	}
	// An unknown expiry (0) is tried as is: a refusal forces the refresh.
	if !force && tok.AccessToken != "" && (tok.ExpiresAt == 0 || time.Now().Add(30*time.Second).Unix() < tok.ExpiresAt) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("%w: it expired and has no refresh_token", ErrBeatportRefused)
	}
	if tok.ClientID == "" {
		tok.ClientID = jwtClaims(tok.AccessToken).ClientID
	}
	if tok.ClientID == "" {
		return "", fmt.Errorf("%w: no client_id to refresh it with", ErrBeatportRefused)
	}
	fresh, err := a.refreshBeatport(ctx, tok)
	if err != nil {
		return "", err
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
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return bpToken{}, fmt.Errorf("%w: refresh got %s", ErrBeatportRefused, resp.Status)
	}
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

// jwtClaims reads the claims crates uses from a JWT access token, without
// checking its signature; zero when it is not a JWT.
func jwtClaims(jwt string) (c struct {
	ClientID string `json:"client_id"`
	Exp      int64  `json:"exp"`
}) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return c
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err == nil {
		json.Unmarshal(payload, &c)
	}
	return c
}
