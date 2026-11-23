# Service port derivation: design and delivery notes

This document records the rules kompose now applies when it decides the shape of
a generated Service from a Compose `ports`/`expose` declaration, and what changes
for existing inputs. It complements [user-guide.md](user-guide.md), which documents
the user-facing labels and flags.

Everything here is validated offline, at the level of `LoadFile` + `Transform`
and the generated objects; no cluster is involved.

## Where the decision is made

Two places used to decide the Service type:

- the `kompose.service.type` label (`handleServiceType` in
  `pkg/loader/compose/utils.go`), accepting `clusterip`, `nodeport`,
  `loadbalancer` and `headless`;
- `deploy.endpoint_mode: vip`, mapped straight to `NodePort` in
  `pkg/loader/compose/compose.go`.

Without either, the Service type stayed unset and Kubernetes defaulted it to
`ClusterIP`; `ports` only populated the port list.

The new pass lives in `pkg/transformer/kubernetes/ports.go`
(`ValidateServicePorts`, called from both the Kubernetes and OpenShift
`Transform`) and runs before any object is built, so one conversion reports every
port problem instead of stopping at the first. The existing helpers
(`Ports.ID`, `ConfigPorts`, `ConfigServicePorts`, `ConfigLBServicePorts`,
`InitSvc`, `CreateService`, `CreateLBService`, `CreateHeadlessService`) are reused;
the new pass only validates and resolves the type.

## Rules

### Explicit labels keep priority

`kompose.service.type` is recorded in `ServiceTypeExplicit`. A derived type is
only computed when that flag is clear, so an explicit label (including the empty
value that normalises to `ClusterIP`) is never overridden by derivation. The one
override kept is the StatefulSet controller, described below.

### Derivation from ports (opt-in)

`--derive-service-type-from-ports` is **off by default**. When on, for a service
with no explicit type:

- at least one *published* ingress-mode port → `NodePort`;
- only `expose` entries, only container ports, or only `mode: host` ports →
  `ClusterIP`.

The switch exists because deriving `NodePort` from a published port would change
the output of existing users (a `8080:80` service currently becomes a
ClusterIP). Existing conversions without the switch produce exactly the same
objects.

### `deploy.endpoint_mode`

- `vip` → `NodePort` (existing mapping, unchanged, recorded here because the
  mapping is debatable: a Swarm virtual IP is closer to a ClusterIP than to a node
  port);
- `dnsrr` → `Headless` (new; Swarm's `dnsrr` skips the virtual IP and returns task
  addresses directly, which is what a headless Service does);
- any other non-empty value is an error.

### StatefulSet controller

A StatefulSet still forces a Headless Service, exactly as before. If a type was
derived and conflicts, it is replaced with an informational note; if the user
wrote the type with a label, the override is a warning that names the service and
the original value. The derived result never silently replaces a user's choice.

### `ports` ranges

compose-go keeps `8000-8010:80` (a host range aimed at one container port) as the
string `"8000-8010"` in `Published`. `cast.ToInt32` turned that into `0`, which was
read as "no host port" with no diagnostic. Ranges are now an **error**:

> published port range "8000-8010" on container port 80 is not supported; declare
> one host-to-container port mapping per line or use matching ranges such as
> 8000-8010:9000-9010

Matching ranges (`8000-8010:9000-9010`) are already expanded pairwise by
compose-go and keep working. `expose` ranges (`3000-3002`) are expanded by kompose.
A container port of `0` or above `65535`, or an unparsable `Published`, is also an
error.

Rationale for an error rather than a one-release warning: silently emitting a
Service whose `port` is not the published one is a correctness bug that produces
a *valid-looking* but wrong object. A warning would keep producing that object.
The message tells the user exactly how to rewrite the input.

### `HostIP`

