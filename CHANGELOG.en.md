# Changelog

[한국어](CHANGELOG.md) · English · [中文 (upstream original)](CHANGELOG.zh.md)

This document records the changes that the ARTEX global edition (this fork) adds on top of the upstream repository. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

The upstream ARTEX project's per-version release history (0.3.x and earlier) and contributor list are preserved verbatim in Chinese in [`CHANGELOG.zh.md`](CHANGELOG.zh.md). As with `README.zh.md`, the original text is kept unchanged so that it stays easy to compare against upstream changes. The detailed content and rationale for each change can be found in the repository's commit history.

## [Unreleased]

### Added

- **Task assurance and change-aware retesting.** A multilingual Assurance tab persists target snapshots, evidence-bound operator reviews, explicit coverage and explained retest plans. Selected findings use the existing retest workflow with atomic snapshot checks, idempotent dispatch and historical handling of stale results; complete records export as JSON. Archive format v4 preserves assurance and terminal retest history with active-run and compaction checks. The offline `artex -retest-plan FILE` mode remains available. See the [workspace guide](docs/task-assurance.md) and [offline planning guide](docs/change-aware-retesting.md).

### Localization (i18n)

- **Added Spanish documentation.** Translated `README.es.md` from the English README into natural Spanish and added a Spanish link to the language navigation of all five READMEs (English, Korean, Chinese, Spanish, Japanese). Code, commands, payloads, URLs and identifiers are preserved; only descriptive text is translated.
- **Added Japanese (ja) across the whole localization stack.** Japanese joins the app's supported languages and can be selected in Settings. UI messages (`web/messages/ja.json`), the date locale (`ja-JP`), the agent output language, and the settings and server message catalogs (`config/msg_i18n.go`, `server/errmsg_catalog.go`) are translated into Japanese, while the agents' internal reasoning prompts stay in their original language as for every other locale. For docs, `README.ja.md` was added and the language navigation and supported-language notes across all five READMEs were updated to include Japanese.

### Fixed

- Distinguish task report loading failures from empty reports and offer a retry button. Late responses from a previous task cannot replace the current task report.

## [0.3.15-global.1] · 2026-10-14 · ARTEX-global transition release

The release that turns the Korean edition (`0.3.15-ko.1`) into ARTEX-global, a multilingual edition for defenders and researchers worldwide. Based on upstream ARTEX 0.3.15; no agent features were changed.

### Identity · multilingual (ARTEX-global rename)

- **Rebranded the ARTEX Korean edition as the ARTEX global edition.** The identity shifts from a region-specific edition to a multilingual edition that defenders and researchers worldwide can reach. No agent features were changed; only the multilingual layer and the docs were touched.
  - **Switched the default language from Korean to English.** User-facing output now defaults to English (bootstrap layer `config.DefaultLanguage`, web `DEFAULT_LOCALE`, `config.example.json`, and the `.env.example` comment); Korean, Chinese, and Spanish are selectable in Settings. The agent-package standalone fallback (`agent.defaultOutputLang`) and the Korean golden tests were left in place to avoid regressions.
  - **Reworded the identity strings to the global edition.** Replaced the "Korean edition" wording in the READMEs (Korean, English, Chinese), CONTRIBUTING, SECURITY, the maintainer runbook, and the issue/PR templates, and renamed the English README's "Why a Korean edition" section and its anchor (`#why-a-korean-edition` → `#why-a-global-edition`). The repository slug and image path are renamed to `artex-global` together in the 'Repository rename' section below in this release.

### English-first README and screenshots

- **Promoted the English README to the repository landing page.** The English document is now `README.md` (the first screen), the Korean body moved to `README.ko.md`, and the links/anchors from the root docs to the top-level README were rewired to English=`README.md`, Korean=`README.ko.md`. The README links inside the detections tree were left as-is.
- **Replaced the preview screenshots with English ones.** Recaptured the mock UI in the default English and placed them under `screenshots/en/` (dashboard, findings, chat); the old Korean captures (`screenshots/ko/`) were removed.
- **Made on-screen dates follow the app language.** Replaced 18 hard-coded `ko-KR` locales with `getDateLocale()` in `web/src/i18n/datetime.ts` (display language → BCP47), so dates render in English by default.
- **Translated the demo data to English.** Translated ~220 user-facing Korean strings in the mock data (`web/src/lib/mock/{data,handler}.ts`) to English, preserving commands, payloads, URLs, and identifiers verbatim.

