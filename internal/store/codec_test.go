package store

import (
	"testing"

	"google.golang.org/protobuf/proto"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
)

// protojson's extra spaces only appear in some builds, so this feeds them in
// directly: normalizeJSON must remove whitespace between tokens in every
// build, and keep whitespace inside strings.
func TestNormalizeJSONStripsExtraSpaces(t *testing.T) {
	got, err := normalizeJSON([]byte(`{"a":  "x  y",  "b": {"c":1}}`))
	want := "{\n  \"a\": \"x  y\",\n  \"b\": {\n    \"c\": 1\n  }\n}\n"
	if err != nil || string(got) != want {
		t.Fatalf("normalizeJSON = %q, %v; want %q", got, err, want)
	}
}

func TestMarshalIsDeterministic(t *testing.T) {
	item := &underdrawv1.Item{
		Id: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Revision: 3, Title: "Sync engine",
		Kind: &underdrawv1.Item_Task{Task: &underdrawv1.Task{
			FeatureId: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Status: underdrawv1.Status_STATUS_READY, Rank: "i",
			WorkState: &underdrawv1.WorkState{Extra: map[string]string{"b": "2", "a": "1"}},
		}},
	}
	want := `{
  "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "revision": "3",
  "title": "Sync engine",
  "task": {
    "feature_id": "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    "status": "STATUS_READY",
    "rank": "i",
    "work_state": {
      "extra": {
        "a": "1",
        "b": "2"
      }
    }
  }
}
`
	// protojson inserts extra spaces depending on a hash of the binary, so
	// its raw output differs between builds. Comparing to an exact expected
	// file catches that in any build where the spaces appear.
	got, err := marshal(item)
	if err != nil || string(got) != want {
		t.Fatalf("marshal:\n%s\nerr=%v", got, err)
	}
	var back underdrawv1.Item
	if err := unmarshal([]byte(want), &back); err != nil || !proto.Equal(&back, item) {
		t.Fatalf("round trip failed: %v", err)
	}
}
