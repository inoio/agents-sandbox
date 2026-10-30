package viperconfig

import (
	"reflect"
	"slices"
)

// This file is the single registry for the launcher config keys. The key sets
// are derived from the Config struct's mapstructure tags rather than
// hand-maintained, so a field cannot be added without its env var and
// config-file handling. The CLI flag binding follows from the same set:
// bindConfigFlags looks each key's flag up by name and skips keys that have no
// flag, so a key does not have to declare that it is a flag.
//
// A field tagged `env:"-"` is deliberately config-file-only: it has no env var
// and no CLI flag. Nested config structs are walked recursively and contribute
// "<prefix>.<key>".

// configKey is one launcher config key.
type configKey struct {
	name string
	// kind is the Go kind of the Config field behind the key. It fixes the type
	// of a bound CLI flag's default, so the value viper stores has the same type
	// as the one it will be merged with.
	kind reflect.Kind
}

// configKeysDerived are all launcher config keys, in Config field order.
var configKeysDerived = deriveConfigKeys()

// configBindKeys are the keys bindConfigFlags offers to viper. It is the whole
// derived set; bindConfigFlags skips keys with no matching flag. The deprecated
// dind alias is deliberately absent: its --dind flag is handled by runoptions,
// and binding it would make IsSet("dind") always true, turning the dind
// precedence check into a spurious deprecation warning.
var configBindKeys = configKeyNames(configKeysDerived)

// configEnvKeys are the keys bound to the AGENTS_SANDBOX_<KEY> env vars (and
// the deprecated OPENCODE_SANDBOX_<KEY> aliases): the derived set plus KeyDind,
// the deprecated docker alias that has no Config field.
var configEnvKeys = slices.Concat(configBindKeys, []string{KeyDind})

// configKeyKinds maps a derived key to its Config field's Go kind.
var configKeyKinds = func() map[string]reflect.Kind {
	kinds := make(map[string]reflect.Kind, len(configKeysDerived))
	for _, key := range configKeysDerived {
		kinds[key.name] = key.kind
	}
	return kinds
}()

func configKeyNames(keys []configKey) []string {
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, key.name)
	}
	return names
}

// deriveConfigKeys walks Config and its nested config structs, collecting every
// mapstructure-tagged key.
func deriveConfigKeys() []configKey {
	return walkConfigKeys(reflect.TypeFor[Config](), "")
}

func walkConfigKeys(t reflect.Type, prefix string) []configKey {
	var keys []configKey
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}
		name, tagged := field.Tag.Lookup("mapstructure")
		if !tagged || name == "-" || field.Tag.Get("env") == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if field.Type.Kind() == reflect.Struct {
			keys = append(keys, walkConfigKeys(field.Type, key)...)
			continue
		}
		keys = append(keys, configKey{name: key, kind: field.Type.Kind()})
	}
	return keys
}
