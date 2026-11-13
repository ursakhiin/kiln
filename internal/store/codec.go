package store

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// marshal encodes m as stable, indented protojson ending in a newline, so the
// same message always produces the same file.
func marshal(m proto.Message) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	return normalizeJSON(raw)
}

// normalizeJSON re-indents JSON with two spaces and a trailing newline.
// protojson deliberately adds extra spaces that vary between builds of the
// binary ("Do not depend on the output being stable"); json.Indent drops all
// whitespace between tokens before re-indenting, so rebuilding or upgrading
// Underdraw doesn't rewrite .underdraw/ files with whitespace-only changes.
func normalizeJSON(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// unmarshal decodes protojson into m.
func unmarshal(b []byte, m proto.Message) error {
	return protojson.Unmarshal(b, m)
}
