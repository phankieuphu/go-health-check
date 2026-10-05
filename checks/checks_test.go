package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	ctx := context.Background()

	if err := HTTP(srv.URL, WithHeader("Authorization", "Bearer t"))(ctx); err != nil {
		t.Fatalf("authorized: %v", err)
	}
	if err := HTTP(srv.URL)(ctx); err == nil {
		t.Fatal("unauthorized: want error")
	}
	if err := HTTP(srv.URL, WithStatusCodes(http.StatusUnauthorized))(ctx); err != nil {
		t.Fatalf("custom codes: %v", err)
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := TCP(addr)(context.Background()); err != nil {
		t.Fatalf("open port: %v", err)
	}
	ln.Close()
	if err := TCP(addr)(context.Background()); err == nil {
		t.Fatal("closed port: want error")
	}
}

func TestGoroutineLimit(t *testing.T) {
	if err := GoroutineLimit(1)(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if err := GoroutineLimit(1 << 20)(context.Background()); err != nil {
		t.Fatal(err)
	}
}
