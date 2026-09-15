package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DeviceCode is the short-lived authorization request GitHub displays to
// a user. The device code itself is a server-side credential: it is
// retained in the database and never exposed to an HTTP caller.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
}

// DeviceStatus is one poll outcome from GitHub's device grant.
type DeviceStatus string

const (
	DevicePending  DeviceStatus = "pending"
	DeviceSlowDown DeviceStatus = "slow_down"
	DeviceApproved DeviceStatus = "approved"
	DeviceDenied   DeviceStatus = "denied"
	DeviceExpired  DeviceStatus = "expired"
)

// DeviceExchange is the result of one device-code poll. AccessToken is
// present only when Status is DeviceApproved.
type DeviceExchange struct {
	Status      DeviceStatus
	AccessToken string
}

// RequestDeviceCode starts GitHub's device authorization grant:
// POST /login/device/code {client_id} with an empty scope.
func (c *Client) RequestDeviceCode(ctx context.Context, clientID string) (DeviceCode, error) {
	form := url.Values{"client_id": {clientID}}
	body, err := c.deviceRequest(ctx, "/login/device/code", form)
	if err != nil {
		return DeviceCode{}, err
	}
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return DeviceCode{}, fmt.Errorf("ghclient: decode device code response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.VerificationURI == "" || out.ExpiresIn <= 0 {
		return DeviceCode{}, &APIError{Status: http.StatusOK, Message: "github returned incomplete device code"}
	}
	return DeviceCode{
		DeviceCode:      out.DeviceCode,
		UserCode:        out.UserCode,
		VerificationURI: out.VerificationURI,
		ExpiresIn:       out.ExpiresIn,
		Interval:        out.Interval,
	}, nil
}

// ExchangeDeviceToken polls GitHub's device authorization grant. Unlike an
// authorization code, a device code is expressly designed to be polled, so
// pending and slow_down are typed statuses rather than errors.
func (c *Client) ExchangeDeviceToken(ctx context.Context, clientID, secret, deviceCode string) (DeviceExchange, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {secret},
		"device_code":   {deviceCode},
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	body, err := c.deviceRequest(ctx, "/login/oauth/access_token", form)
	if err != nil {
		return DeviceExchange{}, err
	}
	var out ghTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return DeviceExchange{}, fmt.Errorf("ghclient: decode device token response: %w", err)
	}
	switch out.Error {
	case "":
		if out.AccessToken == "" {
			return DeviceExchange{}, &APIError{Status: http.StatusOK, Message: "github returned no access token"}
		}
		return DeviceExchange{Status: DeviceApproved, AccessToken: out.AccessToken}, nil
	case "authorization_pending":
		return DeviceExchange{Status: DevicePending}, nil
	case "slow_down":
		return DeviceExchange{Status: DeviceSlowDown}, nil
	case "access_denied":
		return DeviceExchange{Status: DeviceDenied}, nil
	case "expired_token":
		return DeviceExchange{Status: DeviceExpired}, nil
	default:
		msg := out.Error
		if out.ErrorDescription != "" {
			msg = out.ErrorDescription
		}
		return DeviceExchange{}, &APIError{Status: http.StatusOK, Message: msg}
	}
}

// deviceRequest is the bounded POST helper for the device grant: a single
// attempt with a per-request timeout, a body cap, and redacted logging.
// Like the authorization-code exchange it is deliberately not retried.
func (c *Client) deviceRequest(ctx context.Context, path string, form url.Values) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, perRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.authBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("ghclient: build device request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ghclient: device request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := classify(resp, body); err != nil {
		return nil, err
	}
	return body, nil
}
