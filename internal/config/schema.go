package config

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
)

// SchemaFileName is also referenced from the generated sample file; the schema
// and the file it describes travel together (see file.go).

// SchemaDoc builds the JSON Schema for app.config.json from the Config struct
// itself, so a new key needs no second entry anywhere: the field's json tag is
// the property name, its Go doc comment is the description, and its built-in
// default in DefaultsMap is the schema default. A hand-maintained schema would
// drift the first time someone added a key and forgot the file.
//
// Every scalar accepts either its own type or an "env:NAME" directive, because
// the config file resolves values from the environment table (see
// configFileLayer). The reflector cannot express that union from struct tags,
// so the walk afterwards wraps each scalar property in anyOf with the shared
// envDirective definition — the same shape the schema shipped by hand before
// the generator existed.
func SchemaDoc() ([]byte, error) {
	r := &jsonschema.Reflector{
		// The struct is the file: every field is written out, and a missing
		// one means the built-in default, so nothing is "required" in the
		// schema sense. Refusing extra properties is what lets an editor
		// flag a typo ("secrete_key") instead of the run silently reading
		// the built-in default beside it.
		AllowAdditionalProperties: false,
		ExpandedStruct:            true,
		// One tree, no $defs: every section inlines under its key, which is
		// how the schema shipped by hand and how a reader walks it. The one
		// definition below is envDirective, added after reflection.
		DoNotReference: true,
	}
	if err := r.AddGoComments("github.com/riipandi/saka/internal/config", "."); err != nil {
		return nil, fmt.Errorf("config: schema comments: %w", err)
	}

	doc := r.Reflect(&Config{})
	// draft-07, the declaration the committed schema always carried: the
	// keyword set this document uses ($defs, anyOf, $ref by JSON pointer,
	// additionalProperties) predates 2019, and a validator that rejects the
	// 2020-12 meta-schema's $dynamicRef would refuse the file outright.
	doc.Version = "http://json-schema.org/draft-07/schema#"
	doc.ID = ""
	doc.Title = "Saka application configuration"
	doc.Description = schemaDescription

	// The directive definition is the one shared branch every scalar unions
	// with; the walk only points at it by name. DoNotReference leaves the
	// map nil, so it is created here.
	if doc.Definitions == nil {
		doc.Definitions = jsonschema.Definitions{}
	}
	doc.Definitions["envDirective"] = &jsonschema.Schema{
		Description: "A reference into the environment table the file resolves from: the string \"env:NAME\".",
		Type:        "string",
		Pattern:     envDirectivePattern,
	}

	// The sample file opens with a "$schema" key naming this document, so an
	// editor picks up completion and validation. Declare it, or the refusal
	// of extra properties would reject the generated file itself.
	doc.Properties.Set("$schema", &jsonschema.Schema{
		Type:        "string",
		Description: "JSON Schema the file validates against.",
	})

	// Defaults come from DefaultsMap, not from struct tags: one source of
	// truth, the same map Sample() renders from. A flat key with no property
	// means the struct gained or lost a field the flat map does not know —
	// or that flatten() and the struct disagree — and the run must fail
	// rather than ship a schema with a default pointing nowhere.
	flat := DefaultsMap()
	if err := assignDefaults(doc, flat, "$"); err != nil {
		return nil, err
	}

	// The reflector marks every field required because none carries a json
	// omitempty tag, but the file is allowed to leave any key out — that is
	// what the built-in default is for — and the "$schema" key the sample
	// writes is not a config key at all. Required lists would make every
	// generated file invalid, so they are stripped.
	stripRequired(doc)

	wrapped, err := wrapScalars(doc, "envDirective", "$", false)
	if err != nil {
		return nil, err
	}
	if !wrapped {
		return nil, fmt.Errorf("config: schema walk wrapped no scalars; the Config struct no longer matches the walk")
	}

	// List and map values accept the directive as the whole value: the file
	// layer splits a comma-separated variable into the list (listKeys) and
	// parses a directive into header entries (mapKeys), so the schema must
	// offer the string directive beside the array and the object — exactly
	// the warning an editor gives otherwise ("expected array, got env:...").
	for _, key := range append(slices.Clone(listKeys), mapKeys...) {
		if !schemaHasPath(doc, key) {
			return nil, fmt.Errorf("config: schema key %s has no property in the reflected Config", key)
		}
		unionWithDirective(doc, key)
	}

	// The schema must name every key Keys() reports. A struct field without
	// a json tag is invisible to both, but a key in Keys() with no property
	// means the flatten path and the reflection path have diverged.
	for _, key := range Keys() {
		if !schemaHasPath(doc, key) {
			return nil, fmt.Errorf("config: schema key %s has no property in the reflected Config", key)
		}
	}

	out, err := doc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("config: render schema: %w", err)
	}
	return indentJSON(out, "    ")
}

// envDirectivePattern is the shape of an env directive value in the file.
const envDirectivePattern = "^env:[A-Za-z_][A-Za-z0-9_]*$"

// schemaDescription is the note the schema opens with: the directive rule and
// where the truth lives.
const schemaDescription = "Schema for app.config.json. Every scalar value may be written literally " +
	"or as an `env:NAME` directive — a reference into the environment table the file resolves from — " +
	"so boolean and number keys accept the directive string as well as their own type. Lists accept " +
	"the directive as a whole value (comma-separated) and header maps as an object or a directive. " +
	"Durations are written as plain numbers of seconds. The \"$schema\" key names this file, beside " +
	"the config file. Generated from internal/config: every key has a default in config.Default, " +
	"a rule in config.Validate, and an entry here."

