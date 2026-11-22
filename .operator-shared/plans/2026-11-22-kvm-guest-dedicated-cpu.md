# KVM-guest dedicated CPU — contingency plan

> Triggered only if the in-guest `msb` suites are still unstable after the `MSB_GUEST_TIMEOUT_FACTOR=3`
> change. If they are stable, this plan stays unexecuted.

**Goal:** make the in-guest `make test-msb-in-vm` runs reliably green on a host whose CPU is shared,
by either removing the in-guest VMs' contention sensitivity or guaranteeing the guest a CPU floor.

**Specs:** `specs/testing/harness-smoke-tests.md` (ceilings absorb CPU starvation from any source;
`Session.Write` has no deadline, #837), `guides/kvm-guest.md` (the guest and its knobs).

## Problem and evidence

The guest's 12 vCPUs are a *share* of the host's cores (observed host: AMD Ryzen 7 PRO 5850U, 8c/16t), so
host-side load steals CPU from the guest and is invisible inside it. Harness readiness is wall-clock-bounded
(`settledReadyTimeout`/`heavyweightReadyTimeout`/`TuiReadyTimeout` × `MSB_GUEST_TIMEOUT_FACTOR`), so CPU
starvation from **any** source — a concurrent guest launcher VM, or the host itself — makes a normal-buffer
harness (kimi-code tightest) miss its ceiling and fail the subtest. Observed: multiple readiness failures
while an *uncapped* guest VM held all 12 vCPUs; solo runs passed. `steal` is the in-guest observable
(`/proc/stat` field 8) but was not captured during the failing runs.

## Decision inputs (measure before changing the host)

1. Run the suite N times with a live `steal`/`iowait`/`load` sampler and record pass/fail per run.
2. Correlate failures with `steal` and with in-guest `load`.
3. Record per-run VM-start time (the log's `Starting project VM …(Ns)` line).
   - Failures with high `steal` → host contention. Failures with low `steal` and high in-guest load → in-guest.
   - No failures at factor 3 → do nothing; the extra ceiling headroom was sufficient.

## Options

| Option | What it does | Cost / risk |
|---|---|---|
| **A. Operational** — run the suite when the host is idle; cap any concurrent guest launcher with `--cpus` | removes the common contention source; no host change | needs discipline; cannot fix a chronically busy host |
| **B. Raise the ceilings** (`MSB_GUEST_TIMEOUT_FACTOR` higher) | absorbs more starvation; free on healthy runs (bound, not sleep) | stretches failure latency; does not increase throughput or reduce the actual starvation |
| **C. libvirt `cputune` pinning** (`vcpupin` + `shares`, optionally `period`/`quota`) | pins the guest's vCPUs to host pCPUs and gives the domain a scheduler weight/floor | needs the host's libvirt XML + a restart; interacts with NUMA |
| **D. Dedicate host cores** (`isolcpus`/`nohz_full` or a host cpuset slice for the qemu process) | real CPU floor: the guest's cores run nothing else | host-side kernel param + reboot, or a systemd slice; over-dedication starves the host |
| **E. Shrink the guest** (`VM_VCPUS` down) | fewer vCPUs schedule better on a contended host | slows every in-guest VM start; loses the 12-vCPU parallelism |

## Recommendation

- Prefer **A + B** when the host is normally quiet: keep `MSB_GUEST_VM_CPUS=2`, cap every concurrent guest
  launcher, and let the factor (raised to 3) carry the margin. `MSB_GUEST_PARALLEL` stays 2 (raising it was
  measured to hang, #837, and the guest is VM-start-bound).
- Move to **C**, and **D** if the host is chronically shared, only when the measurement shows failures
  tracking `steal` on a host that cannot be kept idle. Do **C** before **D**: pinning is reversible and
  needs no reboot; `isolcpus` is not.

## Steps for C (libvirt pinning)

1. On the host, pick a host pCPU set for the guest (leave the rest for the host). Read the topology with
   `virsh capabilities` / `lscpu -e`.
2. Edit the domain XML (`virsh edit <domain>`): add `<cputune>` with a `<vcpupin vcpu='i' cpuset='…'/>` per
   guest vCPU, a `<shares>` (or `<period>`/`<quota>`) floor, and optionally `<emulatorpin>`.
3. `virsh shutdown && virsh start` the domain (or `virsh define` + restart); confirm with
   `virsh vcpupin <domain>` and `virsh schedinfo <domain>`.
4. Line the change up with `scripts/kvm-guest.sh` (the domain XML it renders) so `make kvm-up` reproduces it.
5. Re-measure: in-guest `steal`, VM-start time, and N consecutive `make test-msb-in-vm` runs.

## Acceptance

- N (≥5) consecutive `make test-msb-in-vm` runs green, with `steal` below a stated ceiling and no harness
  missing its readiness ceiling across the runs.
- The host remains usable for its other work (no sustained starvation of non-guest tasks).

## Risks

- Pinning that overlaps the host's own housekeeping starves *it*; leave cores unreserved for the host.
- `isolcpus` needs a reboot and can surprise other host workloads; prefer a cgroup/cpuset slice (systemd
  `CPUWeight`/`AllowedCPUs`) where possible.
- A macOS host has no libvirt `cputune` equivalent; this plan is Linux-host only.
- Pinning changes VM-start timing, so re-baseline before comparing wall times.

## Outcome

Record the decision and the measured numbers in `guides/kvm-guest.md` (host prerequisites / knobs) once
made; retire this plan if the factor-3 sweep is green and no host change is needed.
