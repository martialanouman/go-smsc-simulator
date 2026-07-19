# Load & NFR harness (S7 / T4)

The simulator's non-functional requirements (plan §11, spec §NFR):

| NFR | Target (per virtual SMSC) | How it is checked |
| --- | --- | --- |
| Sustained throughput | ≥ 15 000 msg/s | automated — `make loadtest` |
| Determinism under load | per-bind aggregate holds | automated — `make loadtest` |
| Memory at rest | < 50 MiB | automated proxy + manual RSS |
| Cold start | < 2 s | manual, against the container |

## Automated (in-process) — `make loadtest`

`internal/smsc/load_test.go` is behind the `loadtest` build tag, so it never runs in
`make test` / CI. It drives real `submit_sm` traffic over a loopback socket through the
in-process SMPP client:

- **`TestLoad_ThroughputNFR`** — 8 concurrent binds on one healthy virtual SMSC; asserts
  the aggregate rate ≥ 15 000 msg/s. (Observed on an Apple M4 Pro: ~85 000 msg/s.)
- **`TestLoad_DeterminismUnderLoad`** — 8 concurrent binds on a seeded `flaky-carrier`;
  asserts each bind's success rate stays within tolerance of the configured 0.8. This is
  the per-bind statistical guarantee — invariant (a) is scoped per bind, so the assertion
  never depends on inter-bind ordering.
- **`TestLoad_IdleMemory`** — 200 idle binds; asserts the heap stays well under 50 MiB. An
  in-process `HeapAlloc` is only a proxy for container RSS (see below), but it catches a
  gross per-bind leak.
- **`BenchmarkThroughput`** — reports msg/s for profiling / regression tracking.

```sh
make loadtest
# or, with a profile:
go test -tags loadtest -run '^$' -bench '^BenchmarkThroughput$' -cpuprofile cpu.out ./internal/smsc
```

## Manual (against the container image)

Numbers that only mean something against the real artifact — measure them on the built
image, not in-process.

### Cold start < 2 s

```sh
make docker
cid=$(docker run -d -p 9000:9000 -v "$PWD/examples/healthy.yml:/etc/smsc/config.yml:ro" smsc-simulator:dev)
start=$(date +%s.%N)
until curl -sf http://127.0.0.1:9000/metrics >/dev/null; do sleep 0.02; done
echo "ready in $(echo "$(date +%s.%N) - $start" | bc)s"
docker rm -f "$cid"
```

Observed: a scratch image (~14.5 MB) becomes ready in ~0.04 s.

### RSS < 50 MiB per virtual SMSC at rest

```sh
docker stats --no-stream smsc-simulator   # MEM USAGE column, idle
```

Observed: ~3.4 MiB for one healthy virtual SMSC at rest.

### Sustained 15 000 msg/s from an external generator

The in-process harness proves the server side. To exercise the full network path, point an
external SMPP load generator — the gateway-under-test's own harness, or a standalone tool —
at the container's SMPP port and read the served counts from `GET /metrics`
(`smsc_submit_sm_received_total`, `smsc_submit_sm_outcome_total`). This generator lives outside
`go.mod`: `internal/` packages (including the in-process client) cannot be imported by a
separate module, so an external tool must speak SMPP itself.
