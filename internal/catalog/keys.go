// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

// trustedPublicKeys is deliberately unexported: neither configuration, environment variables
// nor command-line flags can change the client's trust roots. Keep current and next keys here
// during rotation. Tests in this package may replace this list.
// TODO(owner): replace this publicly known TEST KEY (RFC 8032 test 1) before release.
var trustedPublicKeys = []string{`untrusted comment: OpenXPlay catalog TEST KEY — NOT FOR RELEASE
RWQBAgMEBQYHCNdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea
`}
