package http

import (
	"reflect"
	"testing"
)

func TestPublicFiltersNeverWidenFrozenScope(t *testing.T) {
	for _, test := range []struct{ base, extra, want []string }{{[]string{"active_used"}, nil, []string{"active_used"}}, {[]string{"active_used"}, []string{"active_unused"}, []string{"__no_match__"}}, {[]string{"a", "b"}, []string{"b", "c"}, []string{"b"}}} {
		if got := intersectValues(test.base, test.extra); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("intersection %v != %v", got, test.want)
		}
	}
}

func TestDashboardCursorBoundToScopeAndQuery(t *testing.T) {
	q := queryRequest{Limit: 10, Filters: queryFilters{Stage: []string{"active_used"}}}
	original := queryFingerprint(q, "link-a")
	q.Cursor = "next"
	q.ProjectionID = 12
	if queryFingerprint(q, "link-a") != original {
		t.Fatal("cursor and projection are independently pinned")
	}
	if queryFingerprint(q, "link-b") == original {
		t.Fatal("cursor must not cross share scopes")
	}
	q.Filters.Stage = []string{"active_unused"}
	if queryFingerprint(q, "link-a") == original {
		t.Fatal("cursor must not cross queries")
	}
}
