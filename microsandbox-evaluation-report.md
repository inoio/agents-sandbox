# microsandbox v0.6.6 Evaluations-Report

Autogeneriert durch `eval-microsandbox.sh`.

```bash
$ msb run alpine -- wget -q -O /dev/null https://example.com && echo example-OK || echo example-FAILED
example-OK
```

```bash
$ msb run debian -- sh -c 'apt-get update -qq 2>&1 | head -3'
```

```bash
$ msb run --no-net alpine -- wget -q -O /dev/null https://example.com 2>&1 && echo example-LEAK || echo example-BLOCKED
wget: bad address 'example.com'
example-BLOCKED
```

```bash
$ msb run --no-net --net-rule 'allow@example.com:tcp:443' alpine -- wget -q -O /dev/null https://example.com && echo allowed-OK || echo allowed-BLOCKED
allowed-OK
```

```bash
$ msb run --no-net --net-rule 'allow@example.com:tcp:443' alpine -- wget -q -O /dev/null https://debian.debian.org 2>&1 && echo non-allowed-LEAK || echo non-allowed-BLOCKED
wget: bad address 'debian.debian.org'
non-allowed-BLOCKED
```

```bash
$ msb run --secret 'TEST_SECRET@httpbin.org' alpine -- sh -c 'apk add --no-cache curl >/dev/null 2>&1 && curl -s https://httpbin.org/headers | grep -i test-secret && echo secret-injected || echo secret-missing'
secret-missing
```

```bash
$ msb run debian -- sh -c 'which docker || echo no-docker'
no-docker
```

```bash
$ msb run docker:dind -- sh -c 'mkdir -p /etc/docker && echo "{\"storage-driver\":\"vfs\"}" > /etc/docker/daemon.json && dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 & sleep 5; DOCKER_HOST=unix:///var/run/docker.sock docker run --rm hello-world 2>&1 | head'
Unable to find image 'hello-world:latest' locally
latest: Pulling from library/hello-world
4f55086f7dd0: Pulling fs layer
4f55086f7dd0: Verifying Checksum
4f55086f7dd0: Download complete
4f55086f7dd0: Pull complete
Digest: sha256:c3cbe1cc1aa588a64951ac6286e0df7b27fe2e6324b1001c619bb358770c0178
Status: Downloaded newer image for hello-world:latest

Hello from Docker!
```

```bash
$ msb run docker:dind -- sh -c 'mkdir -p /etc/docker && echo "{\"storage-driver\":\"vfs\"}" > /etc/docker/daemon.json && dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 & sleep 5; DOCKER_HOST=unix:///var/run/docker.sock docker info 2>&1 | grep -E "Storage|Server Version|Cgroup"'
 Server Version: 29.6.2
 Storage Driver: vfs
 Cgroup Driver: cgroupfs
 Cgroup Version: 2
```

```bash
$ ls -la ~/.config/opencode 2>&1 || echo no-opencode-config
total 128
drwxrwxr-x 1 ole users   344 Jul  9 16:42 .
drwxr-xr-x 1 ole ole    3596 Jul 18 13:32 ..
-rw-rw-r-- 1 ole users    45 Jan 30 13:47 .gitignore
drwxrwxr-x 1 ole ole     114 Mai  3 05:53 .opencode
-rw-r--r-- 1 ole ole     848 Apr  1 10:54 bun.lock
-rw-rw-r-- 1 ole ole     201 Mai  3 04:56 dcp.jsonc
drwxrwxr-x 1 ole ole      14 Jun 19 17:28 docs
drwxrwxr-x 1 ole ole      36 Mai  3 04:47 memory
drwxr-xr-x 1 ole users   200 Apr  7 05:33 node_modules
-rw-rw-r-- 1 ole ole    3983 Mai  3 05:57 oh-my-opencode-slim.jsonc
-rw-rw-r-- 1 ole ole   10137 Jul 10 19:11 opencode.jsonc
-rw-rw-r-- 1 ole ole    6512 Jul  8 12:15 opencode.jsonc.bak
-rw-rw-r-- 1 ole ole    3677 Apr  7 05:33 package-lock.json
-rw-rw-r-- 1 ole users    63 Mai  1 14:45 package.json
drwxrwxr-x 1 ole ole      60 Jun  5 15:11 skills
-rw-rw-r-- 1 ole ole     226 Mai 14 03:00 tui.jsonc
-rw-rw-r-- 1 ole ole     328 Mai  3 05:13 tui.jsonc.bak
```

```bash
$ msb run -v ~/.config/opencode:/home/opencode/.config/opencode:ro debian -- ls -la /home/opencode/.config/opencode 2>&1 || echo mount-failed
total 52
-rw-rw-r-- 1 root root    45 Jan 30 12:47 .gitignore
drwxrwxr-x 1 root root   114 May  3 03:53 .opencode
-rw-r--r-- 1 root root   848 Apr  1 08:54 bun.lock
-rw-rw-r-- 1 root root   201 May  3 02:56 dcp.jsonc
drwxrwxr-x 1 root root    14 Jun 19 15:28 docs
drwxrwxr-x 1 root root    36 May  3 02:47 memory
drwxr-xr-x 1 root root   200 Apr  7 03:33 node_modules
-rw-rw-r-- 1 root root  3983 May  3 03:57 oh-my-opencode-slim.jsonc
-rw-rw-r-- 1 root root 10137 Jul 10 17:11 opencode.jsonc
-rw-rw-r-- 1 root root  6512 Jul  8 10:15 opencode.jsonc.bak
-rw-rw-r-- 1 root root  3677 Apr  7 03:33 package-lock.json
-rw-rw-r-- 1 root root    63 May  1 12:45 package.json
drwxrwxr-x 1 root root    60 Jun  5 13:11 skills
-rw-rw-r-- 1 root root   226 May 14 01:00 tui.jsonc
-rw-rw-r-- 1 root root   328 May  3 03:13 tui.jsonc.bak
```

```bash
$ msb run -v '/home/ole/projects/inoio/inoio/saife:/workspace' -w /workspace debian -- sh -c 'ls -la && apt-get update -qq && apt-get install -y -qq git >/dev/null 2>&1 && git status 2>&1 | head'
total 52
drwxr-xr-x 1 root root    60 Jul 18 11:32 .agent-sandbox
-rw-rw-r-- 1 root root    47 Jul 18 16:34 .eval-microsandbox-state
drwxrwxr-x 1 root root   360 Jul 18 16:38 .eval-state
drwxrwxr-x 1 root root    98 Jul 18 16:34 .git
drwxrwxr-x 1 root root    60 Jul 18 13:01 docs
-rwxrwxr-x 1 root root  5942 Jul 18 16:37 eval-microsandbox.sh
-rw-rw-r-- 1 root root 38677 Jul 18 16:34 microsandbox-evaluation-report.md
On branch main

No commits yet

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	.agent-sandbox/
	.eval-microsandbox-state
	.eval-state/
	docs/
```

