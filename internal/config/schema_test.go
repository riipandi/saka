package config

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaCoversEveryKey(t *testing.T) {
	// The schema is generated from the same struct Keys() walks, so a key
	// with no property means the two paths have diverged — a json tag lost,
	// a section added without a field. The sample's keys are all here, and
	// so are the omitted ones (fetcher.user_agent, auth.jwt_algorithm):
	// a generated file leaves them out, the schema still describes them.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	for _, key := range Keys() {
		assert.True(t, schemaHasPath(root, key), "key %s has no schema property", key)
	}
	assert.False(t, schemaHasPath(root, "fetcher.user_agent.root"), "sanity: unknown path refused")
}

func TestSchemaUnionsScalarsWithTheEnvDirective(t *testing.T) {
	// Every scalar accepts an "env:NAME" directive beside its own type; the
	// shared branch is the one $defs entry. The union carries no summary and
	// no default: both belong to the literal branch.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	def, ok := root.Definitions["envDirective"]
	require.True(t, ok, "envDirective must exist in $defs")
	assert.Equal(t, "string", def.Type)

	mode := propertyAt(t, root, "app.mode")
	require.NotNil(t, mode)
	require.Len(t, mode.AnyOf, 2)
	assert.Equal(t, "string", mode.AnyOf[0].Type)
	assert.Equal(t, "#/$defs/envDirective", mode.AnyOf[1].Ref)
	assert.NotEmpty(t, mode.Description, "the union carries the description, at the level the editor shows")
	assert.Equal(t, "development", mode.Default, "the default answers the property, not one branch")

	audit := propertyAt(t, root, "app.audit_retention_days")
	require.NotNil(t, audit)
	require.Len(t, audit.AnyOf, 2)
	assert.Equal(t, "integer", audit.AnyOf[0].Type)
}

func TestSchemaWritesDefaultsAsSeconds(t *testing.T) {
	// A duration default is a plain number of seconds — the unit the file
	// uses — never the int64 nanoseconds the field carries in memory.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	ttl := propertyAt(t, root, "auth.access_ttl")
	require.NotNil(t, ttl)
	assert.InDelta(t, float64(900), ttl.Default, 0)
}

func TestSchemaRefusesExtraProperties(t *testing.T) {
	// Refusing unknown keys is what lets an editor flag a typo instead of
	// the run silently reading the default beside it. The reflector writes
	// "additionalProperties": false, decoded as a boolean schema.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	require.NotNil(t, root.AdditionalProperties, "additionalProperties must be present")
	assert.Equal(t, jsonschema.FalseSchema, root.AdditionalProperties, "additionalProperties must be false")
}

func TestSchemaIsDeterministic(t *testing.T) {
	first, err := SchemaDoc()
	require.NoError(t, err)
	second, err := SchemaDoc()
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "two runs must produce the same bytes")
}

// parseSchema decodes a rendered schema document back into its tree form.
func parseSchema(t *testing.T, raw []byte) *jsonschema.Schema {
	t.Helper()
	doc := &jsonschema.Schema{}
	require.NoError(t, json.Unmarshal(raw, doc), "the rendered schema must parse")
	return doc
}

// propertyAt walks a dotted key to its schema node.
func propertyAt(t *testing.T, root *jsonschema.Schema, key string) *jsonschema.Schema {
	t.Helper()
	parts := strings.Split(key, ".")
	s := root
	for i, part := range parts {
		next, ok := s.Properties.Get(part)
		require.True(t, ok, "path %s missing at %q", key, part)
		if i == len(parts)-1 {
			return next
		}
		s = next
	}
	return nil
}

func TestSchemaUnionsListAndMapKeysWithTheDirective(t *testing.T) {
	// A list accepts the directive as the whole value: the file layer splits
	// the comma-separated variable (listKeys). A map does the same (mapKeys).
	// Without the union the editor refuses "env:LOG_TRANSPORT" as an array.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	for _, key := range append(slices.Clone(listKeys), mapKeys...) {
		prop := propertyAt(t, root, key)
		require.NotNil(t, prop, key)
		require.Len(t, prop.AnyOf, 2, "%s must union with the directive", key)
		assert.Equal(t, "#/$defs/envDirective", prop.AnyOf[1].Ref, key)
	}
	transport := propertyAt(t, root, "log.transport")
	assert.Equal(t, "array", transport.AnyOf[0].Type, "the literal branch stays the array")
}

func TestSchemaDeclaresTheSampleSchemaKey(t *testing.T) {
	// The generated file opens with "$schema"; the schema must accept it or
	// the refusal of extra properties rejects the very file it describes.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	schema := propertyAt(t, root, "$schema")
	require.NotNil(t, schema)
	assert.Equal(t, "string", schema.Type)
	assert.Nil(t, schema.AnyOf, "the schema key names a path, never a directive")
}

func TestSchemaRequiresNothing(t *testing.T) {
	// The file omits keys to take their default, so no required list may
	// stand anywhere in the tree.
	doc, err := SchemaDoc()
	require.NoError(t, err)
	root := parseSchema(t, doc)

	assert.Empty(t, root.Required)
	var walk func(s *jsonschema.Schema)
	walk = func(s *jsonschema.Schema) {
		assert.Empty(t, s.Required)
		if s.Properties != nil {
			for _, prop := range s.Properties.FromOldest() {
				walk(prop)
			}
		}
	}
	walk(root)
}
