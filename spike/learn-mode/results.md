# Learn-mode observation spike — results

Fill in as experiments run. This file, not the scripts, is what feeds the spec
update (`.operator-shared/specs/onboarding/learn-mode.md`, "Observation
approaches"). Run date, host, and the resolving `msb`/SDK versions at the top.

- Run date:
- Host:
- `msb --version`:
- SDK (`go.mod` `microsandbox/sdk/go`):
- mitmproxy version:

## Results

| Experiment | Result | Decision taken |
|---|---|---|
| S1 | | |
| S2 | | |
| S3 | | |
| S4 | | |
| S5 | | |
| S6 | | |
| S7 | | |
| D (D1 destinations / D2 attribution) | | |

## Sub-step D — runtime interception as an observation channel

- D1 destinations (does an intercepted request name its host?): yes / no
- D2 attribution (does it name the credential's header field and destination?): yes / no
- Evidence (the `sni=`/`dst=` line, the violation fields, or their absence):
- If D1 yes and D2 no, D is a certificate-free B; does that change the chosen approach?

## Deviations and gaps

- Experiments not run, and why:
- Places the plan was wrong (e.g. `make test-msb-reuse` does not exist):
- Things the scripts could not automate (docker build pull, pinning client, real agent turn):

## Outcome

- Observation approach chosen (A / B / C / D, and why):
- Does UC2 detection need a permanent proxy, or is the runtime log enough (S5)?
- Is the learned destination set authoritative or advisory (S4)?
- Cost of the chosen channel (S7):
