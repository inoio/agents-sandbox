---
title: Trust the Sandbox CA in Java
description: "Make a JDK in the sandbox VM trust the microsandbox interception CA, by hand or automatically on every boot."
layout: default
parent: Recipes
nav_order: 40
---

# Trust the Sandbox CA in Java

Once a [secret]({% link configuration/secrets.md %}) is configured, the proxy terminates TLS towards the allowed hosts.
A JVM does not read the system trust store for that, so every HTTPS request fails with
`SunCertPathBuilderException: unable to find valid certification path to requested target` — Gradle, Maven, and any
other Java tool included. microsandbox's trust helper (`msb-trust`, installed in the runner image) imports the CA into
the JDK's default trust store and preserves the certificates already there.

## Install a JDK

The helper needs `keytool`, so a JDK has to be in the image. Add it to the project Dockerfile when the base does not
provide one:

```dockerfile
# .agents-sandbox/Dockerfile
FROM agents-sandbox/runner-base:latest
RUN apt-get update && apt-get install -y --no-install-recommends openjdk-21-jdk-headless && rm -rf /var/lib/apt/lists/*
```

Alternatively point `FROM` at a JDK image and treat it as a [custom base]({% link runner-image.md %}#the-base-image-contract).

## Import the CA

The JDK's trust store is root-owned, so import it from a root shell in the VM:

```console
agents-sandbox shell --root
```

Then, in that shell:

```console
msb-trust java
```

It writes to the JDK's default trust store (`$JAVA_HOME/lib/security/cacerts`, usually a symlink to
`/etc/ssl/certs/java/cacerts`) under the alias `microsandbox-interception`. Verify the import:

```console
keytool -list -cacerts -alias microsandbox-interception
```

`keytool` prompts for the store password; pass `-storepass changeit` (or the value of `MSB_JAVA_STORE_PASSWORD`) in a
non-interactive shell. Running `msb-trust java` as the `dev` user fails on that store's permissions. A JVM that is
already running — a Gradle daemon, for example — keeps the trust store it loaded, so restart it after the import.

For a JDK outside the system layout (SDKMAN, Nix), set `JAVA_HOME` to it — the helper uses `$JAVA_HOME/bin/keytool`
when it is set, otherwise `keytool` from `PATH`. The helper never installs packages and reports a missing `keytool`
instead.

`msb-trust all` also configures Chrome/Chromium's NSS store when `certutil` is present. The helper's own messages tell
you to run `sh msb-trust.sh …`, because upstream ships the script as a file you save next to your code; the installed
command is `msb-trust`, so translate the name when you follow them.

## Import it automatically on boot

The trust store lives in the image, not in the home volume, so the import is lost when the VM is recreated (a rebuild, a
config change, or a reset). Import it again on every boot with a
[startup hook]({% link configuration/home-provisioning.md %}):

```yaml
# .agents-sandbox/config.yaml
home:
  .local/bin/trust-java-ca.sh:
    source: trust-java-ca.sh
    hook: startup
    root: true          # the JDK trust store is root-owned
```

```sh
# .agents-sandbox/trust-java-ca.sh
#!/bin/sh
# No secrets configured means no interception CA to import.
[ -r /.msb/tls/ca.pem ] || exit 0
exec msb-trust java
```

The hook runs before the agent session starts, so the first `./gradlew` in the session already trusts the CA. It fires
when the VM **starts**, not when it is attached to, so adding it to an already-running VM needs one `agents-sandbox stop`
(or any change that recreates the VM) before it takes effect. A failing hook is logged and does not stop the VM, but the
`[ -r ... ]` guard keeps a project without secrets quiet.

Keep `JAVA_HOME` in the project Dockerfile (`ENV JAVA_HOME=/path/to/jdk`) if the JDK is not the system one, so the
root-run hook imports into the right trust store.
