package radar

import (
	"errors"
	"testing"
	"time"
)

func TestLinkLifecyclePreservesPublicCodeAndUsesCAS(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	link, err := NewDraft(7, "rd_0123456789abcdefghijAB", "Onboarding", "Welcome guide", "", Content{
		Type:           ContentTypeLink,
		DestinationURL: "https://example.com/welcome",
	}, AuthPolicyUnionIDRequired, now)
	if err != nil {
		t.Fatal(err)
	}
	if link.Status != StatusDraft || link.Version != 1 {
		t.Fatalf("new link status/version=%s/%d", link.Status, link.Version)
	}
	originalCode := link.PublicCode

	enabled, changed, err := link.Transition(1, StatusEnabled, now.Add(time.Minute))
	if err != nil || !changed || enabled.Status != StatusEnabled || enabled.Version != 2 {
		t.Fatalf("enable=%#v changed=%v err=%v", enabled, changed, err)
	}
	if enabled.PublicCode != originalCode {
		t.Fatalf("public code changed: %q -> %q", originalCode, enabled.PublicCode)
	}

	if _, _, err = enabled.Transition(1, StatusDisabled, now); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale transition error=%v", err)
	}
	disabled, changed, err := enabled.Transition(2, StatusDisabled, now.Add(2*time.Minute))
	if err != nil || !changed || disabled.Status != StatusDisabled || disabled.Version != 3 {
		t.Fatalf("disable=%#v changed=%v err=%v", disabled, changed, err)
	}
	reenabled, changed, err := disabled.Transition(3, StatusEnabled, now.Add(3*time.Minute))
	if err != nil || !changed || reenabled.Status != StatusEnabled || reenabled.Version != 4 {
		t.Fatalf("re-enable=%#v changed=%v err=%v", reenabled, changed, err)
	}
}

func TestLinkLifecycleRejectsIllegalTransitionsAndNoopsReplay(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	link := mustDraft(t, Content{Type: ContentTypeLink, DestinationURL: "https://example.com/a"}, now)

	if _, _, err := link.Transition(1, StatusDisabled, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("draft -> disabled error=%v", err)
	}
	same, changed, err := link.Transition(1, StatusDraft, now.Add(time.Minute))
	if err != nil || changed || same.Version != link.Version || same.UpdatedAt != link.UpdatedAt {
		t.Fatalf("same-state replay=%#v changed=%v err=%v", same, changed, err)
	}
	if _, _, err = link.Transition(1, Status("archived"), now); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("invalid target error=%v", err)
	}
}

func TestContentValidationByType(t *testing.T) {
	tests := []struct {
		name    string
		content Content
		valid   bool
	}{
		{"link", Content{Type: ContentTypeLink, DestinationURL: "https://example.com/path?q=1"}, true},
		{"link requires https", Content{Type: ContentTypeLink, DestinationURL: "http://example.com"}, false},
		{"link rejects credentials", Content{Type: ContentTypeLink, DestinationURL: "https://user@example.com"}, false},
		{"link rejects media", Content{Type: ContentTypeLink, DestinationURL: "https://example.com", MediaID: 1}, false},
		{"image", Content{Type: ContentTypeImage, MediaID: 11}, true},
		{"image requires media", Content{Type: ContentTypeImage}, false},
		{"image rejects destination", Content{Type: ContentTypeImage, MediaID: 11, DestinationURL: "https://example.com"}, false},
		{"pdf", Content{Type: ContentTypePDF, MediaID: 12}, true},
		{"pdf requires media", Content{Type: ContentTypePDF}, false},
		{"unknown", Content{Type: ContentType("video"), MediaID: 12}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.content.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate() error=%v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func TestDraftValidation(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	if _, err := NewDraft(1, "bad code", "name", "title", "", Content{Type: ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicyAnonymous, now); err == nil {
		t.Fatal("invalid public code accepted")
	}
	if _, err := NewDraft(1, "rd_0123456789abcdefghijAB", "name", "title", "", Content{Type: ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicy("openid_fallback"), now); err == nil {
		t.Fatal("invalid auth policy accepted")
	}
	if _, err := NewDraft(1, "rd_0123456789abcdefghijAB", "name", "title", "", Content{Type: ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicyUnionIDRequired, time.Time{}); err == nil {
		t.Fatal("zero timestamp accepted")
	}
}

func TestLegacyPublicCodeRemainsValidForMigratedShareURLs(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	if _, err := NewDraft(1, "a8f3k2", "name", "title", "", Content{Type: ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicyUnionIDRequired, now); err != nil {
		t.Fatalf("legacy public code rejected: %v", err)
	}
	if PublicCode("a_b").Valid() || PublicCode("123456789").Valid() {
		t.Fatal("unsupported legacy public code shape accepted")
	}
}

func TestRevisionPreservesPublicCode(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	link := mustDraft(t, Content{Type: ContentTypeLink, DestinationURL: "https://example.com/old"}, now)
	updated, err := link.Revise(1, Revision{
		Name:        "revised",
		Title:       "Revised title",
		Description: "safe copy",
		Content:     Content{Type: ContentTypeImage, MediaID: 99},
		AuthPolicy:  AuthPolicyUnionIDRequired,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.PublicCode != link.PublicCode || updated.ID != link.ID {
		t.Fatalf("immutable identity changed: before=%#v after=%#v", link, updated)
	}
	if updated.Version != 2 || updated.Content.Type != ContentTypeImage || updated.AuthPolicy != AuthPolicyUnionIDRequired {
		t.Fatalf("revision not applied: %#v", updated)
	}
}

func mustDraft(t *testing.T, content Content, now time.Time) Link {
	t.Helper()
	link, err := NewDraft(1, "rd_0123456789abcdefghijAB", "name", "title", "", content, AuthPolicyAnonymous, now)
	if err != nil {
		t.Fatal(err)
	}
	return link
}
