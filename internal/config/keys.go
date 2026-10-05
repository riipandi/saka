package config

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	fconfig "github.com/riipandi/saka/framework/config"
)

// Keys returns every config key, sorted. It is the list `config:print` renders
// and the list a test asserts against, so it is derived from the struct rather
// than written out by hand.
func Keys() []string {
	known := DefaultsMap()
	keys := slices.Sorted(maps.Keys(known))
	return keys
}

// Values returns a resolved Config as a flat map keyed by config path, the form
// `config:print` renders.
//
// A duration is written as a number of seconds, the unit the config file uses,
// so a printed value can be compared against the file directly. Everything else
// keeps its Go value, so a caller can tell a bool from a string.
//
// The result is a copy. A caller that must not print a secret passes
// cfg.Redacted().
func Values(cfg Config) map[string]any {
	out := flatten(cfg)
	for key, value := range out {
		out[key] = renderable(value)
	}
	return out
}

// flatten converts a Config into a flat map keyed by config path. It walks the
// struct with reflection instead of marshalling it, because encoding/json/v2
// refuses a time.Duration and the duration fields are part of the schema.
func flatten(cfg Config) map[string]any {
	out := make(map[string]any)
	flattenStruct(reflect.ValueOf(cfg), nil, out)
	return out
}

// flattenStruct records every exported field as a leaf, recursing into a nested
// section. The unexported origin map is skipped: it is state, not config.
func flattenStruct(value reflect.Value, prefix []string, out map[string]any) {
	valueType := value.Type()
	for i := range value.NumField() {
		field := valueType.Field(i)
		if !field.IsExported() {
			continue
		}

		name := keyName(field)
		if name == "" {
			continue
		}
		path := append(append([]string{}, prefix...), name)

		fieldValue := value.Field(i)
		if fieldValue.Kind() == reflect.Struct && !isValueStruct(fieldValue.Type()) {
			flattenStruct(fieldValue, path, out)
			continue
		}
		out[strings.Join(path, fconfig.Delim)] = fieldValue.Interface()
	}
}

// keyName reads the key a field is known by. The json tag wins because it is the
// name a dumped config file shows; the koanf tag is the fallback.
func keyName(field reflect.StructField) string {
	for _, tag := range []string{"json", "koanf"} {
		name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if name == "" || name == "-" {
			continue
		}
		return name
	}
	return ""
}

// isValueStruct reports whether a struct is a value rather than a section, so it
// is kept whole instead of being walked. A time.Time or a time.Duration is a
// value; a struct defined in this package is a section.
func isValueStruct(valueType reflect.Type) bool {
	return valueType.PkgPath() != reflect.TypeFor[Config]().PkgPath()
}
