# macup

A small, **read-only**, CLI-first macOS application inventory tool written in Go. An early milestone toward an open-source application update checker.

**Current functionality:** find `.app` bundles, read `Contents/Info.plist`, and output an inventory as a table or JSON. This release **does not check for updates**, assess CVEs, or install anything.

## Requirements

- macOS (Apple Silicon or Intel), Go **1.22+** to build.
- No third-party Go dependencies. `plutil` (included with macOS) is used to decode binary plists.

## Build and scan

```sh
go build -o macup ./cmd/macup
./macup scan                     # all sources (apps, Homebrew, App Store)
./macup scan --apps              # .app bundles only
./macup scan --brew              # Homebrew only
./macup scan --appstore          # Mac App Store only (mas required)
./macup scan --apps --brew       # two explicitly selected sources
./macup scan --brew --appstore --json
./macup scan --roots ~/Applications # custom app roots only
./macup doctor
```

`--roots` sets the directories searched for `.app` bundles. Defaults are
`/Applications`, `/System/Applications`, `/System/Applications/Utilities`, and
`~/Applications`.

If any source flags are present, only those sources are scanned. With no
source flags, all sources are scanned. `--roots` also enables the `apps` source.

Optional dependencies: Homebrew (`brew`) for the Homebrew source and the
[mas CLI](https://github.com/mas-cli/mas) for the App Store source.
Missing tools produce warnings instead of aborting the scan.

All selected sources are rendered in one table with the same columns.
A package `NOT FLAGGED` as outdated is **not** guaranteed to be current with
its vendor. The plain `.app` scan has no update-checking source and uses
`UNCHECKED`. Cask and App Store entries may also describe apps found in the
filesystem; the current table keeps these as source-specific rows rather than
making speculative identity matches.

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
2. Homebrew inventory provider with `brew list --versions` and `brew outdated --json=v2` (implemented).
3. Sparkle appcast support with correct version and channel semantics.
4. App Store inventory via `mas` (implemented); caching, error reporting, end-to-end macOS CI remain future work.

The planned `macup outdated` and `macup audit` commands **do not exist yet**.

## License

MIT. See `LICENSE`.

### Reconciled inventory

When `--apps` and `--brew` are scanned together, installed cask app artifact
destinations from `brew info --json=v2 --installed` are matched against
discovered `.app` paths. An unambiguous exact path becomes one table row
(`apps+brew-cask`), while JSON includes a normalized `entries` array and
retains the original provider sections. Multi-app casks and ambiguous matches
remain separate to avoid false attribution. Cask artifact targets are recorded
install destinations, not a fresh filesystem check.

### App Store identity reconciliation

When `--apps` and `--appstore` are enabled, `macup` checks for a regular
`Contents/_MASReceipt/receipt` file and queries Spotlight's
`kMDItemAppStoreAdamID` using macOS `mdls`. An application and a `mas list`
entry merge only when their numeric App Store IDs match uniquely. Entries
with missing Spotlight metadata, duplicate IDs, or conflicting Homebrew
ownership remain separate. This heuristic does **not** cryptographically
validate App Store receipts. It runs read-only and only when `mas` is present.
