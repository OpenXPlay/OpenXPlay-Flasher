<!-- SPDX-FileCopyrightText: 2026 OpenXPlay.org -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# Publishing the beta catalog

`beta.json` is the reviewed source for the website's signed package list. Keep artifact URLs,
sizes and SHA-256 values in this file; do not add local file paths. Advance `sequence` beyond
the catalog currently published at `https://openxplay.org/flasher/beta/`. The build tool sets
the generation timestamp and writes the exact LF-terminated bytes that will be signed.

After the source change has passed CI and merged into `main`, run **Sign catalog** manually
on `main`. The job verifies the current website catalog with the Flasher's embedded public
key, checks that the new sequence increases, signs through standard input, and verifies the
result against the same public key. It uploads only `index.json` and `index.json.minisig` in
the `catalog-beta-<commit>` artifact. It does not commit or deploy them.

Download the artifact, run `oxp-catalog verify --index index.json`, and submit both unchanged
files to `OpenXPlay/OpenXPlay` under `site/public/flasher/beta/`. Preserve the index's LF
bytes. After the website PR merges and Pages deploys, download and verify the published pair.

The GitHub environment `catalog` supplies `CATALOG_SIGNING_KEY` to the signing step only.
Store the original two-line unencrypted key text produced by `oxp-catalog keygen`; a base64
wrapper or an encrypted minisign secret key is not this tool's format. A missing, malformed
or unrelated key fails the job without publishing an artifact. Do not generate a replacement
key for a catalog update: existing Flasher builds trust the existing public key.

Restrict the environment's deployment branches to `main` as well as the workflow's own
main-branch check, so modified workflows on other branches cannot request the secret.
