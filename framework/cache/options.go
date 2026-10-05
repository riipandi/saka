package cache

import "time"

// Options is what the cache is built from. Every field is explicit, so a
// caller cannot construct a cache whose behavior it cannot name; the values
// come from whoever resolves the configuration, not from this package reading
// anything itself.
type Options struct {
	// Enable switches the cache on. Off, every read is a miss.
	Enable bool
	// Driver names the backend: DriverKVStore reads the shared key-value
	// client, anything else is the in-process memory driver.
	Driver CacheDriver
	// TTL is the default lifetime of an entry that names none.
	TTL time.Duration
	// MaxMemory is the in-process driver's byte budget.
	MaxMemory int64
	// KeyPrefix namespaces the keys this cache owns inside a shared backend.
	// Empty means the bare "cache:" prefix; a deployment that shares the
	// backend across services passes the namespace it wants.
	KeyPrefix string
	// KVEnabled reports whether the key-value backend is available at all.
	// It is a caller fact, not this package's: the factory never opens a
	// backend, and the kvstore driver without one is a run without caching.
	KVEnabled bool
}

// DefaultKeyPrefix namespaces the keys when the options name none.
const DefaultKeyPrefix = "cache:"

// CacheDriver is the name of a cache backend. The values are the
// configuration vocabulary the schema validates against.
type CacheDriver string

const (
	// DriverKVStore reads the shared key-value backend.
	DriverKVStore CacheDriver = "kvstore"
	// DriverMemory is the in-process driver.
	DriverMemory CacheDriver = "memory"
)

// Drivers lists every driver name, for a schema that validates the value.
func Drivers() []string {
	return []string{string(DriverMemory), string(DriverKVStore)}
}
