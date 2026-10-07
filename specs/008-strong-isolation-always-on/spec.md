# Feature Specification: Strong Isolation and Always-On Host

**Feature Branch**: `008-strong-isolation-always-on`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "Move execution isolation from a dedicated macOS user to
per-node containers or microVMs with a credential-injecting egress proxy and phased network
policy (deps-only, agent-only, no-network-for-tests), as a phase enabling an always-on host."

**Source**: `ARCHITECTURE.md` §3.8, §8.3/8.4, §13 Phase 7.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Contain a compromised agent to one disposable sandbox, with no real
credentials inside it (Priority: P1)

As the operator, every agent sandbox (implementer, scout, test-author) runs inside a container
or microVM rather than merely under a dedicated OS user, and it never holds a real API key,
GitHub token, or cloud credential directly — only a placeholder that an egress proxy swaps for
the real credential on the way out, so a compromised or malicious-repo-triggered agent cannot
exfiltrate anything usable even if it reads its own "credentials."

**Why this priority**: This closes the residual risk explicitly flagged in
ARCHITECTURE.md §8.2/§11 scenario 9 — the Phase 1 "dedicated macOS user" isolation (Feature
002) is real but weaker than the architecture's target state; this feature is what makes
always-on, fully unattended operation defensible.

**Independent Test**: Can be tested by inspecting a running sandbox's environment/filesystem
for credential material and confirming only placeholder tokens are present, then confirming an
outbound request using the placeholder is correctly completed by the egress proxy injecting
the real credential — never by the sandbox holding it directly.

**Acceptance Scenarios**:

1. **Given** a sandbox is prepared for an implement node, **When** its environment and
   filesystem are inspected, **Then** no real API key, GitHub token, SSH key, or cloud
   credential is present — only placeholder tokens.
2. **Given** a sandboxed process makes an outbound call using its placeholder token, **When**
   the call passes through the egress proxy, **Then** the proxy injects the real credential
   before forwarding, and the sandbox never observes the real value in any response or log it
   can read.
3. **Given** a sandbox is compromised (simulated by a deliberately malicious test payload
   attempting to read environment variables or make an unexpected outbound call), **When** the
   isolation boundary is exercised, **Then** the damage is provably contained to that one
   disposable sandbox, its branch, and that run's budget — no host credential, other repo, or
   other task's workspace is reachable.

---

### User Story 2 - Enforce network access only where it's actually needed, in phases
(Priority: P1)

As the operator, each sandboxed run's network access is restricted to exactly what that phase
of work needs: dependency fetch gets registry access only (no agent, no secrets), the agent
phase gets model-API access only, and the test-execution phase gets no network at all —
enforced structurally, not by convention.

**Why this priority**: This directly extends constitution Principle VI (isolation by default)
from "the verifier has no network" (Feature 002) to the full three-phase network policy the
architecture specifies for every sandboxed stage, not just verification.

**Independent Test**: Can be tested by attempting, from inside each of the three phases
(deps/agent/test), a network call outside that phase's allowed scope, and confirming it is
blocked in all three cases.

**Acceptance Scenarios**:

1. **Given** the dependency-fetch phase of a sandbox's lifecycle, **When** a process attempts
   to reach anything other than a configured package registry, **Then** the connection is
   blocked.
2. **Given** the agent-execution phase, **When** a process attempts to reach anything other
   than a configured model-API endpoint (via the egress proxy), **Then** the connection is
   blocked.
3. **Given** the test-execution phase, **When** any process attempts any outbound network
   connection at all, **Then** it is blocked unconditionally — this phase has zero network
   access, matching Feature 002's existing verifier requirement, now generalized to every
   sandboxed stage that runs tests.

---

### User Story 3 - Choose an isolation backend that fits the current host, without
rewriting the engine (Priority: P2)

