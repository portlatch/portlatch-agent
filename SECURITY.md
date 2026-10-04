# Security policy

The agent runs on your machine and opens a tunnel towards your local network.
If you find a way to abuse it, we want to hear from you before anyone else does.

## Reporting a vulnerability

Write to **security@portlatch.eu**. Please do not open a public issue.

Include what you can of:

- the agent version (`version=` on the `starting` log line) and how it runs:
  Docker image, Linux binary or Windows executable;
- what an attacker needs beforehand — network position, access to the machine,
  a Portlatch account — and what they gain;
- the steps to reproduce it, or a proof of concept.

We will confirm we received your report, keep you informed while we work on a
fix, and credit you in the release notes unless you prefer otherwise. Please give
us a reasonable time to ship the fix before disclosing it.

The same address takes reports about the Portlatch website and control plane,
whose code is not public.

## Supported versions

Fixes land in the latest release. Older versions keep working against the
control plane, but are not patched: update the agent to get a fix.

## Known and accepted

These are documented behaviours, not vulnerabilities:

- **The data directory holds the WireGuard private key and the API token in
  clear.** Whoever reads it can stand in for the agent until it is deleted from
  the dashboard, which revokes both at once. On Linux the
  files are written `0600`; on Windows they inherit the permissions of their
  folder.
- **Outbound only.** The agent opens no port on the host. It listens inside its
  userspace WireGuard stack, on the ports the control plane hands out, and
  connects to the targets you configured.
