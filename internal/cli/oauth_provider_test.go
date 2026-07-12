package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExchangeCopilotToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		expiry := time.Now().Add(time.Hour).Unix()
		_, _ = w.Write([]byte(`{"token":"copilot-abc","expires_at":` + itoa64(expiry) + `}`))
	}))
	defer srv.Close()

	orig := copilotTokenURL
	copilotTokenURL = srv.URL
	defer func() { copilotTokenURL = orig }()

	token, expiry, err := exchangeCopilotToken(context.Background(), "gh-token")
	if err != nil {
		t.Fatalf("exchangeCopilotToken: %v", err)
	}
	if token != "copilot-abc" {
		t.Fatalf("token = %q, want copilot-abc", token)
	}
	if !expiry.After(time.Now()) {
		t.Fatalf("expiry %v should be in the future", expiry)
	}
}

func TestExchangeCopilotTokenUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	orig := copilotTokenURL
	copilotTokenURL = srv.URL
	defer func() { copilotTokenURL = orig }()

	if _, _, err := exchangeCopilotToken(context.Background(), "bad"); err == nil {
		t.Fatal("expected error for unauthorized exchange")
	}
}

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
