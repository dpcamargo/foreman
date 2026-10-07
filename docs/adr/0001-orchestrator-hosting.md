# ADR 0001: Run foreman as a launchd host process under a dedicated macOS user

Date: 2026-10-07
Status: Accepted
Decided by: operator, after a written comparison of options (no foreman exists yet, so the
Appendix B decision protocol could not run; this ADR records the reasoning instead)

## Context

ARCHITECTURE.md contradicts itself on where the orchestrator runs: §3.1, §13 and §14 say a
`launchd` host process, while §18 and §8.4 say a persistent Docker container driving sibling
containers through the Docker socket.

Facts checked on the host on 2026-10-07:

- Containers run on Colima (vz, virtiofs), raised to 4 CPU / 6 GiB.
- Colima's default profile mounts `/Users/dpcamargo` read-write into its VM. Whoever holds that
  profile's Docker socket can read and write the operator's home through a container.
- Until Feature 008, coding agents (Codex, `agy`, Claude via Hermes) run on the host with their
  own subscription logins and vendor OS sandboxes (Codex uses Seatbelt, which does not exist in
  a Linux container). Only the verifier/grader is containerized.

## Decision

Option A: foreman is a `launchd` host process running as a dedicated `foreman` macOS user. It
uses its own Colima profile, started by that user, whose VM mounts only foreman's workspace
root. It never uses the operator's default Colima socket.

## Consequences

- Agents keep their vendor sandboxes and host-side logins; no OAuth tokens are copied into
  containers (ARCHITECTURE.md §8.2).
- foreman, the agents, and the in-process local models use host RAM, not the 6 GiB VM.
- Manual setup (sudo): create the user, log codex / Hermes / agy in under it, start its Colima
  profile. Two Colima VMs share 16 GB, so the default profile may need to be stopped during runs.
- Until the user exists, foreman may run as the operator only for runs the operator starts and
  watches (Feature 001 baselines), never unattended.

## Rejected

Option B (container + Docker socket): the socket is root on the VM, and the VM mounts the
operator's home read-write, so the most privileged process would gain write access to the home
directory. Agents would either need their logins copied into the container (and lose Seatbelt)
or stay on the host, which requires a host process anyway.

## Revisit when

Feature 008 moves agents into per-node containers behind a credential-injecting egress proxy,
or foreman moves to an always-on Linux host.
