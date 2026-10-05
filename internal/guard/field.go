package guard

import (
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// stringField reads a string field off a request message.
//
// The field is named the way the contract declares it — snake_case — and is
// resolved through the message's own descriptor, so the table cannot drift
// from the proto: a name that is not in the message is reported, not silently
// compared against an empty string. A hand-written struct tag reader would
// answer the same question, and it would answer it wrongly the day a field is
// renamed, because nothing would fail.
//
// A nil message, a missing field, and a field that is not a string all report
// false. The caller decides what a miss means; the guard refuses on it.
func stringField(message any, field string) (string, bool) {
	if field == "" || message == nil {
		return "", false
	}

	msg, ok := message.(proto.Message)
	if !ok {
		return "", false
	}
	fields := msg.ProtoReflect().Descriptor().Fields()
	descriptor := fields.ByName(protoreflect.Name(field))
	if descriptor == nil || descriptor.Kind() != protoreflect.StringKind {
		return "", false
	}
	return msg.ProtoReflect().Get(descriptor).String(), true
}

// FieldExists reports whether a message carries the named string field. It is
// what the table test asserts with: a declaration naming a field the contract
// does not have is a guard that can never pass.
func FieldExists(message any, field string) error {
	if _, ok := stringField(message, field); ok {
		return nil
	}
	return fmt.Errorf("guard: %s carries no string field %q", messageName(message), field)
}

// messageName names a message in a report, so a wiring defect says which
// request it is about.
func messageName(message any) string {
	if message == nil {
		return "nil"
	}
	if msg, ok := message.(proto.Message); ok {
		return string(msg.ProtoReflect().Descriptor().FullName())
	}
	return reflect.TypeOf(message).String()
}
