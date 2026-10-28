# Changelog

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
