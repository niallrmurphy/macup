# macup

A small, **read-only**, CLI-first macOS application inventory tool written in Go. An early milestone toward an open-source application update checker.

**Current functionality:** find `.app` bundles, read `Contents/Info.plist`, and output an inventory as a table or JSON. This release checks Homebrew and App Store outdated status, and supports optional Sparkle appcast checking. It does not assess CVEs or install anything.

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
./macup scan --apps --sparkle     # opt-in Sparkle appcast HTTP requests
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
- Does not execute bundled app code, use elevated privileges, or modify software. Sparkle checks perform HTTPS requests only when explicitly enabled.

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
3. Sparkle appcast support (conservative, opt-in initial implementation).
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

### Sparkle appcast checking (experimental)

`macup scan --sparkle` scans `.app` bundles and checks `SUFeedURL` appcasts;
`--apps --sparkle` is equivalent. Sparkle checks are **not** part of the default
scan because they make outbound network requests to URLs advertised by apps.
Only HTTPS feeds are fetched (no redirects, 8-second timeout, 2 MiB cap).
A successful check may add `sparkle` provenance and an available version to
filesystem-only app rows. Raw check results are included under `sparkle` in JSON.

**Limitations:** This is an advisory comparison, **not Sparkle's own update
decision**. It handles only numeric `CFBundleVersion` / `sparkle:version`
values and unchannelled, unrestricted full releases. Channels, OS version
restrictions, differential updates, phased rollouts, architectures, ignored
updates, update feed overrides and signature validation are not implemented.
`NOT FLAGGED` means only that no higher eligible numeric build was seen in the
parsed feed; it is **not** proof of being fully up-to-date. No archives are
downloaded or installed. Feed URLs in applications should be treated as
untrusted, especially on untrusted machines.

### Sparkle progress

With `macup scan --sparkle`, feed checks are reported to **stderr** as they
start and finish, followed by a summary. This makes slow network requests
visible without mixing progress text into JSON or tabular **stdout**.
`macup scan --sparkle --quiet` suppresses these messages (also useful with
`--json`). Only apps with a discovered HTTPS Sparkle feed and a comparable
numeric build trigger requests; the summary counts all discovered feeds,
including those left unchecked.

### Update evidence in table output

The unified table includes an `UPDATE SOURCE` column: `sparkle`, `homebrew`,
or `appstore` when that provider flagged an update, and `-` when no update
was established. This is separate from the `SOURCE` column, which tracks
where a record was discovered. For example, `apps+sparkle` with
`UPDATE SOURCE` set to `sparkle` means the app bundle was discovered locally
and its available version came from an appcast.

### Sparkle diagnostics

Use `macup scan --sparkle-debug` for per-feed explanations on **stderr**:
installed build, feed URL, appcast item count, comparable releases, and release
filtering counts (channel, delta, OS compatibility, minimum-update constraint,
missing or incomparable versions). It implies `--sparkle`. Unlike normal progress,
this detailed output is also shown when `--quiet` is specified.
Pre-fetch rejections, such as invalid feed URLs or non-numeric bundle builds,
are now reported as skipped instead of silently counted. These diagnostics
are advisory and do not replicate all of Sparkle's update-selection policies.

### Sparkle feed diagnostics

`macup scan --sparkle-debug` prints per-feed counts of item-level and enclosure-level
build and display-version fields to stderr, in addition to eligibility counts.
For feed-format investigations, `macup scan --sparkle-debug-xml ./sparkle-shapes`
writes one `.shape.txt` sample per successfully retrieved feed. These samples
contain **only XML element and attribute names from the first item**, never
attribute values, feed contents, download URLs, or application binaries.
The sample directory is created with mode `0700` and files with `0600`.
Inspect samples before sharing: element/attribute names themselves may disclose
application-specific metadata. Network checks remain HTTPS-only and read-only.
