# macup

`macup` is a small, CLI-first macOS application inventory and update checker written in Go.

If you've found yourself wrestling with how to update a lot of software in a hurry,
across a myriad of update platforms, `macup` is for you.

We avoid maintaining our own centralised version database by using all the others.
We use the AppStore, Homebrew, Sparkle Appcasts, Microsoft AutoUpdate, and the
Adobe catalogue, with heuristic-driven public lookups for everything else.
Having established whether or not updates are available, and what provider supplies them,
we can download updates at the user's request from as close to an authorised channel
as we can establish.

We don't change anything unexpectedly. `macup scan`, the default invocation, is **read-only**,
and `macup update` changes nothing until you pass `--yes`. Even then it enacts an update only
via the appropriate update provider, or downloads a signature-verified update for you to open.

For standard applications, `macup` looks for `.app` bundles in standard locations, reads
`Contents/Info.plist`, and outputs an inventory as a table or JSON, with available update
status from the locations listed above. A default scan contacts vendors and catalogues
locally and over the network; `macup scan --offline` guarantees that nothing leaves the
machine (see [What leaves the machine](#what-leaves-the-machine)).

## Install

Download the universal binary from the
[latest release](https://github.com/niallrmurphy/macup/releases/latest),
verify it against the published `SHA256SUMS`, and put it on your `PATH`:

```sh
tar -xzf macup_1.0.0_darwin_universal.tar.gz
shasum -a 256 -c SHA256SUMS
mv macup /usr/local/bin/
macup version
```

Or build from source with Go 1.22 or newer: `go install github.com/niallrmurphy/macup/cmd/macup@latest`.

## Requirements

- macOS (Apple Silicon or Intel), Go **1.22+** to build.
- No third-party Go dependencies. `plutil` (included with macOS) is used to decode binary plists.

## Build and scan

```sh
go build -o macup ./cmd/macup
./macup scan                     # all sources (apps, Homebrew, App Store)
./macup scan --apps              # user-updateable .app bundles (hides macOS system apps by default)
./macup scan --apps --include-system # also show Apple OS-managed system apps
./macup scan --brew              # Homebrew only
./macup scan --appstore          # Mac App Store only (mas required)
./macup scan --apps --brew       # two explicitly selected sources
./macup scan --brew --appstore --json
./macup scan --roots ~/Applications # custom app roots only
./macup scan --offline           # nothing leaves the machine
./macup scan --apps --sparkle     # Sparkle appcast checks only (on by default anyway)
./macup scan --all               # include entries already known to be current
./macup scan --status "NO SOURCE" # only entries with these statuses (comma-separated)
./macup scan --paths             # include bundle paths in the DETAIL column
./macup scan --no-lookup         # skip the public catalogue and feed lookups this run
./macup scan --fresh             # ignore cached provider results this run
./macup scan --cache-ttl 10m     # reuse provider results younger than ten minutes
./macup doctor
```

`--roots` sets the directories searched for `.app` bundles. Defaults are
`/Applications`, `/System/Applications`, `/System/Applications/Utilities`, and
`~/Applications`. Bundles within `/System/Applications` and
`/System/Library/CoreServices/Applications` are excluded from the application
results by default, because they are delivered with macOS updates. Use
`--include-system` to show them. The flag changes filesystem-app visibility
only; it does not turn on a provider by itself. Apple-distributed apps in
`/Applications` are *not* automatically excluded because they may have
independent App Store or vendor update mechanisms.

If any source flags are present, only those sources are scanned. With no
source flags, all sources are scanned. `--roots` also enables the `apps` source.

Optional dependencies: Homebrew (`brew`) for the Homebrew source and the
[mas CLI](https://github.com/mas-cli/mas) for the App Store source.
Missing tools produce warnings instead of aborting the scan. Homebrew maintains
a key source of available updates, so overall result quality is improved greatly
if you have it installed.

All selected sources are rendered in one table with the same columns. By
default the table omits entries a provider has positively reported as
`CURRENT` or `NOT FLAGGED`, and the footer says how many were hidden; pass
`--all` to list everything, or `--status "NO SOURCE"` (comma-separated, case
insensitive) to list only chosen statuses. The `DETAIL` column carries
provider reasons; `--paths` adds the bundle path and cask name, which doubles
the table width. JSON output always contains every entry.

Applications that no provider could evaluate are labelled from local evidence
instead of being left `NO SOURCE`. `SELF-UPDATING` means the bundle
has its own updater, named in `UPDATE SOURCE` (`sparkle`, `squirrel`,
`electron-updater`, `keystone`, `mozilla`, `paddle`, `devmate`); `MANAGED`
means something else updates it: `appstore` (receipt present), `browser`
(web-app shim), `parent` (helper or uninstaller), `setapp`, or
`vendor:<launchd label>` for a vendor-wide updater job. Updater frameworks
are found even if a vendor has tucked them away (Chromium-based browsers bury
Sparkle inside their own framework; Docker wraps a Squirrel app) and a
helper named like an updater (`BraveUpdater`, `opera_autoupdate`) also counts as
one. If we can't find any update source, we label the item `NO SOURCE`; this
helps us to distinguish having nothing to check from not having checked.

The `lookup` provider, on by default (`--no-lookup` or
`--providers=sparkle,microsoft,adobe` to leave it out), tries harder to find
a possible update source by:
* asking the App Store catalogue by bundle identifier;
* the Homebrew cask index by app name; and
* release feeds derived from evidence in the bundle.

If we get an answer suggesting there is a legitimate update, we label the item
`OUTDATED` with the relevant source named in `UPDATE SOURCE` and `lookup` added to
`SOURCE`; a source listing the same version makes it `NOT FLAGGED`; anything
less conclusive is noted in the column `DETAIL`. Catalogues outrank feeds derived from
a repository name found in a binary. Helpers and browser web-app shims are
skipped. Obviously, this requires network connectivity.

The Sparkle provider also uses feeds the bundle keeps outside `Info.plist`:
set in user defaults, or compiled into a binary (VLC and Path Finder, for
example). When a vendor publishes one feed per architecture, the one naming
the running architecture is chosen.

iPhone and iPad apps installed from the App Store on Apple silicon are
wrapped bundles without `Contents/Info.plist`. They are read from the inner
bundle, reported with `"platform": "ios"` in JSON, and use their App Store
item identifier so they reconcile with `mas` output.

A package `NOT FLAGGED` as outdated is **not** guaranteed to be current with
its vendor. The plain `.app` scan has no update-checking source and uses
`NO SOURCE`. Cask and App Store entries may also describe apps found in the
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
- Does not execute bundled app code, use elevated privileges, or modify software. Network requests are described under [What leaves the machine](#what-leaves-the-machine) and `--offline` turns them all off.
- Does not install non-platform-supported updates automatically. We currently feel this to be roughly doable via heuristics, but potentially unsafe.

## Development

```sh
go test ./...
go vet ./...
gofmt -w ./cmd ./internal
```

The core inventory parser and scanner tests can run on Linux, but testing on a Mac is necessary to exercise binary plist handling and verify discovery behavior on real-world app directories.

## Status and roadmap

**1.0** covers inventory (bundles, Homebrew, App Store), update checking
(Sparkle, Microsoft AutoUpdate, Adobe catalogue, public lookups), local
labelling of self-updating and managed apps, a result cache, an offline
mode, and a plan-first `macup update` that delegates to package managers and
verifies Sparkle downloads. See `CHANGELOG.md`.

Not yet: a verified download route for cask-index hits on apps installed
outside Homebrew (the index uses sha256), Skylum's run-time Sparkle
feeds, end-to-end macOS CI, and a SBOM export suitable for managing
CVE detection.

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

The Sparkle provider checks appcasts for `.app` bundles and is part of the
default provider set; `--providers=microsoft,adobe` or `--offline` leaves it
out, and `--sparkle` turns it on alone. It makes outbound requests to feed
URLs advertised by the apps themselves.
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

## Updating

```sh
./macup update                      # print the plan; nothing runs
./macup update --only slack,iterm   # limit the plan by name
./macup update --yes                # execute the plan
./macup update --fetch-only         # download and verify Sparkle updates only
```

`macup update` runs the same scan as `macup scan`, then maps every
`OUTDATED` entry to the most trustworthy route and prints the plan. Nothing
runs without `--yes`.

| Route | What runs | Who does the install |
|---|---|---|
| `brew` | `brew upgrade --formula NAME` or `--cask TOKEN` | Homebrew |
| `mas` | `mas upgrade ID` | the App Store |
| `msupdate` | Microsoft AutoUpdate's `msupdate --install --apps ID` | Microsoft AutoUpdate |
| `sparkle-fetch` | downloads the appcast enclosure, verifies it, opens it | you, from the opened disk image or archive |
| `manual` | nothing; the ACTION column says where to go | you |

The Sparkle route never replaces an application bundle: Sparkle's own
installer handles running apps, helpers, launch agents and permissions, and
reimplementing that is a recipe for breakage. Instead the enclosure is
downloaded into its own directory under `~/Library/Caches/macup/downloads`
(so an archive expanded by Archive Utility stays beside it), named from the
URL that actually served it, its Content-Disposition, or the file's own
format when the vendor publishes through a redirector script; its size is
checked against the appcast, and its **EdDSA signature is verified with the application's own
`SUPublicEDKey`** before the file is written. An enclosure without a signature,
or an app without a public key, is skipped unless `--allow-unverified` is
given, and is then labelled `UNVERIFIED`. Downloads carry a quarantine flag so
Gatekeeper treats them like any other download. `--fetch-only` stops after
staging. Every executed or failed action is appended to
`~/Library/Caches/macup/update.log`.

Apps whose newer version came only from a catalogue (`homebrew-cask`,
`itunes`) or a derived feed get a `manual` route: macup has a version number
but no vendor-signed artefact to fetch. For a cask that is not yet installed
through Homebrew, `brew install --cask --adopt TOKEN` may bring it under
Homebrew for future upgrades. Adobe and other vendor-managed apps are routed
to their own updater.

## What leaves the machine

macup is read-only, but a default scan does talk to the network, because
that is where update information lives. Here is what each step does, so you
can choose a mode you are comfortable with.

| Step | Contacts | Sends |
|---|---|---|
| Application inventory, bundle inspection, classification | nothing | nothing |
| Homebrew (`brew list`, `brew outdated`, `brew info`) | nothing: local tap state, auto-update suppressed | nothing |
| App Store (`mas outdated`) | Apple | your App Store account's installed apps, as `mas` does |
| Sparkle | each app's own feed host | the request itself; Sparkle feeds learn your IP and what app asked |
| Microsoft AutoUpdate (`msupdate --list`) | Microsoft | what MAU normally sends |
| Adobe catalogue | Adobe | a catalogue fetch |
| Lookup: App Store catalogue | Apple's iTunes Search API | bundle identifiers of apps with no other source |
| Lookup: Homebrew cask index | formulae.brew.sh | a download of the public index; matching happens locally |
| Lookup: derived feeds and generic endpoints | GitHub, GitLab, SourceForge, or the vendor named in the bundle | the request itself |
| Result cache | nothing | nothing; stored under `~/Library/Caches/macup` |
| `macup update --yes` | Homebrew, Apple, Microsoft, and each Sparkle app's enclosure host | whatever those managers send; Sparkle downloads are plain fetches |

Modes:

- **`macup scan`** does all of the above.
- **`macup scan --no-lookup`** keeps the vendor channels (Sparkle, MAU, Adobe,
  `mas`) and skips the two public catalogues and derived feeds.
- **`macup scan --providers=none`** runs no update provider at all but still
  lets `mas` ask Apple for outdated apps; combine with `--apps --brew` to
  avoid that.
- **`macup scan --offline`** guarantees nothing leaves the machine: local
  inventory, bundle inspection and classification, and Homebrew's local
  state. No provider runs, `mas` is not asked, and cached answers from
  earlier network runs are not consulted either, so every status reflects
  only what is on disk now. Expect `NO SOURCE`, `SELF-UPDATING` and `MANAGED`
  rather than versions.

`macup probe` is opt-in at every network stage and documented separately
below.

### Result cache

Network providers dominate scan time. Their results are cached under
`~/Library/Caches/macup/results` and reused for one hour by default
(`--cache-ttl`, `0` disables). Keys include whatever must invalidate an
entry: Sparkle checks and lookups are keyed by bundle path plus installed
version and build, so an app that was updated is re-checked immediately
regardless of age; the Microsoft and Adobe results are keyed by the
installed versions of every app of that vendor. `--fresh` ignores cached
results for one run while still storing new ones. Homebrew, the App Store
and the local inventory are never cached: they are quick, and they are what
you check after acting.

### Scan progress

Every source and provider announces when it starts and what it found on
**stderr**, so nothing mixes into JSON or tabular **stdout**: the application
scan, Homebrew, the App Store, Microsoft AutoUpdate (which can take over a
minute), Adobe, and Sparkle. `--quiet` suppresses all of these.

With `macup scan --sparkle`, a one-line start message and a closing summary
go to **stderr**.
`macup scan --sparkle --verbose` additionally reports each feed check as it
starts and finishes, which makes slow network requests visible.
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

## Microsoft AutoUpdate (read-only)

`macup scan --providers=microsoft` checks pending updates through the locally installed
Microsoft AutoUpdate `msupdate --list --format plist`. It never installs updates.
The default scan also attempts this provider if available. On macOS, the first
invocation may require a privacy permission prompt. `msupdate` can report only
products registered with MAU; it does not cover every Microsoft app.

The provider reports only pending updates. A product omitted by MAU is not
necessarily current. Parsed updates are linked to an unambiguous discovered
application when possible; otherwise they remain visible independently.
If MAU's output schema differs from the documented examples, a warning is
reported instead of guessing at an update.

### Inventory sources versus update providers

Inventory source flags (`--apps`, `--brew`, `--appstore`) select *what to inventory*;
with none specified, all available inventory sources are used. `--roots`
implies the app inventory source.

Update-check providers are separate. `--providers=all` (the default) enables
Sparkle and Microsoft AutoUpdate where application scanning is selected.
`--providers=none` disables both, while `--providers=microsoft` or
`--providers=sparkle` runs only the named checker. Examples:

```sh
macup scan --apps --providers=microsoft
macup scan --providers=none
macup scan --apps --providers=microsoft,sparkle --json
```

Provider failures do not suppress discovered applications. MAU diagnostics now
include a bounded excerpt of the command output on conversion failure, which
may contain machine-specific data; inspect logs before sharing.

### Adobe catalogue provider

`macup scan --apps --providers=adobe` checks Adobe's own published product
catalogue over HTTPS and matches supported applications against Adobe's local
installed-product inventory (`/Library/Application Support/Adobe/caps/hdpim.db`).
The provider is on by default and can be disabled with `--providers=none`.
It requires the `sqlite3` command and Adobe's local inventory to be readable.
The integration is read-only; no Adobe installer/update helper is executed.

Recognised products are Lightroom Classic (`LTRM`), Photoshop 2023
(`PHSP`), Adobe Content Synchronizer (`COSY`), and CCXProcess (`CCXP`).
The latter two are Creative Cloud components, not independent user-facing
applications. Their package and bundle version formats may differ. A newer **same-major catalogue release** is reported as
`OUTDATED`; a newer **major-generation release** as `UPGRADE AVAILABLE`.
These are *published catalogue candidates*, **not** confirmed Creative Cloud
install offers: eligibility, license, rollout, OS minimum and policy may differ.
`CURRENT` is deliberately not inferred from a catalogue entry alone. An
unrecognised schema, missing local product identity or failed fetch cannot be
used as evidence that an application is up to date.

### Experimental long-tail probe

`macup probe` is an **experimental**, independent discovery tool. It does **not**
modify the statuses produced by `macup scan`, and does not execute application
updaters or install anything. Its JSON is evidence for human review, not an
assertion that an update is applicable.

```sh
./macup probe "/Applications/Nisus Writer Pro.app"                  # local evidence only, no network
./macup probe --lookup "/Applications/Signal.app"                    # plus keyed public lookups
./macup probe --lookup --origins /Applications/OSCAR.app             # plus local download-origin records
./macup probe --fetch "/Applications/Nisus Writer Pro.app"          # bounded HTTPS page retrieval
./macup probe --fetch --model llama3.2:latest "/Applications/Nisus Writer Pro.app"
```

Evidence is gathered in order of cost and trust. Each stage only runs when
its flag is set, except the first two, which are local and read-only:

1. **Bundle contents.** `Info.plist` URL values; updater frameworks
   (`families`: Sparkle, Squirrel, Keystone, Setapp, Paddle, DevMate, Electron);
   electron-updater's `app-update.yml`; `package.json` inside `app.asar`
   (homepage, repository, publish targets); Mozilla's `application.ini`
   update template; and a bounded scan of the main executable, other
   binaries, plug-ins and JavaScript bundles for literal HTTPS URLs. Update
   endpoints live in dylibs as often as in the main executable. URLs are
   ranked by *affinity*: whether the host or path names the product, the
   bundle identifier's labels, or the signing organisation. Dependency
   documentation hosts without affinity are dropped. Nothing in the bundle
   is executed.
2. **Local OS records.** The app's own preferences (`SUFeedURL` set at run
   time), vendor updater jobs in `/Library/LaunchAgents` and
   `/Library/LaunchDaemons`, and the code signature (`signer`, `team_id`).
3. **Classification** (`management`). Who updates the bundle: `appstore`
   (receipt or App Store signature), `pwa` (browser web-app shim), `helper`
   (uninstaller, helper, nested background agent), `setapp`, `vendor` (a
   vendor-wide updater job exists), or `self` (an embedded updater family was
   found). Absent when nothing is known.
4. **`--origins`.** Where macOS recorded the download: Spotlight
   `kMDItemWhereFroms` on the bundle and the LaunchServices quarantine
   database, joined by the quarantine event UUID when the bundle still carries
   it, otherwise by download filename. These records are a browsing history,
   so this stage is opt-in and the matched URLs are reported under `origins`.
5. **`--lookup`.** Keyed public queries that need no maintained database: the
   iTunes Search API by bundle identifier; the Homebrew cask index
   (`formulae.brew.sh/api/cask.json`, cached under `~/Library/Caches/macup`
   for a day) matched by app filename with bundle identifier as tie-breaker;
   and feeds derived from evidence already found (GitHub releases, GitLab
   tags, SourceForge files, Sparkle appcasts, electron-updater `latest-mac.yml`,
   Mozilla update server). Each produces a `lookups` entry with `comparison`
   of `newer`, `same`, `older`, `unknown` (numbering schemes differ), or
   `none-offered` (the vendor's update server answered with no update for this
   exact build). When none of those answered, a bounded generic pass (at most
   eight requests, eight seconds each) interprets the best vendor-affine
   candidates by shape rather than by vendor: JSON under conventional keys
   (`version`, `tag_name`), XML version attributes or elements, short
   plain-text version files, YAML manifests, and HTML pages listing
   versioned Mac installers whose filename names the product. Beside
   directory URLs it tries well-known files such as `LATEST.TXT`,
   `appcast.xml` and `latest-mac.yml`; for stable download links it issues a
   HEAD and reads the version from the redirect target or served filename.
   Redirects are followed up to three hops over HTTPS only, every hop passing
   the same public-address check, and a lookup that landed somewhere other
   than the bundle said carries a `redirected to` note.
6. **`--fetch`** and **`--model`** interpret candidate pages, as below.

A `newer` lookup is a strong hint, not a verdict: App Store and Homebrew may
lag or lead the vendor's direct channel, and a GitHub feed may list a build
for a different platform. Pre-release tags are skipped in forge feeds.

The model option calls only `http://127.0.0.1:11434/api/generate` (Ollama must
already be running); no model is downloaded automatically. HTTP fetching is
opt-in; redirects are disabled, private/loopback destinations are blocked,
responses are capped at 512 KiB, and at most three pages are fetched.
Some apps will produce zero candidates, and JS-rendered
release pages may yield little text. `llm.verified` means an exact quoted
passage and the proposed version appear together in retrieved text, **not**
that update eligibility or product identity has been independently established.
Never treat a zero-result probe as evidence the installed app is current.

### Experimental whole-inventory probe

`macup probe --all` enumerates the standard application roots using the same
scanner as `scan`, excluding system-volume apps unless `--include-system` is
set. It emits a single JSON object with `total`, `succeeded`, `failed`,
`inventory_warnings`, and an ordered `results` array. Each result contains
its application `path` and either its `report` or an `error`. One broken app
does not abort the batch. The summary is printed to stderr.

```sh
macup probe --all > probe-results.json
macup probe --all --lookup --origins --workers 4 > probe-results.json
macup probe --all --roots /Applications --fetch > probe-results.json
macup probe --all --fetch --model llama3.2:latest --workers 2 > probe-results.json
```

Network fetching and Ollama remain explicit opt-ins. `--workers` is bounded
to 1–4, and `--max-pages` to 1–3 per app. Even at those limits, a full
Ollama-backed run can take substantial time and make many HTTPS requests.
The probe never alters `scan` statuses or installs anything.
