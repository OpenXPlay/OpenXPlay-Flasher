// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HTTPError preserves the response status so callers can distinguish absence from a retry.
type HTTPError struct {
	URL    string
	Status int
}

func (e *HTTPError) Error() string   { return fmt.Sprintf("catalog: %s: HTTP %d", e.URL, e.Status) }
func (e *HTTPError) Temporary() bool { return e.Status == 429 || e.Status >= 500 && e.Status <= 599 }

// NewHTTPClient uses the system proxy and bounds connection, header and total transfer time.
// Callers may instead supply a client with their own RoundTripper, including a test transport.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout: 90 * time.Second, ExpectContinueTimeout: time.Second},
		Timeout: 30 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("catalog: too many redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("catalog: refusing an insecure redirect")
			}
			return nil
		},
	}
}

func get(ctx context.Context, client *http.Client, url, agent string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept-Encoding", "identity")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &HTTPError{url, res.StatusCode}
	}
	if res.ContentLength > limit {
		return nil, ErrTooLarge
	}
	return readLimited(res.Body, limit)
}
