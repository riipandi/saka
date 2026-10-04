package config

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListKeysMatchTheStruct(t *testing.T) {
	// The list is what makes a comma-separated string a list, so a new slice
	// field must be listed or a directive naming several entries would decode
	// into a one-element list holding "a,b".
	var found []string
	for _, key := range Keys() {
		if reflect.TypeOf(DefaultsMap()[key]).Kind() == reflect.Slice {
			found = append(found, key)
		}
	}

	assert.Equal(t, listKeys, found,
		"listKeys must list exactly the slice fields on Config")
}

func TestMapKeysMatchTheStruct(t *testing.T) {
	// The list is what makes a JSON object under otel.headers one value rather
	// than a section walked into dotted keys, so a new map field must be listed
	// or its entries would be dropped by the engine's FilterKnown and read as
	// unset.
	var found []string
	for _, key := range Keys() {
		if reflect.TypeOf(DefaultsMap()[key]).Kind() == reflect.Map {
			found = append(found, key)
		}
	}

	assert.Equal(t, mapKeys, found,
		"mapKeys must list exactly the map fields on Config")
}
