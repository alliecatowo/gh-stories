package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Token is the result of a successful authorization-code exchange.
type Token struct {
	AccessToken string
	TokenType   string
	Scope       string
}

// ghTokenResponse mirrors the JSON GitHub's token endpoint returns when asked
// for application/json (otherwise it replies with a query-string body).
type ghTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeCode performs the server-side authorization-code exchange with
// PKCE. The client secret and code verifier never leave this call — they are
// sent directly to GitHub over TLS and are not logged.
//
// This call is deliberately NOT retried: an authorization code is single-use,
// so silently retrying a failed exchange risks sending an already-consumed
// code a second time and turning a transient network hiccup into a confusing
// "bad code" error instead of a clean, single, honest failure.
func (c *Client) ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI, codeVerifier string) (Token, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	reqCtx, cancel := context.WithTimeout(ctx, perRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		c.authBase+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("ghclient: build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("ghclient: token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := readLimited(resp.Body, maxBodyBytes)
	if err != nil {
		return Token{}, err
	}
	if err := classify(resp, body); err != nil {
		return Token{}, err
	}

	var tr ghTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("ghclient: decode token response: %w", err)
	}
	if tr.Error != "" {
		msg := tr.Error
		if tr.ErrorDescription != "" {
			msg = tr.ErrorDescription
		}
		return Token{}, &APIError{Status: http.StatusOK, Message: msg}
	}
	if tr.AccessToken == "" {
		return Token{}, &APIError{Status: http.StatusOK, Message: "github returned no access token"}
	}
	return Token{AccessToken: tr.AccessToken, TokenType: tr.TokenType, Scope: tr.Scope}, nil
}
