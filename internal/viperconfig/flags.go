package viperconfig

import (
	"reflect"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// bindConfigFlags binds each config-backed flag found on cmd (local or
// inherited) to viper and mirrors its declared default so that an
// unspecified flag with a default does not override env/config. A key with no
// matching flag is skipped, so the binding list is derived from the Config
// struct rather than hand-maintained. A declared flag is never nil, so
// BindPFlag cannot fail here.
func bindConfigFlags(v *viper.Viper, cmd *cobra.Command) {
	for _, key := range configBindKeys {
		flag := findFlag(cmd, key)
		if flag == nil {
			continue
		}
		_ = v.BindPFlag(key, flag)
		// viper's own flag-default fallback returns the same value, so this
		// SetDefault is redundant for every config-backed flag.
		// gomutants:disable-next-line STATEMENT_REMOVE reason="viper's flag-default fallback returns the same value"
		v.SetDefault(key, flagTypedDefault(key, flag))
	}
}

func findFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

// flagTypedDefault converts a flag's declared default into the Go type of the
// Config field behind the key, so viper stores a default of the same type as
// the value it is merged with. viper's own fallback returns the flag's raw
// string for every pflag type it does not cast (uint8 among them), which is why
// the type is taken from the struct rather than from the pflag.
func flagTypedDefault(key string, flag *pflag.Flag) any {
	kind := configKeyKinds[key]
	if kind == reflect.Bool {
		return flag.DefValue == strTrue
	}
	if kind == reflect.Uint8 {
		n, _ := strconv.ParseUint(flag.DefValue, 10, 8)
		return uint8(n)
	}
	return flag.DefValue
}
