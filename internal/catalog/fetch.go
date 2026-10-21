// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
)

// The app never downloads packages: users download them from the catalog's links (or any other
// source) and pick the files, which are checked by SHA-256. Only the maintainer tool fetches an
// artifact, to record its size and digest in a new index.

// Progress describes hashing of one local file. Callbacks run synchronously.
type Progress struct {
	Done, Total int64
	URL         string
}

// ProgressFunc receives updates for one file.
type ProgressFunc func(Progress)

// ArtifactSize is useful to a maintainer fetching metadata without trusting Content-Length.
// It hashes at most 2 GiB plus one byte; a missing or empty body cannot become an artifact.
func ArtifactSize(ctx context.Context, client *http.Client, url string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "OpenXPlay-Flasher/catalog-tool")
	res, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, "", &HTTPError{url, res.StatusCode}
	}
	if res.ContentLength > MaxArtifactSize {
		return 0, "", ErrTooLarge
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, res.Body}, MaxArtifactSize+1))
	if n > MaxArtifactSize {
		return 0, "", ErrTooLarge
	}
	if n == 0 && err == nil {
		return 0, "", &InvalidError{"artifact", "empty download"}
	}
	return n, hex.EncodeToString(h.Sum(nil)), err
}
