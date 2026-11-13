package main

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// inputSchema returns the JSON Schema (2020-12) describing m as an MCP tool's
// inputSchema. Property names are proto field names.
func inputSchema(m *protogen.Message) (map[string]any, error) {
	return messageSchema(m, nil)
}

// messageSchema builds the schema for m; stack holds the full names of the
// messages being expanded, to detect recursion.
func messageSchema(m *protogen.Message, stack []protoreflect.FullName) (map[string]any, error) {
	name := m.Desc.FullName()
	for _, s := range stack {
		if s == name {
			return nil, fmt.Errorf("recursive message %s", name)
		}
	}
	stack = append(stack, name)

	props := map[string]any{}
	for _, f := range m.Fields {
		s, err := fieldSchema(f, stack)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Desc.FullName(), err)
		}
		if d := commentText(f.Comments.Leading); d != "" {
			s["description"] = d
		}
		props[string(f.Desc.Name())] = s
	}

	desc := commentText(m.Comments.Leading)
	for _, o := range m.Oneofs {
		if o.Desc.IsSynthetic() {
			continue
		}
		names := make([]string, len(o.Fields))
		for i, f := range o.Fields {
			names[i] = string(f.Desc.Name())
		}
		line := "Set at most one of: " + strings.Join(names, ", ") + "."
		if desc != "" {
			desc += "\n\n"
		}
		desc += line
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if desc != "" {
		schema["description"] = desc
	}
	return schema, nil
}

func fieldSchema(f *protogen.Field, stack []protoreflect.FullName) (map[string]any, error) {
	switch {
	case f.Desc.IsMap():
		v, err := singularSchema(f.Message.Fields[1], stack)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": v}, nil
	case f.Desc.IsList():
		item, err := singularSchema(f, stack)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": item}, nil
	}
	return singularSchema(f, stack)
}

// singularSchema is the schema for one value of f, ignoring repeated/map.
func singularSchema(f *protogen.Field, stack []protoreflect.FullName) (map[string]any, error) {
	switch f.Desc.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return map[string]any{"type": "string"}, nil
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return map[string]any{"type": "integer"}, nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}, nil
	case protoreflect.EnumKind:
		values := make([]any, len(f.Enum.Values))
		for i, v := range f.Enum.Values {
			values[i] = string(v.Desc.Name())
		}
		return map[string]any{"type": "string", "enum": values}, nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		full := f.Message.Desc.FullName()
		switch full {
		case "google.protobuf.Timestamp":
			return map[string]any{"type": "string", "format": "date-time"}, nil
		case "google.protobuf.FieldMask":
			return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, nil
		}
		if strings.HasPrefix(string(full), "google.protobuf.") {
			return nil, fmt.Errorf("unsupported well-known type %s", full)
		}
		return messageSchema(f.Message, stack)
	}
	return nil, fmt.Errorf("unsupported field kind %s", f.Desc.Kind())
}

// commentText turns a leading comment into plain text: the // markers and one
// leading space per line are removed, and the result is trimmed.
func commentText(c protogen.Comments) string {
	lines := strings.Split(strings.TrimSpace(string(c)), "\n")
	for i, l := range lines {
		l = strings.TrimPrefix(l, "//")
		lines[i] = strings.TrimPrefix(l, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