As the operator, the sandbox manager supports at least one of the evaluated phase-7 isolation
backends (Docker Sandboxes/microVM, Apple `container`, or hardened Docker containers in the
shared Colima VM) behind one `Sandbox` interface, so moving to an always-on host later (a Mac
mini or a Linux box) doesn't require re-architecting node dispatch.

**Why this priority**: Keeps the engine's contract with sandboxes (`Prepare`/`Export`/
`Destroy`, per ARCHITECTURE.md §15.1) stable across a hardware/backend change, matching
constitution Principle IX (minimal, justified infrastructure — don't rebuild the engine to
swap an isolation backend).

**Independent Test**: Can be tested by running the same task through two different configured
isolation backends (for example, hardened Docker containers vs. Apple `container`) and
confirming identical `RunResult`/`VerifyReport` shapes come back, with only the isolation
mechanism differing.

**Acceptance Scenarios**:

1. **Given** the `Sandbox` interface (`Prepare`, `Export`, `Destroy`) already defined in
   Feature 002, **When** a phase-7 backend is selected via configuration, **Then** the engine's
   node dispatch code requires no change beyond selecting that backend's implementation.
2. **Given** a per-node container or microVM backend is active, **When** a node's workspace is
   prepared, **Then** it gets its own filesystem and network namespace, isolated from sibling
   nodes' workspaces, not merely a different OS-user-owned directory.
3. **Given** the weakest evaluated option (hardened Docker containers sharing the Colima VM's
   kernel) is selected, **When** isolation is assessed, **Then** the system still enforces
   non-root, `--cap-drop ALL`, `no-new-privileges`, read-only rootfs plus tmpfs, and pids/
   memory/CPU limits — the "weakest of the three" option is still hardened, not merely
   "a container with defaults."

---

### Edge Cases

- What happens when the egress proxy itself is unreachable? Outbound calls requiring credential
  injection MUST fail closed (the call does not go through with a placeholder value reaching a
  real endpoint), not fail open.
- How does the system handle a sandbox attempting to escalate its own network phase (for
  example, an agent-phase process trying to behave like it's in deps-phase to reach a
  registry)? Phase transitions MUST be enforced by the sandbox manager/proxy based on which
  stage of the run is executing, not by a flag the sandboxed process can set itself.
- What happens when moving from the dedicated-macOS-user backend (Feature 002) to a phase-7
  backend for an in-flight task? This feature MUST NOT require in-flight tasks to be
  interrupted — new nodes can be dispatched under the new backend while already-running nodes
  under the old backend finish normally, since both implement the same `Sandbox` interface.
- What happens when a microVM/container backend's per-node overhead (startup time, resource
  reservation) exceeds the configured budget for a lane-F task? The sandbox manager MUST
  surface this as a resource-availability condition the scheduler can act on (queue, or select
  a lighter backend if multiple are configured), not silently degrade isolation to make the
  budget.
- What happens when the credential-injecting proxy needs to support a provider not yet
  configured? The proxy MUST reject the outbound call rather than forwarding it unauthenticated
  or with a stale credential.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST implement at least one phase-7 isolation backend (container or
  microVM per node) satisfying the existing `Sandbox` interface (`Prepare`, `Export`,
  `Destroy`) from Feature 002, requiring no change to engine dispatch code to adopt it.
- **FR-002**: No sandboxed workspace, under any phase-7 backend, MUST contain a real API key,
  GitHub token, SSH key, or cloud credential; sandboxes MUST hold placeholder tokens only.
- **FR-003**: The system MUST implement a credential-injecting egress proxy that substitutes
  real credentials for placeholder tokens on outbound calls, such that the real credential
  value is never observable from inside the sandbox.
- **FR-004**: The egress proxy MUST fail closed (reject the call) when it cannot reach its own
  credential source or does not recognize the target provider — never forward a call
  unauthenticated or with a stale/incorrect credential.
