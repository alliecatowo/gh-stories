package ghclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeDeviceGitHub(t *testing.T, mode string, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			require.NoError(t, r.ParseForm())
			require.Equal(t, "client-123", r.Form.Get("client_id"))
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "gh-device-code",
				"user_code":        "WDJB-MJHT",
				"verification_uri": "https://github.com/login/device",
				"expires_in":       900,
				"interval":         5,
			})
		case "/login/oauth/access_token":
			if hits != nil {
				*hits++
			}
			require.NoError(t, r.ParseForm())
			require.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", r.Form.Get("grant_type"))
			require.Equal(t, "gh-device-code", r.Form.Get("device_code"))
			switch mode {
			case "pending":
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			case "slow_down":
				json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			case "denied":
				json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
			case "expired":
				json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
			default:
				json.NewEncoder(w).Encode(map[string]string{
					"access_token": "gho_device_abc", "token_type": "bearer", "scope": "",
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRequestDeviceCode(t *testing.T) {
	srv := fakeDeviceGitHub(t, "approved", nil)
	defer srv.Close()

	c := newTestClient(srv.URL)
	code, err := c.RequestDeviceCode(context.Background(), "client-123")
	require.NoError(t, err)
	assert.Equal(t, "gh-device-code", code.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", code.UserCode)
	assert.Equal(t, "https://github.com/login/device", code.VerificationURI)
	assert.Equal(t, 900, code.ExpiresIn)
	assert.Equal(t, 5, code.Interval)
}

func TestExchangeDeviceTokenStatuses(t *testing.T) {
	for mode, want := range map[string]DeviceStatus{
		"pending":   DevicePending,
		"slow_down": DeviceSlowDown,
		"denied":    DeviceDenied,
		"expired":   DeviceExpired,
	} {
		srv := fakeDeviceGitHub(t, mode, nil)
		c := newTestClient(srv.URL)
		got, err := c.ExchangeDeviceToken(context.Background(), "client-123", "secret", "gh-device-code")
		require.NoError(t, err, "mode %s", mode)
		assert.Equal(t, want, got.Status, "mode %s", mode)
		assert.Empty(t, got.AccessToken, "mode %s", mode)
		srv.Close()
	}

	srv := fakeDeviceGitHub(t, "approved", nil)
	defer srv.Close()
	c := newTestClient(srv.URL)
	got, err := c.ExchangeDeviceToken(context.Background(), "client-123", "secret", "gh-device-code")
	require.NoError(t, err)
	assert.Equal(t, DeviceApproved, got.Status)
	assert.Equal(t, "gho_device_abc", got.AccessToken)
}

func TestExchangeDeviceTokenSendsClientSecret(t *testing.T) {
	var hits int
	srv := fakeDeviceGitHub(t, "approved", &hits)
	defer srv.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "secret-xyz", r.Form.Get("client_secret"))
		json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer srv2.Close()

	c := newTestClient(srv2.URL)
	_, err := c.ExchangeDeviceToken(context.Background(), "client-123", "secret-xyz", "gh-device-code")
	require.NoError(t, err)
	assert.Zero(t, hits, "this exchange must hit the secret-checking server, not the shared fake")
}
