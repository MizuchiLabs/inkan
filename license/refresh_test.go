package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRefresh(t *testing.T) {
	_, priv := testKeypair(t)
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(expires time.Time, with ed25519.PrivateKey) string {
		key, err := Issue("myapp", Claims{Plan: "pro", ExpiresAt: expires.Unix(), ID: "sub_1"}, with)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	old := issue(time.Now().Add(-time.Hour), priv)
	renewed := issue(time.Now().Add(time.Hour), priv)

	tests := []struct {
		name    string
		status  int
		answer  string
		want    string
		wantErr string
	}{
		{"renewed", http.StatusOK, renewed + "\n", renewed, ""},
		{"nothing new was paid", http.StatusOK, old, old, ""},
		{"key of a stranger", http.StatusOK, issue(time.Now().Add(time.Hour), stranger), "", ErrInvalidSig.Error()},
		{"not a key at all", http.StatusOK, "<html>Sign in to the hotel wifi</html>", "", ErrInvalidFormat.Error()},
		{
			"turned down",
			http.StatusNotFound,
			"no license behind this key\n",
			"",
			"404 Not Found: no license behind this key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || string(sent) != old {
					t.Errorf("the worker got %s %q, want a POST of the key", r.Method, sent)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.answer)
			}))
			defer srv.Close()

			got, err := Refresh(t.Context(), srv.URL, " "+old+"\n", "myapp")
			if got != tt.want {
				t.Fatalf("got key %q, want %q", got, tt.want)
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("got error %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if got, err := Refresh(t.Context(), srv.URL, old, "myapp"); err == nil || got != "" {
		t.Fatalf("an unreachable worker must be an error, got %q, %v", got, err)
	}
}
