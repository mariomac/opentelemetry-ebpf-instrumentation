# Route53 name resolution

The opt-in `route53` name resolver maps endpoint IPs to fully qualified DNS names
from selected AWS Route53 hosted zones on detected EC2 hosts.
Configure Route53 in Config v2:

```yaml
file_format: "1.0"
extensions:
  obi:
    version: "2.0"
    enrich:
      enrichers:
        cloud:
          refresh_interval: 30s
          route53:
            refresh_interval: 5m
            hosted_zone_ids: [Z0123456789EXAMPLE]
      service_name:
        sources: [k8s, ecs, route53]
```

Use the AWS SDK's standard credentials configuration. Grant
`route53:ListResourceRecordSets` on the configured hosted zones; listing all zones
is not required. Zone IDs can include the `/hostedzone/` prefix. Select zones
appropriate to the monitored network, since different private networks can reuse
IP addresses.

Literal A and AAAA records contribute IP mappings. Names are lowercase, retain
all domain labels, and omit the trailing dot. Aliases, CNAMEs, and wildcard records
are excluded. If multiple names map to the same IP, remote name resolution uses
the lexicographically smallest name, independently of API response order.
Service graph endpoint mappings retain all direct names for that IP.

Kubernetes and ECS names take precedence over Route53 names. Route53 takes
precedence over captured DNS responses and reverse DNS. Route53 decorates endpoint
names; ECS container identity remains responsible for local process naming.

Route53 polls independently of ECS, with a default interval of five minutes.
Initial discovery is delayed randomly between zero and one interval to spread
startup traffic. Later polls wait between 80% and 120% of the interval after the
previous fetch completes. Names remain unresolved until initial discovery succeeds.

Each source retains its last successful snapshot. Successful refreshes remove
deleted records; a failed Route53 fetch preserves its mappings and does not block
ECS refreshes. Repeated throttling doubles the Route53 polling interval up to
eight times the configured interval, with the same jitter. A successful fetch
restores the configured interval. SDK retries still apply within each fetch.

A longer interval reduces average API traffic; jitter spreads bursts. Each OBI
instance still lists every configured zone, including all pages. Size the interval
for the number of instances and zones sharing the account, leaving capacity for
other API clients. There is no coordination between instances.

Hosted zones and the refresh interval can reference deployment-specific variables
through Config v2 substitution, for example:

```yaml
route53:
  refresh_interval: ${ROUTE53_REFRESH_INTERVAL:-5m}
  hosted_zone_ids: ["${ROUTE53_HOSTED_ZONE_ID}"]
```

For legacy Config v1, enable the source and configure the same settings under
`cloud_metadata.route53`:

```yaml
name_resolver:
  sources: [k8s, ecs, route53]
cloud_metadata:
  route53:
    refresh_interval: 5m
    hosted_zone_ids: [Z0123456789EXAMPLE]
```

Config v1 also accepts these environment variables, which override YAML values:

- `OTEL_EBPF_NAME_RESOLVER_SOURCES`: comma-separated resolver sources.
- `OTEL_EBPF_NAME_RESOLVER_ROUTE53_HOSTED_ZONE_IDS`: comma-separated hosted zone IDs.
- `OTEL_EBPF_NAME_RESOLVER_ROUTE53_REFRESH_INTERVAL`: polling interval (default `5m`).

The hosted zone IDs are required when the `route53` source is enabled.
`obi config migrate` preserves Route53 settings present in the Config v1 YAML;
legacy environment overrides must be materialized or explicitly rewired for Config v2.

The Route53 client region, which selects the AWS partition, is
`extensions.obi.enrich.enrichers.cloud.region` (or `cloud_metadata.region` in
Config v1) if set, otherwise the detected
cloud region. Without either, the AWS SDK's region configuration applies,
falling back to `us-east-1`.
For local tests, its endpoint override is `AWS_ENDPOINT_URL_ROUTE_53`.

## Routed service graphs

When `application_service_graph` metrics are enabled, OBI emits a
`traces_service_graph_endpoint` gauge with value `1` for each known Route53 name
of the local endpoint. It is independent of whether the process sends or receives
requests. Its attributes are `service.name`, `service.namespace`, `route`, and
`source`; Prometheus exposes the dotted names with underscores.

The local client/server identity in request metrics is always the instrumented
process's `service.name`. A remote endpoint resolved only through Route53 has an
empty `client` or `server`, and its canonical DNS name in `client.route` or
`server.route`. Kubernetes and ECS service identities take precedence and retain
the existing client/server labels. Other DNS sources keep their existing behavior.
This enrichment does not change names in RED metrics or traces.

