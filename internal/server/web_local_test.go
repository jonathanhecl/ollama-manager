package server

import (
	"net/url"
	"testing"
)

func TestUnwrapDDGResultURL(t *testing.T) {
	t.Run("uddg on lite redirect", func(t *testing.T) {
		const want = "https://go.dev/doc/tutorial/"
		href := "//duckduckgo.com/l/?uddg=" + url.QueryEscape(want) + "&rut=abc"
		got, ok := unwrapDDGResultURL(href)
		if !ok || got != want {
			t.Fatalf("unwrap(%q) = %q, %v; want %q, true", href, got, ok, want)
		}
	})
	t.Run("https direct", func(t *testing.T) {
		got, ok := unwrapDDGResultURL("https://example.org/path")
		if !ok || got != "https://example.org/path" {
			t.Fatalf("got %q, %v", got, ok)
		}
	})
	t.Run("ddg help rejected", func(t *testing.T) {
		_, ok := unwrapDDGResultURL("https://duckduckgo.com/duckduckgo-help-pages/company/ads")
		if ok {
			t.Fatal("expected ddg help to be rejected")
		}
	})
}