- **FR-005**: The system MUST enforce three distinct network phases per sandboxed run:
  dependency-fetch (registries only, no agent process, no secrets), agent-execution (model-API
  endpoints only, via the egress proxy), and test-execution (no network access at all) —
  structurally enforced, not advisory.
- **FR-006**: Phase transitions (which network policy currently applies) MUST be enforced by
  the sandbox manager or proxy based on the run's actual lifecycle stage, not by any value the
  sandboxed process itself can set.
- **FR-007**: Every phase-7 container/microVM, regardless of which concrete backend is active,
  MUST run non-root, with `--cap-drop ALL` (or the backend's equivalent), `no-new-privileges`,
  a read-only root filesystem plus tmpfs for scratch space, and configured pids/memory/CPU
  limits.
- **FR-008**: The system MUST allow in-flight tasks dispatched under the Feature 002
  dedicated-OS-user backend to continue running to completion while new nodes are dispatched
  under a newly-configured phase-7 backend, without requiring a coordinated cutover.
- **FR-009**: The sandbox manager MUST be able to report when a configured backend's resource
  requirements exceed what's available for a given lane/budget, as a distinct condition from a
  generic dispatch failure.

### Key Entities *(include if feature involves data)*

- **Isolation Profile**: a named backend configuration (Docker Sandboxes, Apple `container`,
  hardened shared-VM Docker) implementing the `Sandbox` interface, selectable per host/phase.
- **Egress Proxy Credential Mapping**: the (placeholder token → real credential, provider)
  mapping used to inject real credentials on outbound calls, never exposed to the sandbox.
- **Network Phase**: one of `deps`, `agent`, `test` — the currently-enforced outbound policy
  for a given sandboxed run at a given point in its lifecycle.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Inspecting any phase-7 sandbox's environment and filesystem in testing never
  finds a real credential value — 100% placeholder tokens, across every tested backend.
- **SC-002**: A simulated malicious payload attempting to read environment variables, escalate
  its network phase, or reach outside its declared network policy fails to do so in 100% of
  tested attempts, across every network phase.
- **SC-003**: The same task, run under two different configured phase-7 backends, produces
  identical `RunResult`/`VerifyReport` shapes, confirming the `Sandbox` interface abstraction
  holds across backends.
- **SC-004**: A simulated egress-proxy outage results in outbound calls failing closed (no
  call succeeds using a placeholder reaching a real endpoint) in 100% of tested cases.
- **SC-005**: Switching the configured backend does not interrupt a task already dispatched
  under the prior (Feature 002) backend — that task completes normally while new dispatch uses
  the new backend.

## Assumptions

- This feature treats Feature 002's dedicated-macOS-user isolation as the Phase 1 baseline it
  extends, not replaces outright on day one — both backends can coexist behind the `Sandbox`
  interface during a migration window, per FR-008.
- Among the three evaluated backends in ARCHITECTURE.md §8.3, this feature's `/speckit-plan`
  is expected to select ONE as the initial concrete implementation (most likely hardened Docker
  containers in the existing Colima VM, since it requires no new platform dependency),
  leaving Docker Sandboxes and Apple `container` as documented, interface-compatible
  alternatives rather than all three being built simultaneously.
- The credential-injecting egress proxy reuses the pattern Hermes already ships
  (`iron-proxy`, per ARCHITECTURE.md §3.14/§8.3) as a reference design; this feature does not
  require reusing Hermes's exact implementation, only an equivalent credential-injection
  contract.
- "Always-on host" migration itself (moving the daemon from the operator's laptop to a Mac
  mini or Linux box) is treated as an operational deployment step enabled by this feature's
  isolation work, not a functional requirement of this feature — the daemon's own
  portability (one Go binary plus one SQLite file) was already established by Feature 002/003.
- This feature does not change Feature 002's verifier network policy (already `--network=none`
  for tests); it generalizes the same no-network guarantee to every sandboxed stage that runs
  tests, and adds the two new phases (deps, agent) that didn't previously have an explicit,
  separately-enforced policy.
