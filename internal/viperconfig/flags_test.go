package viperconfig

import (
	"testing"

	"github.com/spf13/pflag"
)

// The flag default type comes from the owning Config field's Go type, so a
// uint8 key needs no special case and a bool key cannot silently bind a string.
func TestFlagTypedDefaultUsesConfigFieldType(t *testing.T) {
	for _, tc := range []struct {
		key  string
		def  string
		want any
	}{
		{keyCPUs, "4", uint8(4)},
		{keyYes, "true", true},
		{KeyOpenLinks, "false", false},
		{KeyDocker, "true", true},
		{"memory", "4G", "4G"},
	} {
		if got := flagTypedDefault(tc.key, &pflag.Flag{DefValue: tc.def}); got != tc.want {
			t.Errorf("flagTypedDefault(%q, %q) = %#v, want %#v", tc.key, tc.def, got, tc.want)
		}
	}
}

func TestFlagTypedDefault(t *testing.T) {
	cpuFlag := &pflag.Flag{Name: "cpus", DefValue: "4"}
	if got := flagTypedDefault("cpus", cpuFlag); got != uint8(4) {
		t.Errorf("flagTypedDefault(cpus) = %#v, want uint8(4)", got)
	}

	boolFlag := &pflag.Flag{Name: "yes", DefValue: "true"}
	if got := flagTypedDefault("yes", boolFlag); got != true {
		t.Errorf("flagTypedDefault(yes) = %#v, want true", got)
	}

	quietFlag := &pflag.Flag{Name: "quiet", DefValue: "true"}
	if got := flagTypedDefault("quiet", quietFlag); got != true {
		t.Errorf("flagTypedDefault(quiet) = %#v, want true", got)
	}
	quietFlagFalse := &pflag.Flag{Name: "quiet", DefValue: "false"}
	if got := flagTypedDefault("quiet", quietFlagFalse); got != false {
		t.Errorf("flagTypedDefault(quiet,false) = %#v, want false", got)
	}

	strFlag := &pflag.Flag{Name: "memory", DefValue: "8G"}
	if got := flagTypedDefault("memory", strFlag); got != "8G" {
		t.Errorf("flagTypedDefault(memory) = %#v, want 8G", got)
	}
}

// The cpus default is parsed with base 10 and 8 bits; both literals are
// observable at values whose base-9/base-11 or 7/9-bit interpretation differs.
func TestFlagTypedDefaultCPUsLimits(t *testing.T) {
	for _, tc := range []struct {
		def  string
		want uint8
	}{
		{"200", 200},
		{"256", 255},
	} {
		flag := &pflag.Flag{Name: "cpus", DefValue: tc.def}
		if got := flagTypedDefault("cpus", flag); got != tc.want {
			t.Errorf("flagTypedDefault(cpus, %q) = %#v, want %v", tc.def, got, tc.want)
		}
	}
}