// assignDefaults walks the schema beside the flat default map and sets each
// scalar property's default from its key. Objects recurse and consume nothing.
// The wrapper anyOf is assigned after this walk, so the default lands on the
// literal branch alone — a default beside a union would claim the directive
// string defaults to a bool or a number.
func assignDefaults(s *jsonschema.Schema, flat map[string]any, path string) error {
	for name, prop := range s.Properties.FromOldest() {
		keyPath := strings.TrimPrefix(path+"."+name, "$.")
		if prop.Properties != nil && prop.Properties.Len() > 0 {
			if err := assignDefaults(prop, flat, keyPath); err != nil {
				return err
			}
			continue
		}
		if def, ok := flat[keyPath]; ok {
			// An empty string default says nothing a reader could act on;
			// the reference schema leaves those out.
			if s, isStr := def.(string); isStr && s == "" {
				continue
			}
			prop.Default = schemaDefault(prop.Type, def)
		}
	}
	return nil
}

// schemaDefault converts a built-in default into the JSON value the schema
// declares. time.Duration reaches here as int64 nanoseconds; the schema and
// the file speak plain numbers of seconds, the unit every duration key uses.
func schemaDefault(propType string, value any) any {
	if d, ok := value.(time.Duration); ok {
		if propType == "integer" || propType == "number" {
			return int64(d / time.Second)
		}
	}
	return value
}

// wrapScalars walks a schema tree and gives every scalar property (string,
// boolean, integer, number — durations included) an anyOf union with the named
// env directive definition. Enum properties keep their enum on the literal
// branch: a directive string is accepted beside the fixed values, the way
// app.mode resolves from the environment table too. Objects recurse. Arrays
// stay as they are — the list keys that accept a directive carry it as the
// whole value, which the file layer splits, and the reflection of []string
// already says "array of strings".
//
// It reports whether anything was wrapped. A false return means the Config
// struct stopped matching what this walk understands, and the caller fails
// the run rather than ship a schema without the directive unions.
func wrapScalars(s *jsonschema.Schema, directiveRef, path string, inRoot bool) (bool, error) {
	_ = inRoot
	wrapped := false
	for name, prop := range s.Properties.FromOldest() {
		if name == "$schema" {
			continue // names this document, never a directive
		}
		keyPath := path + "." + name
		switch {
		case prop.Properties != nil && prop.Properties.Len() > 0:
			sub, err := wrapScalars(prop, directiveRef, keyPath, false)
			if err != nil {
				return false, err
			}
			wrapped = wrapped || sub
		case prop.Type == "string" || prop.Type == "boolean" || prop.Type == "integer" || prop.Type == "number":
			literal := *prop
			prop.Default = literal.Default
			prop.AnyOf = []*jsonschema.Schema{&literal, {Ref: "#/$defs/" + directiveRef}}
			// Description and default move to the union: the default answers
			// the property, whichever branch the file chose, and a summary
			// reads at the level the editor shows it. The literal branch
			// keeps only what describes its own values.
			prop.Ref = ""
			prop.Type = ""
			prop.Enum = nil
			prop.Pattern = ""
			literal.Description = ""
			wrapped = true
		}
	}
	return wrapped, nil
}

// stripRequired removes every required list from the tree. The generated file
// omits keys to take their default, and the "$schema" key is not a config
// property, so a required list would reject the very file the schema is meant
// to describe. additionalProperties stays false: that is the typo check.
func stripRequired(s *jsonschema.Schema) {
	s.Required = nil
	for _, prop := range s.Properties.FromOldest() {
		if prop.Properties != nil && prop.Properties.Len() > 0 {
			stripRequired(prop)
		}
	}
}

// unionWithDirective wraps the property at a dotted key in an anyOf with the
// env directive branch, the way wrapScalars does for scalars but for the list
// and map keys: the whole value may be one "env:NAME" string the file layer
// splits or parses. The array or object stays on the literal branch.
func unionWithDirective(root *jsonschema.Schema, key string) {
	parts := strings.Split(key, ".")
	s := root
	for i, part := range parts {
		next, ok := s.Properties.Get(part)
		if !ok {
			return
		}
		if i == len(parts)-1 {
			literal := *next
			next.Default = literal.Default
			next.AnyOf = []*jsonschema.Schema{&literal, {Ref: "#/$defs/envDirective"}}
			next.Type = ""
			next.Items = nil
			next.AdditionalProperties = nil
			literal.Description = ""
			return
		}
		s = next
	}
}

// schemaHasPath reports whether the dotted key exists as a property path in
// the schema. The "$schema" key the sample writes is not a config key and is
// not expected here.
func schemaHasPath(root *jsonschema.Schema, key string) bool {
	parts := strings.Split(key, ".")
	s := root
	for i, part := range parts {
		if s == nil || s.Properties == nil {
			return false
		}
		next, ok := s.Properties.Get(part)
		if !ok {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		s = next
	}
	return false
}

// indentJSON re-indents generated JSON to the four spaces the repository's
// generated files use. The document is copied token by token: feeding the
// bytes straight to json/v2 would encode them as a base64 string, and
// Schema.MarshalJSON has already produced the ordering the library chose.
func indentJSON(raw []byte, indent string) ([]byte, error) {
	var out bytes.Buffer
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	enc := jsontext.NewEncoder(&out, jsontext.WithIndent(indent))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("config: indent: %w", err)
		}
		if err := enc.WriteToken(tok); err != nil {
			return nil, fmt.Errorf("config: indent: %w", err)
		}
	}
	return out.Bytes(), nil
}
