package sandbox

import (
	"testing"
	"time"
)

func TestDockerdCheckCmdChecksBinaryPresence(t *testing.T) {
	want := "test -x /usr/bin/dockerd"
	if dockerdBinaryCheckCmd != want {
		t.Errorf("dockerdCheckCmd:\n  got:  %q\n  want: %q", dockerdBinaryCheckCmd, want)
	}
}

func TestDockerdStartCmdUsesUnixSocketAndVfsConfig(t *testing.T) {
	want := "pkill dockerd 2>/dev/null || : && find /run /var/run -iname 'docker*.pid' -delete 2>/dev/null && sleep 1 && dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	if dockerdRestartCmd != want {
		t.Errorf("dockerdRestartCmd:\n  got:  %q\n  want: %q", dockerdRestartCmd, want)
	}
}

func TestDockerdReadyCmdRunsDockerInfo(t *testing.T) {
	want := "docker info"
	if dockerdReadyCmd != want {
		t.Errorf("dockerdReadyCmd:\n  got:  %q\n  want: %q", dockerdReadyCmd, want)
	}
}

func TestDockerdReadyTimeoutIs10Seconds(t *testing.T) {
	if dockerdReadyTimeout != 10*time.Second {
		t.Errorf("dockerdReadyTimeout:\n  got:  %v\n  want: 30s", dockerdReadyTimeout)
	}
}

func TestDockerdPollIntervalIsOneSecond(t *testing.T) {
	if dockerdPollInterval != time.Second {
		t.Errorf("dockerdPollInterval:\n  got:  %v\n  want: 1s", dockerdPollInterval)
	}
}
