# HTTP load baseline

Measured with `cmd/tunna-load` against a release build of the server on
loopback, on the maintainer's development machine (8-core desktop CPU,
NVMe, Windows), 2026-09-14, at commit `5248f13` plus the load tool. Metadata
in SQLite with `synchronous=FULL` and `_txlock=immediate`, one transaction per
write (ADR-0002 phase C not yet implemented). Eight seconds per profile.

| Profile | Workers | Requests/s | p50 | p95 | p99 | max | Throughput |
|---------|--------:|-----------:|----:|----:|----:|----:|-----------:|
| PUT 4 KiB | 16 | 480 | 5.8 ms | 111 ms | 637 ms | 1.84 s | 2.0 MB/s |
| PUT 4 KiB | 64 | 446 | 10.2 ms | 837 ms | 1.94 s | 4.57 s | 1.8 MB/s |
| GET 4 KiB | 16 | 4 303 | 1.1 ms | 13.8 ms | 17.9 ms | 29 ms | 17.6 MB/s |
| GET 1 MiB | 16 | 3 084 | 3.5 ms | 12.1 ms | 17.3 ms | 27 ms | 3 233 MB/s |
| Mixed 90/10 4 KiB | 16 | 3 361 | 0.5 ms | 29 ms | 51 ms | 747 ms | 13.8 MB/s |

Zero errors in every run. The load generator signs every request itself,
so its own HMAC and connection handling are inside the client-side numbers.

## What the numbers say

**Small writes are fsync-bound and serialized.** About 480 per second at
16 workers, and *fewer* at 64: more concurrency does not add throughput, it
only lengthens the queue, which is why p99 goes from 0.6 s to 1.9 s. Each
PUT pays two fsyncs, the blob and the metadata commit, and the immediate
lock makes the commits one at a time. This is the number ADR-0002 phase C
exists to change: the driver benchmark showed a hundred rows under one
fsync at 1.4 ms, so batching commits across concurrent PUTs should lift the
metadata side by an order of magnitude. The blob fsync stays per object and
is the floor after that.

**Large reads are limited by nothing the server does.** 3.2 GB/s of 1 MiB
objects over loopback is memory bandwidth and the kernel's socket path;
`ServeContent` streams straight from the file. The server's per-byte cost
is not measurable here.

**Small reads have a per-request cost worth looking at.** 1.1 ms median for
a 4 KiB GET on loopback is higher than the byte path explains; the 1 MiB
GET is only 2.4 ms slower despite 256 times the bytes. The candidates, in
the order to probe them: two SQLite point reads per GET (bucket, then
object) through `database/sql`; the Windows `CreateFile` open with
share-delete per request; the client's HMAC per request; Go's loopback HTTP
on Windows. A pprof profile of the GET path settles it. Until then, no
conclusion, only a number to beat.

**The mixed profile behaves.** Reads keep flowing while writes queue; the
median stays under a millisecond and the write tail shows up only in max.

## How to reproduce

```bash
go build -o tunna.exe ./cmd/tunna
go build -o tunna-load.exe ./cmd/tunna-load
TUNNA_DATA_DIR=./loaddata TUNNA_ADDR=127.0.0.1:18090 TUNNA_BOOTSTRAP_KEY=tk_load:secret ./tunna.exe &
TUNNA_LOAD_SECRET=secret ./tunna-load.exe -base http://127.0.0.1:18090 -key tk_load -profile put -size 4096 -workers 16 -duration 8s
```

Run the profiles one at a time; concurrent profiles confound each other.
Numbers from a laptop, a container, or a cloud disk will differ by an
order of magnitude on the write side; record the hardware with the result.
