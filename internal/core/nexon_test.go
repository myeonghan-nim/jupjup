package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	c := NewClient("test-key")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c, srv
}

func TestFetchLowestTwo(t *testing.T) {
	var gotKey, gotKeyword string
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-nxopen-api-key")
		gotKeyword = r.URL.Query().Get("keyword")
		// 정렬 안 된 응답 + 검색어 미포함 항목 1개 섞기
		w.Write([]byte(`{"auction_item":[
			{"item_display_name":"마나 허브","auction_price_per_unit":1200},
			{"item_display_name":"허브 조각","auction_price_per_unit":1},
			{"item_display_name":"축복받은 마나 허브","auction_price_per_unit":100}]}`))
	})
	defer srv.Close()

	lowest, next, err := c.FetchLowestTwo(context.Background(), "마나 허브")
	if err != nil {
		t.Fatal(err)
	}
	if lowest != 100 || next != 1200 {
		t.Errorf("got (%d, %d), want (100, 1200)", lowest, next)
	}
	if gotKey != "test-key" {
		t.Errorf("API 키 헤더 누락: %q", gotKey)
	}
	if gotKeyword != "마나 허브" {
		t.Errorf("keyword 파라미터: %q", gotKeyword)
	}
}

func TestFetchLowestTwoNotEnough(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auction_item":[{"item_display_name":"마나 허브","auction_price_per_unit":100}]}`))
	})
	defer srv.Close()
	_, _, err := c.FetchLowestTwo(context.Background(), "마나 허브")
	if !errors.Is(err, ErrNotEnoughListings) {
		t.Errorf("want ErrNotEnoughListings, got %v", err)
	}
}

func TestFetchLowestTwoUnauthorized(t *testing.T) {
	for _, code := range []int{401, 403} {
		c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			w.Write([]byte(`{"error":{"name":"OPENAPI00004","message":"invalid key"}}`))
		})
		_, _, err := c.FetchLowestTwo(context.Background(), "x")
		srv.Close()
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("status %d: want ErrUnauthorized, got %v", code, err)
		}
	}
}

func TestFetchLowestTwoTimeout(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	})
	defer srv.Close()
	c.HTTP = &http.Client{Timeout: 50 * time.Millisecond}

	_, _, err := c.FetchLowestTwo(context.Background(), "x")
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Errorf("want timeout error, got %v", err)
	}
}

func TestFetchLowestTwoRateLimited(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"name":"OPENAPI00007","message":"rate limited"}}`))
	})
	defer srv.Close()
	_, _, err := c.FetchLowestTwo(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "OPENAPI00007") {
		t.Errorf("429 에러에 API 에러명 미포함: %v", err)
	}
}

func TestKeyStage(t *testing.T) {
	cases := []struct {
		key  string
		want Stage
	}{
		{"test_abcdef", StageDev},
		{"live_abcdef", StageService},
		{"abcdef", StageDev}, // 알 수 없는 형식 → 한도가 낮은 쪽으로
	}
	for _, c := range cases {
		if got := KeyStage(c.key); got != c.want {
			t.Errorf("KeyStage(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}
