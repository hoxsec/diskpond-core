# DiskPond Core

The backup engine used by the [DiskPond desktop app](https://github.com/hoxsec/DiskPond).
Kopia provides snapshot management, deduplication, encryption, compression and
storage-provider support.

## Integration

DiskPond embeds this repository as its pinned `apps/core` submodule and builds the
engine with `pnpm build:core` from the application repository. See the application's
[development guide](https://github.com/hoxsec/DiskPond/blob/main/DEVELOPMENT.txt)
for toolchain, build and verification instructions.

The fork adds desktop integration, repository discovery and legacy-format
compatibility, source handling and repository statistics. DiskPond Cloud uses the
standard S3 backend; the retired CloudBlink-specific proxy backend has been removed.

## Upstream and license

This engine derives from [BlinkDisk Core](https://github.com/blinkdisk/core),
itself a fork of [Kopia](https://github.com/kopia/kopia). Thanks to the Kopia,
BlinkDisk and dependency contributors. The engine retains its [Apache-2.0 license](LICENSE)
and upstream copyright notices. Application provenance and pinned revisions are
recorded in [UPSTREAM.txt](https://github.com/hoxsec/DiskPond/blob/main/UPSTREAM.txt).
