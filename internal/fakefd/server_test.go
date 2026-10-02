package fakefd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func get(t *testing.T, h http.Handler, path, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRules(t *testing.T) {
	h := New(Options{APIKey: "k"})
	tests := []struct {
		name, user, pass string
		want             int
	}{
		{"no auth", "", "", 401},
		{"wrong key", "nope", "X", 401},
		{"wrong password", "k", "secret", 401},
		{"right", "k", "X", 200},
	}
	for _, tc := range tests {
		if got := get(t, h, "/api/v2/tickets", tc.user, tc.pass).Code; got != tc.want {
			t.Errorf("%s: %d want %d", tc.name, got, tc.want)
		}
	}
}

func TestEveryNthFailures(t *testing.T) {
	h := New(Options{APIKey: "k", Every429: 3, Every500: 5})
	var codes []int
	for i := 0; i < 6; i++ {
		rec := get(t, h, "/api/v2/tickets", "k", "X")
		codes = append(codes, rec.Code)
		if rec.Code == 429 && rec.Header().Get("Retry-After") != "1" {
			t.Fatal("429 must carry Retry-After")
		}
	}
	want := []int{200, 200, 429, 200, 500, 429}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
}

func TestOutageEnds(t *testing.T) {
	now := time.Unix(1000, 0)
	h := New(Options{APIKey: "k", Outage: 10 * time.Second, Now: func() time.Time { return now }})
	if get(t, h, "/api/v2/tickets", "k", "X").Code != 500 {
		t.Fatal("should be down")
	}
	now = now.Add(11 * time.Second)
	if get(t, h, "/api/v2/tickets", "k", "X").Code != 200 {
		t.Fatal("should be back")
	}
}

func TestRealRateLimitWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	h := New(Options{APIKey: "k", LimitPerMinute: 2, Now: func() time.Time { return now }})
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, get(t, h, "/api/v2/tickets", "k", "X").Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes = %v", codes)
	}
	now = now.Add(61 * time.Second)
	if get(t, h, "/api/v2/tickets", "k", "X").Code != 200 {
		t.Fatal("window should reset")
	}
}

func TestSearchSyntax(t *testing.T) {
	h := New(Options{APIKey: "k"})
	search := func(q string) *httptest.ResponseRecorder {
		return get(t, h, "/api/v2/search/tickets?query="+url.QueryEscape(q), "k", "X")
	}
	if search(`status:2`).Code != 400 {
		t.Fatal("unquoted query must be rejected like the real API")
	}
	if search(`"foo:bar"`).Code != 400 {
		t.Fatal("unsupported field must be rejected")
	}
	rec := search(`"status:2 AND priority:4"`)
	var out struct {
		Total   int `json:"total"`
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Total != 1 || out.Results[0].ID != 204 {
		t.Fatalf("%s %v", rec.Body, err)
	}
	if get(t, h, "/api/v2/search/tickets?page=11&query="+url.QueryEscape(`"status:2"`), "k", "X").Code != 400 {
		t.Fatal("page 11 must be rejected")
	}
}

func TestMissingResourcesAnd404(t *testing.T) {
	h := New(Options{APIKey: "k"})
	for _, p := range []string{"/api/v2/tickets/1", "/api/v2/tickets/1/conversations"} {
		if get(t, h, p, "k", "X").Code != 404 {
			t.Errorf("%s should be 404", p)
		}
	}
	if get(t, h, "/api/v2/tickets?updated_since=garbage", "k", "X").Code != 400 {
		t.Error("bad date should be 400")
	}
}
