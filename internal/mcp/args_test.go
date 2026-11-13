package mcp

import (
	"encoding/json"
	"testing"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

func TestNormalizeArgs(t *testing.T) {
	md := (&kilnv1.UpdateItemRequest{}).ProtoReflect().Descriptor()
	tests := []struct {
		name, in, want string
	}{
		{"mask array", `{"update_mask":["title","task.feature_id"]}`, `{"update_mask":"title,task.featureId"}`},
		{"mask string", `{"update_mask":"task.feature_id"}`, `{"update_mask":"task.featureId"}`},
		{"null", `null`, `{}`},
		{"empty", ``, `{}`},
		{"int64 string", `{"base_revision":"3"}`, `{"base_revision":"3"}`},
		{"large number", `{"base_revision":9007199254740993}`, `{"base_revision":9007199254740993}`},
		{"lowerCamel key", `{"updateMask":["task.feature_id"]}`, `{"updateMask":"task.featureId"}`},
		{"nested message", `{"item":{"task":{"feature_id":"x"}},"update_mask":["task.feature_id"]}`,
			`{"item":{"task":{"feature_id":"x"}},"update_mask":"task.featureId"}`},
		{"unknown key", `{"bogus":{"update_mask":["a_b"]}}`, `{"bogus":{"update_mask":["a_b"]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeArgs(json.RawMessage(tt.in), md)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNormalizeArgsRejectsNonObject(t *testing.T) {
	md := (&kilnv1.UpdateItemRequest{}).ProtoReflect().Descriptor()
	if _, err := normalizeArgs(json.RawMessage(`[1]`), md); err == nil {
		t.Fatal("want an error for a non-object")
	}
}
