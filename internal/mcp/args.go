package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

const fieldMaskName protoreflect.FullName = "google.protobuf.FieldMask"

// normalizeArgs rewrites MCP tool arguments into JSON that protojson accepts
// for the message md. Tool schemas describe a FieldMask as an array of
// snake_case paths, but protojson accepts only a comma-separated string of
// lowerCamel paths, so every FieldMask value is converted. Empty or null
// arguments become {}. Unknown keys are left for protojson to reject.
func normalizeArgs(raw json.RawMessage, md protoreflect.MessageDescriptor) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("arguments are not valid JSON: %w", err)
	}
	if v == nil {
		return []byte("{}"), nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	normalizeMessage(obj, md)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// normalizeMessage converts the FieldMask values in obj, a JSON object for
// md, and recurses into its message fields.
func normalizeMessage(obj map[string]any, md protoreflect.MessageDescriptor) {
	for key, val := range obj {
		fd := md.Fields().ByJSONName(key)
		if fd == nil {
			fd = md.Fields().ByTextName(key)
		}
		if fd == nil || fd.Message() == nil || fd.IsMap() {
			continue
		}
		sub := fd.Message()
		switch {
		case sub.FullName() == fieldMaskName && !fd.IsList():
			if mask, ok := fieldMaskString(val); ok {
				obj[key] = mask
			}
		case fd.IsList():
			if list, ok := val.([]any); ok {
				for _, el := range list {
					if m, ok := el.(map[string]any); ok {
						normalizeMessage(m, sub)
					}
				}
			}
		default:
			if m, ok := val.(map[string]any); ok {
				normalizeMessage(m, sub)
			}
		}
	}
}

// fieldMaskString converts a FieldMask given as an array of paths or as a
// comma-separated string into protojson's form. It reports false for any
// other value, which is left for protojson to reject.
func fieldMaskString(val any) (string, bool) {
	var paths []string
	switch v := val.(type) {
	case string:
		if v == "" {
			return "", true
		}
		paths = strings.Split(v, ",")
	case []any:
		for _, el := range v {
			s, ok := el.(string)
			if !ok {
				return "", false
			}
			paths = append(paths, s)
		}
	default:
		return "", false
	}
	for i, p := range paths {
		segs := strings.Split(p, ".")
		for j, s := range segs {
			segs[j] = lowerCamel(s)
		}
		paths[i] = strings.Join(segs, ".")
	}
	return strings.Join(paths, ","), true
}

// lowerCamel converts a snake_case name to lowerCamel: feature_id → featureId.
// Names without underscores are returned unchanged.
func lowerCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if p := parts[i]; p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
