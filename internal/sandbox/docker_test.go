package sandbox

import (
	"testing"
	"time"
)

func TestDockerdCheckCmdChecksBinaryPresence(t *testing.T) {
	want := "test -x /usr/bin/dockerd"
	if dockerdCheckCmd != want {
		t.Errorf("dockerdCheckCmd:\n  got:  %q\n  want: %q", dockerdCheckCmd, want)
	}
}

func TestDockerdStartCmdUsesUnixSocketAndVfsConfig(t *testing.T) {
	want := "dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	if dockerdStartCmd != want {
		t.Errorf("dockerdStartCmd:\n  got:  %q\n  want: %q", dockerdStartCmd, want)
	}
}

func TestDockerdReadyCmdRunsDockerInfo(t *testing.T) {
	want := "docker info"
	if dockerdReadyCmd != want {
		t.Errorf("dockerdReadyCmd:\n  got:  %q\n  want: %q", dockerdReadyCmd, want)
	}
}

func TestDockerdReadyTimeoutIs30Seconds(t *testing.T) {
	if dockerdReadyTimeout != 30*time.Second {
		t.Errorf("dockerdReadyTimeout:\n  got:  %v\n  want: 30s", dockerdReadyTimeout)
	}
}

func TestDockerdPollIntervalIsOneSecond(t *testing.T) {
	if dockerdPollInterval != time.Second {
		t.Errorf("dockerdPollInterval:\n  got:  %v\n  want: 1s", dockerdPollInterval)
	}
}