For a frontend calling a backend through `cakes-api.internal`, the backend's OBI
can emit:

```prometheus
traces_service_graph_endpoint{service_name="cakes-backend",service_namespace="cakes",route="cakes-api.internal",source="obi"} 1
```

The frontend's OBI can emit:

```prometheus
traces_service_graph_request_total{client="cakes-frontend",client_service_namespace="cakes",server="",server_route="cakes-api.internal",source="obi"} 42
```

Examples omit other labels. All four service graph request metrics carry the route
labels. Prometheus emits empty route labels when unused; OTLP omits unused routes.
Mappings are emitted only for observed local addresses with known Route53 records.
No mapping is emitted if a process has no known route.

### Prometheus enrichment

These recording rules map server routes back to service names and namespaces,
collapse duplicate mappings from replicas, and preserve edges without a mapping:

```yaml
groups:
  - name: routed-service-graph
    rules:
      - record: obi_service_graph:server_endpoints
        expr: |
          max by (server_route, server, server_service_namespace) (
            label_replace(
              label_replace(
                label_replace(
                  traces_service_graph_endpoint{route!="",service_name!=""},
                  "server", "$1", "service_name", "(.+)"
                ),
                "server_route", "$1", "route", "(.+)"
              ),
              "server_service_namespace", "$1", "service_namespace", "(.*)"
            )
          )
      - record: obi_service_graph:request_rate5m
        expr: rate(traces_service_graph_request_total[5m])
      - record: obi_service_graph:server_request_rate5m
        expr: |
          (
            obi_service_graph:request_rate5m{server="",server_route!=""}
            * on (server_route) group_left (server, server_service_namespace)
              obi_service_graph:server_endpoints
          )
          or
          (
            obi_service_graph:request_rate5m
            unless on (server_route) obi_service_graph:server_endpoints
          )
```

For client enrichment, build the same mapping with `client`, `client_route`, and
`client_service_namespace`, and apply it to the server-enriched result. Apply
`rate` before enrichment so label changes do not mix counter histories. Histogram
buckets and failed-request counters can be enriched using the same mapping.

Several routes can map to one service. Each route must map to exactly one
`(service.name, service.namespace)` pair within the query's network scope. The
rules above assume one scope. If environments reuse DNS names, retain a shared
deployment/network label in the mapping aggregation and add it to every
`on (...)` clause. Do not join on the observing OBI's `instance` or `job`:
client and server are exported by different instances. Conflicting identities for
one route are ambiguous and cause the join to fail rather than select one.

### Limitations and future extensions

- **Direct VM records only:** CNAMEs, Route53 aliases, and wildcard records are
  excluded. Extend the fetcher to resolve CNAME chains and aliases. Load-balancer
  routes also require evidence linking the frontend address to backend services.
- **One service per VM:** OBI maps a local socket IP to the instrumented service.
  Multiple services sharing an address require port/process-aware attribution.
- **Observed addresses only:** loopback, NAT, proxies, public addresses, and
  multiple interfaces can prevent the observed socket address from matching the
  advertised DNS record. Cloud address metadata and backend attribution would be
  needed to bridge them. Incoming source IPs behind NAT can identify the gateway
  instead of the originating client.
- **Activity-based lifetime:** endpoint gauges reuse exporter TTL and reporter
  eviction. Idle mappings can expire; changed records and exited processes can
  retain old mappings until expiration. OTLP expiration is activity-driven, so
  cleanup can take up to twice the configured TTL while traffic continues.
  Extend publication to process and inventory lifecycle events for idle services
  and prompt removal of obsolete mappings.
- **Other hostname sources:** captured DNS, reverse DNS, and HTTP authorities
  still use the legacy endpoint naming. Extend provenance tracking to distinguish
  their routes from service identities too.
- **Counting across OBIs:** enrichment identifies endpoints but does not deduplicate
  requests observed by both client and server OBIs. Summing both observations can
  double-count. A future policy needs explicit observation provenance and a way
  to select or deduplicate observations across instances.

## Integration tests

`TestRoute53ServiceResolution` starts a digest-pinned Floci container, creates a
hosted zone and a record pointing at a backend container, and checks the exported
HTTP client metric's explicitly enabled `server` label. It uses a one-second Route53 interval and
replaces the record to verify periodic refreshes. Existing EC2/ECS metadata containers and the ECS API failure mock remain unchanged.

```sh
go test -v -run '^TestRoute53ServiceResolution$' -timeout 10m ./internal/test/integration/
```

Unit tests additionally cover pagination, IPv6, duplicate IPs, record exclusions,
snapshot retention on failure, record deletion, resolver precedence, and recovery
from an initial API error.
