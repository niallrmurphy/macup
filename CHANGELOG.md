# Changelog

## 1.2.0 (2026-11-04)

- The Sparkle route reuses an enclosure staged by an earlier run when it
  still passes the appcast size and EdDSA checks, so running `update` again
  before installing does not download it a second time. A staged file that
  no longer passes is removed and fetched afresh.
- Staged downloads are retired once they cannot be reused: each staging
  directory records its app and version, and the rescan after `--yes` or
  `--fetch-only` removes it once that version is installed, the feed has
  moved on, or the app is gone. Directories with no record, from older
  releases, go after 30 days. The summary lists removals and space freed.
- A newer version found only in a catalogue the copy was not installed
  from, the App Store or the Homebrew cask index, is now `OTHER CHANNEL`
  rather than `OUTDATED`: the version exists, but reaching it means
  switching who installs the app (a sandboxed App Store build, or Homebrew
  taking over). The plan still lists these entries; an App Store hit gets
  the listing URL in its reason and is never installed automatically.
- New `apple` update provider: `macup scan` runs `softwareupdate --list` and
  reports pending macOS, Command Line Tools and Safari updates. It is part of
  `--providers=all`, is skipped by `--offline`, and is not cached.
- `macup update` gains an `Apple` route (`sudo softwareupdate --install
  LABEL`, batched into one call). Updates that need a restart run only with
  `--yes --apple-restart`, and macup never restarts the machine.

## 1.1.0 (2026-10-29)

- `macup update` gains a `brew-adopt` route for an app installed outside
  Homebrew when exactly one cask ships an app with its filename. The plan
  shows `brew install --cask --force TOKEN`, with `--appdir` when the app
  lives outside `/Applications` so it is replaced in place, and
  `--yes --adopt` runs it; Homebrew then owns future upgrades. Without
  `--adopt` the action is skipped with a hint, and the summary says so.
- Homebrew's own `--adopt` flag, which the README used to suggest, is not
  used: it accepts only a bundle whose version already matches the cask's,
  which an outdated app never does.
- A cask chosen by tie-break among several that ship the same app name, or
  a pkg cask matched only because its uninstall stanza quits this bundle
  identifier (Elgato Camera Hub quits the DisplayLink Manager it bundles),
  still reports a version but is never offered for adoption.
- Catalogue-only entries get clearer manual advice: an App Store listing
  says so, and a cask hit without a clean match says why.
- Homebrew actions are batched by shared command rather than by a fixed
  argument position.

## 1.0.2 (2026-10-28)

- Sparkle enclosures given as relative URLs (Tailscale's appcast lists
  `Tailscale-1.105.110-macos.zip`) are resolved against the feed URL, as
  Sparkle does, instead of being rejected as non-HTTPS.
- When an EdDSA check fails and the enclosure does not even name the
  installed app, the error says the vendor may have renamed or re-keyed the
  product (Mirage Host's feed now serves Mirage Beacon).

## 1.0.1 (2026-10-28)

Fixes and improvements from the first field report on 1.0.0.

- `macup update --yes` runs `brew update` first (`--skip-brew-update` to
  skip), so the versions installed are the ones the plan showed and the
  first upgrade no longer triggers Homebrew's auto-update mid-run.
- Homebrew actions are batched into one `brew upgrade --formula ...` and one
  `brew upgrade --cask ...`, so dependencies are resolved once instead of
  being revisited per package.
- After executing, `update` rescans and prints a summary: updated,
  downloaded-and-verified-but-not-yet-installed (opening an archive is not
  installing), still outdated, newly outdated since the plan, Homebrew
  dependencies that changed as side effects (new Python, OpenSSL, removals),
  skipped, and failed. `--no-rescan` restores the old behaviour.
- iTunes lookups only accept Mac listings: a shared bundle identifier no
  longer compares a Mac app against its iOS version (Spotify 9.x).
- A Sparkle enclosure that is not an HTTPS URL is reported with the URL.
- When Homebrew calls a numerically lower version an upgrade (openssl@3
  3.6.3 -> 3.5.9, a version-scheme change), the row says so in DETAIL.
- When msupdate prints an error instead of a property list (for example an
  XPC connection failure), the warning shows that error rather than a
  plist conversion failure.
- The table shows paths automatically for bundles that share a name.

## 1.0.0 (2026-10-28)

First release.

### Inventory
- Discovers `.app` bundles under the standard roots, reads `Info.plist`
  (binary plists via `plutil`), and handles iPhone and iPad wrapper bundles.
- Homebrew formulae and casks, with cask-to-bundle reconciliation by path.
- App Store apps via `mas`, with receipt-based identity reconciliation.

### Update checking
- Sparkle appcasts, including feeds the bundle keeps outside `Info.plist`
  (user defaults, or compiled into a binary) and per-architecture feeds.
- Microsoft AutoUpdate via `msupdate --list`, including the per-app
  Update Assistant schema MAU emits while an app is open.
- Adobe catalogue comparison for Creative Cloud products.
- A `lookup` provider for everything else: the App Store catalogue by bundle
  identifier, the Homebrew cask index by app name, and release feeds derived
  from evidence in the bundle (GitHub, GitLab, SourceForge, electron-updater,
  Mozilla), with a bounded generic pass over vendor endpoints.
- Local labelling of what no provider could check: `SELF-UPDATING` with the
  embedded mechanism, `MANAGED` with who updates it, or `NO SOURCE`.

### Output and operation
- One table for every source, hiding known-current rows by default;
  `--all`, `--status`, `--paths`; JSON output with everything.
- Progress for every source and provider on stderr; `--quiet`, `--verbose`.
- A result cache keyed by installed version so a rescan takes seconds;
  `--cache-ttl`, `--fresh`.
- `--offline`: nothing leaves the machine.

### Updating
- `macup update` prints a plan and runs nothing without `--yes`: Homebrew,
  `mas` and Microsoft AutoUpdate delegation, and Sparkle downloads verified
  with the application's own EdDSA public key before being opened.
  `--fetch-only`, `--only`, `--allow-unverified`.

### Probe
- `macup probe` exposes the evidence pipeline for one app or the whole
  inventory, with opt-in origins (quarantine records), lookups, page
  fetches and a local Ollama model.