`HostIP` is only written to the container's `hostPort` when
`kompose.controller.port.expose` is set; the Service ignores it. A loopback
binding (`127.0.0.1:8080:80`, `::1:...`) is therefore reported as a
`port-exposure` warning: a Service exposes the port cluster-wide, and NodePort or
LoadBalancer widen it further. The warning is emitted once per distinct host IP
per service, not once per expanded port of a range.

The binding is **not** downgraded to ClusterIP. `HostIP` has no Kubernetes
equivalent on a Service; the only way to honour "loopback only" would be to keep
the service unexposed, which would discard a port the user asked to publish. The
warning makes the widened exposure visible without guessing intent.

### `mode: host` and `mode: ingress`

- `mode: host` is closest to a container `hostPort`: the port is published on the
  node. It **always** becomes a container `hostPort`, independent of
  `kompose.controller.port.expose`, and is **not** added to the Service. A service
  whose ports are all `mode: host` produces no Service (a warning already said so
  when `ports` was empty; now it says so for host-only ports too).
- `mode: ingress` (the compose default) is closest to a Service port.
- An unknown `mode` is an error.

A port is either a hostPort or a Service port, never both, so the two cannot be
generated for the same declaration.

### Multi-port services and nodePort

`kompose.service.nodeport.port` stays single-valued: with more than one Service
port it is still an error ("cannot set kompose.service.nodeport.port when service
has multiple ports"), now reported through the aggregated validator.

`kompose.service.nodeport.ports` is the per-port alternative, keyed by container
port: `80:30080,443:30443`. Using both labels on one service is an error.

Validation (all collected, none fatal one at a time):

- every nodePort must lie in the configured range (default `30000-32767`, settable
  with `--nodeport-range-min` / `--nodeport-range-max`); out-of-range is an error,
  never emitted as-is;
- a nodePort assigned to a container port the service does not declare is an
  error, as is assigning one to a `mode: host` port;
- the same `(nodePort, protocol)` twice is an error. The same nodePort for TCP and
  UDP is allowed for `NodePort`, matching the API server's own rule.

### `LoadBalancer` and mixed protocols

`ConfigLBServicePorts` splits a LoadBalancer into a TCP and a UDP Service.
`ConfigServicePorts` used to call `log.Fatalf` when a LoadBalancer used one port
number for both protocols; that is now an accumulated **error** ("service port 53
is used for both TCP and UDP; LoadBalancer Services cannot publish the same port
number across protocols on most cloud providers"). Because it is an error, one
conversion can report it alongside every other port problem and exit with a
non-zero status instead of killing the process mid-walk.

The split Services (`<name>-tcp`, `<name>-udp`) keep the same selector
(`io.kompose.service: <service>`) and the same `externalTrafficPolicy` on both
halves. Port names are computed per generated Service, so the TCP and UDP halves
of a shared number each keep the bare number instead of suffixing each other.

### Headless ports

`CreateHeadlessService` keeps the `headless`/`55555` placeholder **only when the
service has no real Service ports**. When it has real ports, those are emitted
verbatim. Rationale: a headless Service's ports drive its SRV records and
Endpoints, so real ports are meaningful and the placeholder would be noise; the
placeholder exists only to satisfy the "Services need at least one port" rule for
port-less services.

Headless plus `externalTrafficPolicy` still prints "ignored", now with the service
name and the label's original value.

### Port names

A Service port is named after the exposed port number (the published port, else
the container port). When two ports in the *same generated Service* expose the
same number, the first keeps the bare number and later ones get a
`-<protocol>` suffix (e.g. `6379`, `6379-udp`). Names are checked with
`IsDNS1123Label` — the rule the API server applies to Service port names
(`ValidateDNS1123Label`). The 15-character limit belongs to `IANA_SVC_NAME`,
which is used for *container* port names, and is not applied to Service ports.

Two container ports mapping to one Service port (e.g. `80` and `90` both published
as `8080`) are a `port-conflict` error at conversion time, not a `kubectl apply`
failure.

### Output order

Ports are emitted sorted by container port, then protocol, then host port, host
IP and mode (`SortedPorts`), not in Compose declaration order. This makes repeated
conversions byte-identical. Where the fixture outputs changed only because of
this reordering, they were regenerated.

### Diagnostics

Diagnostics carry a category prefix; the categories cover port problems only and
are not a global taxonomy:

- `port-range` — invalid or out-of-range port numbers (ranges, nodePort bounds),
- `port-exposure` — widened exposure such as loopback bindings,
- `port-conflict` — duplicate names/ports/nodePorts, mixed protocols,
- `port-mode` — `mode: host` handling,
- `port-derivation` — how a Service type was derived or overridden.

Warnings are logged and do not change the exit status; errors are collected and
returned together, so a conversion with several problems lists all of them.

## Impact on existing output

Compares `LoadFile` + `Transform` before and after. "Unchanged" means no label, no
new switch, identical objects.

| Compose fragment | Before | After (no switch) | New warning? |
|---|---|---|---|
| `ports: ["8080:80"]`, no label | ClusterIP, port 8080→80 | identical | no |
| `expose: ["80"]`, no ports | ClusterIP, port 80→80 | identical | no |
| container port only (`ports: ["80"]`, no published) | ClusterIP, port 80 | identical | no |
| `ports: ["8080:80"]` + `--derive-service-type-from-ports` | n/a | NodePort | no (info note `port-derivation`) |
| `expose: ["3000"]` + switch | n/a | ClusterIP (unchanged) | no |
| `ports: ["127.0.0.1:8080:80"]` | ClusterIP | identical | `port-exposure` warning |
| `ports: ["8000-8010:80"]` | port 0 → container port, silently | **error** | — (becomes a failure) |
| `ports: ["80:8080", "90:8080"]` | duplicate name/port emitted | **error** (`port-conflict`) | — (becomes a failure) |
| `mode: host` port | hostPort only with the label, plus a Service | always hostPort, **no Service** | `port-mode` info, plus "no Service" warning |
| `endpoint_mode: vip` | NodePort | identical | no |
| `endpoint_mode: dnsrr` | ClusterIP | **Headless** | no |
| StatefulSet controller + port-derived type | forced Headless silently | Headless, note/warning | `port-derivation` |
| Headless with real ports | real ports emitted by `CreateService`; placeholder only when port-less | identical for this case; placeholder now only for port-less services | no |
| `kompose.service.type: headless` + ETP label, no ports | "ignored" warning | same, warning now names the service and value | yes (message only) |
| NodePort label with nodePort `33111` | emitted as-is | **error** unless range widened | — (becomes a failure) |
| `kompose.service.nodeport.ports: "80:30080,443:30443"` | unknown label, ignored | per-port nodePorts | no |
| LoadBalancer with TCP+UDP on port 53 | `log.Fatalf` | **error**, aggregated | — (becomes a clean failure) |

Files whose fixtures changed and were regenerated: `v2/*`, `env/*`,
`configmap-file-configs/*`, `host-port-protocol/*` (ordering and host-mode), and
`service-label/*` (nodePort moved into range). Everything else is byte-identical.

## OpenShift

The OpenShift transformer calls the same port and Service helpers, so it inherits
every rule above. Its `Transform` runs the same validation pass, and it gained the
explicit `LoadBalancer`/`Headless` branches so a headless service with ports no
longer loses its ports. The changes are visible in `output-os*` fixtures where the
Kubernetes side changed (ordering, host-mode, nodePort range).

## Verification

- `go test ./...` covers the loader (`loadPorts` ranges, modes, expose ranges,
  nodePort label parsing) and the transformer (`ValidateServicePorts` derivation,
  accumulation, nodePort map and range, LoadBalancer split, headless ports, naming,
  deterministic order). The accumulation test asserts that two problems in one
  service are both reported, and a `Transform` test asserts that two services'
  problems appear in one error.
- The fixture diffs above were regenerated from the new binary and reviewed; only
  port content changed.
