package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type completionURLLinkRoundTripper func(*http.Request) (*http.Response, error)

func (fn completionURLLinkRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestCompletionURLLinkResolverUsesConfiguredKeyThenLegacyFallbackKeys(t *testing.T) {
	var requested string
	resolver := &CompletionURLLinkResolver{client: &http.Client{Transport: completionURLLinkRoundTripper(func(request *http.Request) (*http.Response, error) {
		requested = request.URL.String()
		if request.Header.Get("Accept") != "application/json" {
			t.Fatal("resolver omitted JSON accept header")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"result":{"destination":"https://destination.example.test/after-paid"},"url_link":"https://wrong.example.test"}`)), Request: request}, nil
	})}}
	destination, err := resolver.Resolve(context.Background(), completionTarget{Enabled: true, TargetType: "url_link", SourceURL: "https://source.example.test/resolve", ResponseKey: "result.destination"})
	if err != nil || destination != "https://destination.example.test/after-paid" || requested != "https://source.example.test/resolve" {
		t.Fatalf("destination=%q requested=%q err=%v", destination, requested, err)
	}
}

func TestCompletionURLLinkResolverFallsBackWithoutReportingSuccess(t *testing.T) {
	resolver := &CompletionURLLinkResolver{client: &http.Client{Transport: completionURLLinkRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`failure`)), Request: request}, nil
	})}}
	target := completionTarget{Enabled: true, TargetType: "url_link", SourceURL: "https://source.example.test/resolve", ResponseKey: "url_link", FallbackURL: "/safe-fallback"}
	destination, err := resolver.Resolve(context.Background(), target)
	if err != nil || destination != "/safe-fallback" {
		t.Fatalf("fallback destination=%q err=%v", destination, err)
	}
	target.FallbackURL = ""
	destination, err = resolver.Resolve(context.Background(), target)
	if err != nil || destination != "" {
		t.Fatalf("missing fallback destination=%q err=%v", destination, err)
	}
}
