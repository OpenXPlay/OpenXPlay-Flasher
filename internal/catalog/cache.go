// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Progress describes a single artifact transfer. Done includes a verified-on-completion resume
// prefix; callbacks run synchronously and must not call back into this Cache.
type Progress struct {
	Done, Total int64
	URL         string
}

// ProgressFunc receives updates for one artifact, including a completed cache hit.
type ProgressFunc func(Progress)

// Cache owns a content-addressed artifact directory. Index must be the current verified index.
// Operations are serialized so a prune or second transfer cannot remove an active partial file.
type Cache struct {
	Dir     string
	Index   *Index
	Client  *http.Client
	Version string
	mu      sync.Mutex
}

// NewCache accepts a client so all downloads can use an injected transport. A nil client uses
// proxy and timeout defaults. Set Version before downloading to include the app version in requests.
func NewCache(dir string, index *Index, client *http.Client) *Cache {
	if client == nil {
		client = NewHTTPClient()
	}
	return &Cache{Dir: dir, Index: index, Client: client, Version: "dev"}
}

func (c *Cache) path(a Artifact) string { return filepath.Join(c.Dir, "artifacts", a.SHA256, a.Name) }

// Ensure returns only a size- and hash-verified file, using an existing file when possible.
// Cancellation and interrupted bodies keep the partial file; a bad hash discards it.
func (c *Cache) Ensure(ctx context.Context, a Artifact, progress ProgressFunc) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := a.Validate(); err != nil {
		return "", err
	}
	if err := c.remember(ctx); err != nil {
		return "", err
	}
	path := c.path(a)
	if err := VerifyContext(ctx, path, a); err == nil {
		if progress != nil {
			progress(Progress{Done: a.Size, Total: a.Size})
		}
		return path, nil
	} else if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	part := path + ".part"
	if err := preparePart(ctx, part, path, a); err != nil {
		return "", err
	}
	if err := VerifyContext(ctx, path, a); err == nil {
		if progress != nil {
			progress(Progress{Done: a.Size, Total: a.Size})
		}
		return path, nil
	}
	var failures []error
	for _, url := range a.URLs {
		// A bad resume prefix gets one fresh attempt at the same mirror. Other integrity
		// failures move straight to the next mirror; transient failures get three attempts.
		freshRetry := false
		for attempt := 0; attempt < 3; attempt++ {
			resumed, retry, err := c.download(ctx, url, part, a, progress)
			if err == nil {
				if err := os.Rename(part, path); err != nil {
					return "", err
				}
				return path, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			failures = append(failures, err)
			if errors.Is(err, ErrIntegrity) {
				if err := os.Remove(part); err != nil && !os.IsNotExist(err) {
					return "", err
				}
				if resumed && !freshRetry {
					freshRetry = true
					attempt--
					continue
				}
				break
			}
			if !retry || attempt == 2 {
				break
			}
			if err := backoff(ctx, time.Duration(1<<attempt)*100*time.Millisecond); err != nil {
				return "", err
			}
		}
	}
	return "", errors.Join(failures...)
}

func preparePart(ctx context.Context, part, path string, a Artifact) error {
	st, err := os.Lstat(part)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return &InvalidError{"partial file", "not a regular file"}
	}
	if st.Size() == a.Size {
		if err := VerifyContext(ctx, part, a); err == nil {
			return os.Rename(part, path)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if st.Size() >= a.Size {
		return os.Remove(part)
	}
	return nil
}

func backoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// download hashes both the partial prefix and incoming bytes. A Range answer must describe
// exactly the requested remainder; a 200 answer starts again at byte zero.
func (c *Cache) download(ctx context.Context, url, part string, a Artifact, progress ProgressFunc) (resumed, retry bool, err error) {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, false, err
	}
	if !st.Mode().IsRegular() {
		return false, false, &InvalidError{"partial file", "not a regular file"}
	}
	offset := st.Size()
	if offset >= a.Size {
		return true, false, &IntegrityError{a.Name, "invalid partial file size"}
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return offset > 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("User-Agent", "OpenXPlay-Flasher/"+c.Version)
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	res, err := c.Client.Do(req)
	if err != nil {
		return offset > 0, true, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		return true, false, &IntegrityError{a.Name, "server refused resume range"}
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		e := &HTTPError{url, res.StatusCode}
		return offset > 0, e.Temporary(), e
	}
	if res.StatusCode == http.StatusPartialContent {
		want := fmt.Sprintf("bytes %d-%d/%d", offset, a.Size-1, a.Size)
		if offset == 0 || res.Header.Get("Content-Range") != want {
			return offset > 0, false, &IntegrityError{a.Name, "invalid Content-Range"}
		}
	} else if offset > 0 {
		if err := f.Truncate(0); err != nil {
			return false, false, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return false, false, err
		}
		offset = 0
		h.Reset()
	}
	if res.ContentLength > a.Size-offset {
		return offset > 0, false, &IntegrityError{a.Name, "body exceeds expected size"}
	}
	done := offset
	if progress != nil {
		progress(Progress{done, a.Size, url})
	}
	buf := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return offset > 0, false, err
		}
		// Read at most one byte beyond the remainder and never write that extra byte.
		n, readErr := res.Body.Read(buf[:min(int64(len(buf)), a.Size-done+1)])
		if int64(n) > a.Size-done {
			return offset > 0, false, &IntegrityError{a.Name, "body exceeds expected size"}
		}
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return offset > 0, false, err
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(Progress{done, a.Size, url})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return offset > 0, true, readErr
		}
	}
	if done != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return offset > 0, false, &IntegrityError{a.Name, "wrong size or SHA-256"}
	}
	if err := ctx.Err(); err != nil {
		return offset > 0, false, err
	}
	if err := f.Sync(); err != nil {
		return offset > 0, false, err
	}
	return offset > 0, false, f.Close()
}

// Import accepts any local name. It matches by bytes against the current index, then copies
// into a separate temporary file and verifies again to catch changes during the copy.
func (c *Cache) Import(ctx context.Context, path string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Index == nil {
		return "", &InvalidError{"index", "no current index"}
	}
	if err := c.remember(ctx); err != nil {
		return "", err
	}
	size, sum, err := Hash(ctx, path)
	if err != nil {
		return "", err
	}
	for _, a := range c.Index.Artifacts() {
		if a.Size != size || a.SHA256 != sum {
			continue
		}
		dest := c.path(a)
		if err := VerifyContext(ctx, dest, a); err == nil {
			return dest, nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		in, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer in.Close()
		out, err := os.CreateTemp(filepath.Dir(dest), ".import-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(out.Name())
		defer out.Close()
		n, err := io.Copy(out, io.LimitReader(contextReader{ctx, in}, a.Size+1))
		if err != nil {
			return "", err
		}
		if n != a.Size {
			return "", &IntegrityError{a.Name, "file changed during import"}
		}
		if err := out.Sync(); err != nil {
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if err := VerifyContext(ctx, out.Name(), a); err != nil {
			return "", err
		}
		if err := os.Rename(out.Name(), dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", ErrNoMatch
}

// parseCachePath recognizes only the cache's own SHA-256/name layout, not arbitrary paths.
func parseCachePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return rel, len(parts) == 2 && digest.MatchString(parts[0])
}

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
