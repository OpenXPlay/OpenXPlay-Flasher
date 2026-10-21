// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

// trustedPublicKeys is deliberately unexported: neither configuration, environment variables
// nor command-line flags can change the client's trust roots. Keep current and next keys here
// during rotation. Tests in this package may replace this list.
var trustedPublicKeys = []string{`untrusted comment: OpenXPlay catalog public key
RWRp4I3XTejnpqQ7hsqb+8rgbHRGz+eRqgXTBFcYCROKsl10+BUfQT4s
`}
