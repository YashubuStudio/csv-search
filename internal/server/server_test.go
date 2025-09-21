package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeSearchRequestPostMaxResults(t *testing.T) {
	s := &Server{}
	body := `{"query":"テスト","dataset":"textile_jobs","max_results":2,"summary_only":true,"filters":{"得意先名":"艶栄工業㈱"}}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	decoded, err := s.decodeSearchRequest(req)
	if err != nil {
		t.Fatalf("decodeSearchRequest returned error: %v", err)
	}
	if decoded.Query != "テスト" {
		t.Fatalf("unexpected query: %q", decoded.Query)
	}
	if decoded.Dataset != "textile_jobs" {
		t.Fatalf("unexpected dataset: %q", decoded.Dataset)
	}
	if decoded.TopK != 2 {
		t.Fatalf("expected TopK=2, got %d", decoded.TopK)
	}
	if !decoded.SummaryOnly {
		t.Fatalf("expected SummaryOnly=true")
	}
	if len(decoded.Filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(decoded.Filters))
	}
	filter := decoded.Filters[0]
	if filter.Field != "得意先名" || filter.Value != "艶栄工業㈱" {
		t.Fatalf("unexpected filter parsed: %+v", filter)
	}
}

func TestDecodeSearchRequestGetMaxResults(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/query?query=hello&max_results=3&summary_only=true", nil)

	decoded, err := s.decodeSearchRequest(req)
	if err != nil {
		t.Fatalf("decodeSearchRequest returned error: %v", err)
	}
	if decoded.Query != "hello" {
		t.Fatalf("unexpected query: %q", decoded.Query)
	}
	if decoded.TopK != 3 {
		t.Fatalf("expected TopK=3, got %d", decoded.TopK)
	}
	if !decoded.SummaryOnly {
		t.Fatalf("expected SummaryOnly=true")
	}
}
