package main

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/pluginpb"

	optionsv1 "github.com/ursakhiin/kiln/gen/go/kiln/options/v1"
	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

func testPlugin(t *testing.T) *protogen.Plugin {
	t.Helper()
	files := []protoreflect.FileDescriptor{
		descriptorpb.File_google_protobuf_descriptor_proto,
		timestamppb.File_google_protobuf_timestamp_proto,
		fieldmaskpb.File_google_protobuf_field_mask_proto,
		optionsv1.File_kiln_options_v1_mcp_proto,
		kilnv1.File_kiln_v1_item_proto,
		kilnv1.File_kiln_v1_history_proto,
		kilnv1.File_kiln_v1_service_proto,
	}
	// The compiled-in descriptors keep their go_package option (checked
	// 2026-11-13), so no M parameters are needed.
	req := &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{"kiln/v1/service.proto"}}
	for _, fd := range files {
		req.ProtoFile = append(req.ProtoFile, protodesc.ToFileDescriptorProto(fd))
	}
	p, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func findMessage(t *testing.T, p *protogen.Plugin, name string) *protogen.Message {
	t.Helper()
	for _, f := range p.Files {
		if f.Desc.Path() != "kiln/v1/service.proto" {
			continue
		}
		for _, m := range f.Messages {
			if string(m.Desc.Name()) == name {
				return m
			}
		}
	}
	t.Fatalf("message %s not found", name)
	return nil
}

// schemaOf finds message name in kiln/v1/service.proto and returns its schema
// re-encoded as JSON for comparison.
func schemaOf(t *testing.T, name string) string {
	t.Helper()
	s, err := inputSchema(findMessage(t, testPlugin(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return jsonOf(t, s)
}

func schemaTree(t *testing.T, name string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(schemaOf(t, name)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func at(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	for _, p := range path {
		props, ok := m["properties"].(map[string]any)
		if !ok {
			t.Fatalf("no properties at %q", p)
		}
		m, ok = props[p].(map[string]any)
		if !ok {
			t.Fatalf("no property %q", p)
		}
	}
	return m
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSchemaScalars(t *testing.T) {
	want := `{"additionalProperties":false,"properties":{"id":{"type":"string"}},"type":"object"}`
	if got := schemaOf(t, "GetItemRequest"); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestSchemaEnumsAndIntegers(t *testing.T) {
	s := schemaTree(t, "SetStatusRequest")
	if got := jsonOf(t, at(t, s, "base_revision")); got != `{"type":"integer"}` {
		t.Fatalf("base_revision: %s", got)
	}
	want := `{"enum":["STATUS_UNSPECIFIED","STATUS_DRAFT","STATUS_READY","STATUS_IN_PROGRESS","STATUS_IN_REVIEW","STATUS_DONE"],"type":"string"}`
	if got := jsonOf(t, at(t, s, "status")); got != want {
		t.Fatalf("status: %s", got)
	}
}

func TestSchemaNestedTypes(t *testing.T) {
	s := schemaTree(t, "UpdateItemRequest")
	checks := []struct {
		path []string
		want string
	}{
		{[]string{"update_mask"}, `{"items":{"type":"string"},"type":"array"}`},
		{[]string{"item", "create_time"}, `{"format":"date-time","type":"string"}`},
		{[]string{"item", "task", "work_state", "extra"}, `{"additionalProperties":{"type":"string"},"type":"object"}`},
		{[]string{"item", "task", "work_state", "files"}, `{"items":{"type":"string"},"type":"array"}`},
	}
	for _, c := range checks {
		if got := jsonOf(t, at(t, s, c.path...)); got != c.want {
			t.Errorf("%v: got %s, want %s", c.path, got, c.want)
		}
	}
	item := at(t, s, "item")
	if got := item["description"]; got != "Set at most one of: project, feature, task." {
		t.Errorf("item description = %q", got)
	}
}

func TestSchemaRejectsRecursion(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/rec.proto"),
		Package: proto.String("test"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/rec;rec")},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Node"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("child"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".test.Node"),
				JsonName: proto.String("child"),
			}},
		}},
	}
	p, err := protogen.Options{}.New(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test/rec.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = inputSchema(p.Files[0].Messages[0])
	if err == nil || !strings.Contains(err.Error(), "recursive message") {
		t.Fatalf("err = %v", err)
	}
}

func TestSchemaRejectsUnsupportedWellKnownType(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/wkt.proto"),
		Package:    proto.String("test"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Options:    &descriptorpb.FileOptions{GoPackage: proto.String("example.com/wkt;wkt")},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Holder"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("opts"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".google.protobuf.FileOptions"),
				JsonName: proto.String("opts"),
			}},
		}},
	}
	p, err := protogen.Options{}.New(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test/wkt.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto), fdp},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = inputSchema(p.FilesByPath["test/wkt.proto"].Messages[0])
	if err == nil || !strings.Contains(err.Error(), "unsupported well-known type google.protobuf.FileOptions") {
		t.Fatalf("err = %v", err)
	}
}

func TestSchemaFieldCommentKeepsOneofHint(t *testing.T) {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/cmt.proto"),
		Package: proto.String("test"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/cmt;cmt")},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Inner"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("a"), Number: proto.Int32(1), Label: opt, Type: str, OneofIndex: proto.Int32(0), JsonName: proto.String("a")},
					{Name: proto.String("b"), Number: proto.Int32(2), Label: opt, Type: str, OneofIndex: proto.Int32(0), JsonName: proto.String("b")},
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("kind")}},
			},
			{
				Name: proto.String("Outer"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name: proto.String("inner"), Number: proto.Int32(1), Label: opt,
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".test.Inner"), JsonName: proto.String("inner"),
				}},
			},
		},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{
			{Path: []int32{4, 1, 2, 0}, Span: []int32{0, 0, 0}, LeadingComments: proto.String(" The inner thing.\n")},
		}},
	}
	p, err := protogen.Options{}.New(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test/cmt.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := inputSchema(p.FilesByPath["test/cmt.proto"].Messages[1])
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal([]byte(jsonOf(t, s)), &tree); err != nil {
		t.Fatal(err)
	}
	want := "The inner thing.\n\nSet at most one of: a, b."
	if got := at(t, tree, "inner")["description"]; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestCommentText(t *testing.T) {
	got := commentText(protogen.Comments("// First line.\n//   indented\n//\n// Last.\n"))
	want := "First line.\n  indented\n\nLast."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
