# macup

A small, **read-only**, CLI-first macOS application inventory tool written in Go. Milestone 1 for a future open-source application update checker.

**Current functionality:** find `.app` bundles, read `Contents/Info.plist`, and output an inventory as a table or JSON. This release **does not check for updates**, assess CVEs, or install anything.

## Requirements

- macOS (Apple Silicon or Intel), Go **1.22+** to build.
- No third-party Go dependencies. `plutil` (included with macOS) is used to decode binary plists.

## Build

```sh
go build -o macup ./cmd/macup
./macup scan
./macup scan --json
./macup doctor
```

Or run directly:

```sh
go run ./cmd/macup scan --json
```

Custom scan roots:

```sh
./macup scan --roots "$HOME/Applications,/Applications" --json
```

Default roots: `/Applications`, `/System/Applications`, `/System/Applications/Utilities`, and `~/Applications` (if available).

## JSON format

```json
{
  "applications": [{"name":"Example","bundle_id":"com.example.app","version":"1.0","build":"1","path":"/Applications/Example.app"}],
  "warnings": [],
  "roots": ["/Applications"]
}
```

## Design and limitations

- Does not follow symlinks; skips walking inside `.app` bundles. Recursive discovery is bounded to five directory levels below each root, so deeply nested apps may not be found.
- Keeps separately installed app copies, even if they share a bundle ID; deduplicates only by normalized path.
- Reads only top-level scalar plist keys. If the plist is binary, invokes `/usr/bin/plutil` to convert it to XML in memory.
- A broken bundle appears as a warning, not an application. Missing scan roots are silently skipped.
- App bundles outside the configured roots, and some applications without conventional Info.plist files, may be missed.
- Does not execute bundled app code, use elevated privileges, fetch the network, or modify software.

## Development

```sh
go test ./...
go vet ./...
gofmt -w ./cmd ./internal
```

The core inventory parser and scanner tests can run on Linux, but testing on a Mac is necessary to exercise binary plist handling and verify discovery behavior on real-world app directories.

## Roadmap

1. **Milestone 1 (this release):** safe local inventory, JSON/table, diagnostics.
2. Homebrew provider using `brew outdated --json=v2`.
3. Sparkle appcast support with correct version and channel semantics.
4. Mac App Store provider through `mas`, caching, error reporting, end-to-end macOS CI.

The planned `macup outdated` and `macup audit` commands **do not exist yet**.

## License

MIT. See `LICENSE`.