### Repository rename (artex-ko → artex-global)

- **Renamed the repository to `artex-global`.** Changed the GitHub slug to `jiwoochris/artex-global` (old URLs kept via GitHub redirects) and the container image path to `ghcr.io/jiwoochris/artex-global`. Replaced 126 occurrences across 37 files in the repo — badges, clone URLs, `docker-compose.yml`, `release.yml`, `install.sh`, docs, edition labels, detection-rule authors, the MISP event name. The upstream Go module (`github.com/Autumn-27/artex`) and the historical version tag (`0.3.15-ko.1`) were left unchanged.

### Localization (i18n)

- **Show built-in agent names and descriptions per language.** The built-in agents (goal decomposition, planning, main, execution, Auto, penetration test, vulnerability retest) are DB seed data (`builtinAgents` in `db/db.go` and the retest agent in `server/finding_retests.go`), so their names, descriptions, and prompt-variable labels used to appear only in Chinese. They are now looked up by agent `key` in `web/messages/` and shown in the current language (falling back to the DB value). User-created agents keep showing their DB values. Applied to the agent list, the editor drawer, and the chat screen; approval/intercept records that come from a chat now show the agent name instead of its internal key (e.g. `pentest`).

### Upstream sync

- **Model judge (fallback approval) token usage is reported separately (upstream 0.3.15, #191).** This upstream change was missing from 0.3.15-ko.1. Judge calls are metered as `worker=judge`, served by the new `GET /api/intercept/judge/usage`. Under the model judge switch in "System → Command interception", a card shows cumulative calls, input, output and cache read/write plus a 30-day daily bar chart.

### Security

- **Closed the remaining gaps in the #22 auth hardening.** A failed password-hash read was still folded into "no password" in token verification and in startup first-run handling, caching an empty hash. Token verification now refuses on a failed read, and startup skips env-var initialisation and setup-token issuance. `ARTEX_ADMIN_PASSWORD` initialisation is also stored once via `InsertSettingIfAbsent` and checked against the 72-byte limit. The `/setup` screen shows a retry notice instead of the form when it cannot confirm the state (the UI part of upstream #197).

## [0.3.15-ko.1] · 2026-10-12 · First Korean-edition release

The first release of the Korean edition, based on upstream ARTEX 0.3.15.

### Distribution

- **The Korean-edition Docker image is now published.** Upstream's `autumn27/artex` image was taken down from Docker Hub, which broke the `docker compose up -d` quick start. Each release tag now pushes a multi-architecture (amd64/arm64) image to `ghcr.io/jiwoochris/artex-global`, and `docker-compose.yml` pulls that image.

### Security

- **Blocked remote takeover of the first-run admin account.** An instance without a password accepted `POST /api/auth/init` unauthenticated, so whoever reached the port first could set the admin password and obtain a JWT (privately reported by Dangel). Initialisation now requires the setup token (`setup_token`) printed only on the server console at startup, and wrong attempts are rate-limited. Added `ARTEX_SETUP_TOKEN` and `ARTEX_ADMIN_PASSWORD` for unattended deployments and a token field on the `/setup` screen.
- **Default bind address is now 127.0.0.1.** The `artex -addr` default moves from `:8787` to `127.0.0.1:8787`, and `docker-compose.yml` publishes 8787 as `${ARTEX_BIND:-127.0.0.1}`. Deployments reached from other machines must pass `-addr :8787` or set `ARTEX_BIND=0.0.0.0` in `.env`. Starting without a password on a non-loopback address logs a warning.
- **Changing the password invalidates every existing token.** The JWT signing key is derived from the password hash, so a change through the UI, `reset-password.sh`, or a direct DB edit ends earlier sessions. Users must log in once after upgrading. The change-password response carries a fresh token so the current browser session continues.
- **Replaced wildcard CORS with an allowlist.** Instead of `Access-Control-Allow-Origin: *`, headers are only sent for origins in `ARTEX_CORS_ORIGINS` (default: `next dev` at `http://localhost:5173`). Also added server-side minimum password length (8) and audit logs for failed initialisation/login attempts (with client address).
- **Ported the authentication hardening from upstream ARTEX 0.3.15.** Password-setting reads now fail closed with 503 instead of reporting `initialized:false` (`authStatus`, `authInit`, `authLogin`, change-password). First-time setup is atomic via `INSERT ... ON CONFLICT DO NOTHING` ([`db.InsertSettingIfAbsent`](db/settings.go)), so the primary-key constraint protects the existing value even when the read check is unreliable. Passwords over 72 bytes are rejected with 400 instead of 500, and a database connection-pool cap (32) reduces the path where a `too many clients` error can masquerade as a read failure. Ported to match upstream commit `a951e4a` (0.3.15); only the parts not already covered by PR #20 were added.

### Localization (i18n)

- **Added switching to English, Chinese and Spanish.** Korean remains the default. Picking a language under "Display language" in Settings switches the web UI (`web/messages/{ko,en,zh,es}.json`), agent-written reports, summaries and chat replies, and server error messages and status labels together. Operator console logs and the server default follow `ARTEX_LANG` (or `language` in the config file). The agent's behavioural-instruction body is still left in its original language.
- **Forced user-facing output into Korean.** The benchmarked agent's behavioral-instruction body (the "brain") is left in its original language to preserve performance, and a code-level fixed segment (`langDirective`) instructs the agent to write only the user-facing output (vulnerability reports, fact summaries, final summaries, chat replies) in Korean. Commands, payloads, code, and raw logs are kept in their original form.
- **Translated the web UI into Korean.** Introduced `next-intl` into the Next App Router and split the strings into `web/messages/ko.json` and `web/messages/zh.json`. The original Chinese is preserved in `zh.json` so that it can be compared against upstream updates. Screen strings for the dashboard, vulnerabilities, chat, notification delivery, interception, LLM settings, and more were translated into Korean.
- **Translated the server API's user-facing errors and responses into Korean.** HTTP error and response text that is returned to the browser was replaced with Korean. Text that feeds back into the agent brain as input, however, was kept in its original language to prevent benchmark drift, and the reasoning behind each such decision is recorded in the repository's working documents.
- **Reorganized the documentation in Korean.** Created a Korean `README.md`, kept an English `README.en.md` alongside it, and preserved the original Chinese as `README.zh.md`.

### Defense and detection resources

- **Added a defense and detection guide.** A Korean guide ([`docs/defense-ko.md`](docs/defense-ko.md)) and an English version with the same content ([`docs/defense-en.md`](docs/defense-en.md)) that cover how an autonomous AI attack differs from a traditional scanner, the fingerprints (IoCs) a defender can observe, entry points and hardening, detection rules, and incident response.
- **Provides deployable detection rules.** The guide's fingerprints were turned into rules you can use directly. The host and log layer is covered by [Sigma](https://sigmahq.io) atomic and correlation rules ([`detections/sigma/`](detections/sigma/)), and the network layer by [Suricata](https://suricata.io) rules ([`detections/suricata/`](detections/suricata/)) that target the enrich prober and norma SDK WebFetch User-Agents.
- **Visualized ATT&CK coverage.** The techniques that the rules tag were organized into a MITRE ATT&CK Navigator layer ([`detections/attack/`](detections/attack/)).
- **Provides machine-readable indicators of compromise (IoCs) in standard formats.** The unique fingerprints that ARTEX emits were collected into a single CSV ([`detections/indicators/artex_indicators.csv`](detections/indicators/artex_indicators.csv)), along with a MISP event ([`detections/indicators/artex_indicators.misp.json`](detections/indicators/artex_indicators.misp.json)) carrying the same indicators that can be imported straight into a threat-intelligence platform. Indicators that a rule backs are marked with `to_ids`, while host-forensic ports are marked separately as triage clues.
- **Attached reproducible detection tests.** Eight test suites prove the rules by actually running them (Sigma structure and compilation checks, Sigma live event-matching, backend portability, SigmaHQ convention lint, Suricata load and firing, ATT&CK layer consistency, indicator-to-source matching, and MISP export ↔ CSV synchronization). A batch runner that runs them all at once and a pre-commit example were added and wired into the CI merge gate. Sigma live event-matching confirms that the rules not only compile but actually fire on malicious sample events and stay silent on benign ones, for both the atomic and the correlation rules.

### Repository hardening

- **Added security and misuse warnings and Korean legal notices.** The scope of use, notices under the Network Act and the Personal Information Protection Act, and a misuse-prohibition warning were added at the top of the README.
- **Replaced the screen preview with Korean UI screenshots.**
- **Set up a maintainer runbook and a contributing guide.** Added a runbook ([`MAINTAINING.en.md`](MAINTAINING.en.md)) for preventing upstream-sync and translation drift, and a detection-rule contribution contract ([`CONTRIBUTING.en.md`](CONTRIBUTING.en.md)). The runbook also documents the release pipeline's build assumptions and how to verify them locally without a tag.
- **Added push/PR merge-gate CI.** The upstream repository ran CI only on tag releases, but this fork runs — on every push and pull request — a Go build, static analysis (`go vet`), and unit tests ([`ci.yml`](.github/workflows/ci.yml)); a Korean UI static build ([`web.yml`](.github/workflows/web.yml)); and integrity checks for the documentation's repository-internal links and image references ([`docs.yml`](.github/workflows/docs.yml)), catching regressions introduced during localization before they merge. Integration tests that require a database are verified alongside a PostgreSQL service isolated per package. The documentation link check runs as a deterministic script ([`scripts/check-doc-links.py`](scripts/check-doc-links.py)) that does not depend on an external network, so the many relative links the multilingual documents point at one another — and the screen-preview images — cannot merge while broken. Documentation anchor (`#heading`) links are also checked against headings using the same slug rules as GitHub, catching table-of-contents and cross-reference links that silently break when heading text changes. The detection-rule suite is handled by the merge gate described in the "Defense and detection resources" section above.
- **Periodically checks external-link liveness.** External links the documents point at — the defense guide's incident-reporting channels and standards references — depend on remote server state and are flaky, so they are kept out of the merge gate; instead a non-blocking workflow ([`external-links`](.github/workflows/external-links.yml)) runs a browser-User-Agent, GET, redirect-following check ([`scripts/check-external-links.py`](scripts/check-external-links.py)) every Monday and on manual dispatch. A host that is alive but blocks the check method (bot protection, rate limiting) and upstream-inherited dead links we cannot fix (an allowlist) are not counted as failures, so it turns red only when an external link our documents curate newly breaks.
- **Established contribution and governance infrastructure.** Added bug, feature, and translation issue templates ([`.github/ISSUE_TEMPLATE/`](.github/ISSUE_TEMPLATE/)) and a pull-request template ([`PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md)), a security-vulnerability reporting policy ([`SECURITY.en.md`](SECURITY.en.md)), and a code of conduct ([`CODE_OF_CONDUCT.en.md`](CODE_OF_CONDUCT.en.md)), so that external contributors submit issues, PRs, and security reports in a consistent format.
- **Completed an English documentation layer for overseas contributors.** This repository is Korean-first, but English versions of the core documents are provided so that contributors, security researchers, and defenders who do not read Korean can reach the same information. In addition to the English `README.en.md` and the defense guide ([`docs/defense-en.md`](docs/defense-en.md)), there are English versions of the changelog ([`CHANGELOG.en.md`](CHANGELOG.en.md)), the security reporting policy ([`SECURITY.en.md`](SECURITY.en.md)), the code of conduct ([`CODE_OF_CONDUCT.en.md`](CODE_OF_CONDUCT.en.md)), the contributing guide ([`CONTRIBUTING.en.md`](CONTRIBUTING.en.md)), the maintainer runbook ([`MAINTAINING.en.md`](MAINTAINING.en.md)), the traffic-evidence design document ([`docs/finding-traffic-evidence-en.md`](docs/finding-traffic-evidence-en.md)), and the bug, feature, and translation issue templates. The Korean and English versions link to each other in their headers, so you can move to the other language from whichever one you arrive at. (The pull-request template is currently Korean-only.)

---

The upstream ARTEX project's per-version release history and contributor list can be viewed verbatim in [`CHANGELOG.zh.md`](CHANGELOG.zh.md).
