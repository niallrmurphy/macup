# Changelog

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
