---
title: Expose a Dev Server
description: "Publish a dev server or a database port from the sandbox VM to the host loopback, so the browser and host-side tools can reach it."
layout: default
parent: Recipes
nav_order: 25
---

# Expose a Dev Server

A service inside the sandbox — a dev server, a database — is unreachable from the host until you publish its port.
The `ports:` config key (or the `--port`/`-p` flag) publishes guest ports on the host loopback (`127.0.0.1`, TCP
only), opt-in and never on the LAN. See [Networking]({% link configuration/networking.md %}#published-ports) for
the full reference.

## A dev server with hot reload

Publish the server's guest port, here Vite's default 5173 under the same host port:

```yaml
ports:
  - "5173"
```

or once, on the command line:

```sh
agents-sandbox run -p 5173
```

Then start the dev server inside the sandbox with `--host 0.0.0.0` — binding only the guest loopback keeps the
published port from reaching it:

```sh
npm run dev -- --host 0.0.0.0
```

Open `http://127.0.0.1:5173` on the host; hot reload works as if the server ran locally.

## A database inspected from the host

Run the database in the sandbox's Docker engine and publish its port under a different host port (PostgreSQL's
5432 as host 15432 here, keeping 5432 free on the host):

```yaml
ports:
  - "15432:5432"
```

Start Postgres in the sandbox (the runner image ships the Docker engine when the project enables it; see
[Docker-in-Docker]({% link runner-image.md %}#docker-in-docker)):

```sh
docker run -d --name db -p 5432:5432 -e POSTGRES_PASSWORD=dev postgres:17
```

Point a host-side GUI client (or `psql`) at `127.0.0.1:15432`. The database is reachable from the host machine and
from nowhere else.

## Changing published ports

Published ports are baked in at VM creation: microsandbox cannot add them to a running VM, so a change to the
`ports:` list recreates the VM on the next run. With other clients attached you can defer the recreate — the
launcher then warns and applies the change on the next run that recreates. A run without `ports:` against a VM
that has them drops them at the recreate.
