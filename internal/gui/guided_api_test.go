// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type guidedDevice struct {
	*installDevice
	mu                                        sync.Mutex
	commands                                  []string
	sizes                                     map[string]int
	credentialViews                           [][]byte
	reportedSerial                            string
	badSize                                   bool
	private, restarted, failWrite, failVerify bool
}

func (d *guidedDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	d.mu.Lock()
	if cmd == "cat /proc/cmdline" && d.reportedSerial != "" {
		serial := d.reportedSerial
		d.mu.Unlock()
		return adb.Result{Stdout: []byte("androidboot.serialno=" + serial)}, nil
	}
	if strings.Contains(cmd, "/var/lib/openxplay/mfi") || strings.HasPrefix(cmd, "rm -rf /var/lib/openxplay/mfi") {
		d.commands = append(d.commands, cmd)
		defer d.mu.Unlock()
		if strings.Contains(cmd, "stat -c '%s'") {
			keySize := d.sizes["identity.pk8"]
			if d.badSize && keySize > 0 {
				keySize++
			}
			return adb.Result{Stdout: []byte(fmt.Sprintf("%d\r\n%d\r\n", keySize, d.sizes["certificate.p7b"]))}, nil
		}
		if d.failWrite {
			return adb.Result{}, errors.New("private-error-must-not-escape")
		}
		if strings.Contains(cmd, "mkdir -m 700") {
			d.private = true
		}
		if strings.Contains(cmd, "systemctl restart openxplay-relay.service") {
			if !strings.Contains(cmd, "chmod 600") || !strings.Contains(cmd, "chown 0:0") || !strings.Contains(cmd, "mv -f") {
				return adb.Result{}, errors.New("missing explicit modes or atomic replace")
			}
			d.restarted = true
		}
		return adb.Result{}, nil
	}
	d.mu.Unlock()
	if strings.Contains(cmd, "systemctl is-active openxplay-relay.service") {
		d.mu.Lock()
		fail := d.failVerify
		d.mu.Unlock()
		if fail {
			return adb.Result{ExitCode: 1, Stdout: []byte("inactive\r\n")}, nil
		}
	}
	return d.installDevice.Shell(ctx, cmd)
}
func (d *guidedDevice) Push(_ context.Context, path string, data []byte, mode fs.FileMode) error {
	if !strings.HasPrefix(path, "/var/lib/openxplay/mfi/") {
		return d.installDevice.Push(context.Background(), path, data, mode)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.private || mode != 0o600 || !strings.HasPrefix(path, "/var/lib/openxplay/mfi/.import-") {
		return errors.New("unsafe credential staging")
	}
	if len(data) == 0 || data[0] != 0x30 {
		return errors.New("identity payload must be DER")
	}
	d.credentialViews = append(d.credentialViews, data)
	d.sizes[filepath.Base(path)] = len(data)
	return nil
}
func (d *guidedDevice) OpenTCP(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	if port != 6975 {
		return nil, errors.New("wrong device TCP port")
	}
	local, remote := net.Pipe()
	go func() {
		defer remote.Close()
		stop := context.AfterFunc(ctx, func() { remote.Close() })
		defer stop()
		_, _ = io.Copy(remote, remote)
	}()
	return local, nil
}

type guidedNative struct {
	*fakeNative
	identity         []string
	artifacts        []string
	artifact, backup string
}

func (n *guidedNative) OpenIdentity() ([]string, error)   { return n.identity, nil }
func (n *guidedNative) OpenArtifacts() ([]string, error)  { return n.artifacts, nil }
func (n *guidedNative) OpenArtifact() (string, error)     { return n.artifact, nil }
func (n *guidedNative) OpenBackupFolder() (string, error) { return n.backup, nil }
func guidedFixture(t *testing.T) (fixture, *app.Service, *guidedDevice, *guidedNative) {
	t.Helper()
	f, svc, _, dev, _ := installFixture(t, true)
	d := &guidedDevice{installDevice: dev, sizes: map[string]int{}}
	svc.SetADB(oneDevice{d})
	n := &guidedNative{fakeNative: &fakeNative{}}
	f.handler = Handler(Deps{Service: svc, Hub: f.hub, Settings: f.settings, Native: n})
	installState(t, f)
	return f, svc, d, n
}
func jsonBody(v any) string { b, _ := json.Marshal(v); return string(b) }
func identityFiles(t *testing.T) []string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	content := struct{ Type asn1.ObjectIdentifier }{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}}
	signed, err := asn1.Marshal(struct {
		Version int
		Digests []asn1.RawValue `asn1:"set"`
		Content any
		Signers []asn1.RawValue `asn1:"set"`
	}{1, []asn1.RawValue{}, content, []asn1.RawValue{}})
	if err != nil {
		t.Fatal(err)
	}
	p7b, err := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}, asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: signed}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "identity.pk8"), filepath.Join(dir, "certificate.p7b")}
	for i, data := range [][]byte{pk8, p7b} {
		if err = os.WriteFile(paths[i], data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}
func TestIdentityRoutesAndSecrecy(t *testing.T) {
	f, svc, d, n := guidedFixture(t)
	paths := identityFiles(t)
	n.identity = paths[:1]
	if rec := f.do("POST", "/api/identity/pick", `{}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "identity.pk8") {
		t.Fatalf("picker: %d", rec.Code)
	}
	if rec := f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": paths[:1]})); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "identity-missing" || !strings.Contains(rec.Body.String(), "certificate.p7b") {
		t.Fatalf("missing file: %d %s", rec.Code, rec.Body)
	}
	zipPath := filepath.Join(t.TempDir(), "empty.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	zw.Close()
	zf.Close()
	if rec := f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": []string{zipPath}})); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "identity-missing" {
		t.Fatalf("empty ZIP: %d %s", rec.Code, rec.Body)
	}
	d.mu.Lock()
	if len(d.sizes) != 0 {
		t.Fatal("refusal pushed credentials")
	}
	d.mu.Unlock()
	if rec := f.do("GET", "/api/identity", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"identity":"absent"`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	rec := f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": []string{paths[1], paths[0]}}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"identity":"present"`) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	d.mu.Lock()
	if !d.restarted {
		t.Fatal("relay not restarted")
	}
	d.mu.Unlock()
	if svc.InstallLog() != "" {
		t.Fatal("identity entered installation log")
	}
	if strings.Contains(rec.Body.String(), paths[0]) || strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Fatal("identity data entered response")
	}
	d.mu.Lock()
	d.failWrite = true
	d.mu.Unlock()
	rec = f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": paths}))
	if rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "identity-write" || strings.Contains(rec.Body.String(), "private-error") {
		t.Fatalf("private error escaped: %d", rec.Code)
	}
	for _, path := range []string{"/api/identity/pick", "/api/identity/import"} {
		if rec := f.do("POST", path, ""); rec.Code != 415 {
			t.Fatalf("unguarded %s: %d", path, rec.Code)
		}
	}
}
func TestUSBAdminRoutesLifecycle(t *testing.T) {
	f, svc, _, n := guidedFixture(t)
	rec := f.do("POST", "/api/admin/forward/start", `{}`)
	var forward app.ForwardState
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &forward) != nil || !forward.Active {
		t.Fatalf("forward: %d %s", rec.Code, rec.Body)
	}
	u, err := url.Parse(forward.Address)
	if err != nil || u.Scheme != "https" || u.Hostname() != "127.0.0.1" {
		t.Fatal("forward is not loopback HTTPS")
	}
	c, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = c.Write([]byte("usb-fixture")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 11)
	if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "usb-fixture" {
		t.Fatalf("tunnel echo: %q %v", buf, err)
	}
	if rec = f.do("GET", "/api/identity", ""); rec.Code != 200 {
		t.Fatal("tunnel blocked device reads")
	}
	if rec = f.do("POST", "/api/open", jsonBody(map[string]string{"url": forward.Address})); rec.Code != 204 || len(n.opened) != 1 {
		t.Fatal("active forward cannot open in native browser")
	}
	if rec = f.do("POST", "/api/open", `{"url":"https://127.0.0.1:1/"}`); rec.Code != 403 {
		t.Fatal("arbitrary local URL allowed")
	}
	if rec = f.do("GET", "/api/admin/forward", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), forward.Address) {
		t.Fatal("forward status missing")
	}
	if rec = f.do("POST", "/api/admin/forward/stop", `{}`); rec.Code != 200 || strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatal("forward failed to stop")
	}
	if _, err = c.Read(buf); err == nil {
		t.Fatal("stop retained active tunnel")
	}
	if rec = f.do("POST", "/api/open", jsonBody(map[string]string{"url": forward.Address})); rec.Code != 403 {
		t.Fatal("stopped URL remains openable")
	}
	empty := newFixture(t)
	if rec = empty.do("POST", "/api/admin/forward/start", `{}`); rec.Code != 409 {
		t.Fatal("forward without device was accepted")
	}
	if rec = empty.do("POST", "/api/admin/forward/start", ""); rec.Code != 415 {
		t.Fatal("forward write has no JSON guard")
	}
	if svc.ForwardStatus().Active {
		t.Fatal("stopped forward still active")
	}
}

func TestIdentitySizeReadbackMustMatchBeforeSuccess(t *testing.T) {
	f, svc, d, _ := guidedFixture(t)
	paths := identityFiles(t)
	d.mu.Lock()
	d.badSize = true
	d.mu.Unlock()
	rec := f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": paths}))
	if rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "identity-status" {
		t.Fatalf("wrong size readback accepted: %d", rec.Code)
	}
	if svc.InstallSnapshot().NextSteps.Identity == "present" || f.hub.State().Install.NextSteps.Identity == "present" {
		t.Fatal("failed readback published Identity added")
	}
	d.mu.Lock()
	d.badSize = false
	d.mu.Unlock()
	if rec = f.do("POST", "/api/identity/import", jsonBody(map[string]any{"paths": paths})); rec.Code != 200 || svc.InstallSnapshot().NextSteps.Identity != "present" {
		t.Fatal("identity retry failed")
	}
}

func TestForwardStopsWhenDeviceLeaves(t *testing.T) {
	f, svc, bus, dev, _ := installFixture(t, true)
	d := &guidedDevice{installDevice: dev, sizes: map[string]int{}}
	svc.SetADB(oneDevice{d})
	installState(t, f)
	if rec := f.do("POST", "/api/admin/forward/start", `{}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	bus.Set()
	svc.Rescan(t.Context())
	if svc.ForwardStatus().Active {
		t.Fatal("device departure left forward active")
	}
}

func TestForwardRejectsReusedTransportForAnotherBoard(t *testing.T) {
	f, svc, d, _ := guidedFixture(t)
	rec := f.do("POST", "/api/admin/forward/start", `{}`)
	var forward app.ForwardState
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &forward) != nil {
		t.Fatal("fixture forward")
	}
	d.mu.Lock()
	d.reportedSerial = "another-board"
	d.mu.Unlock()
	u, _ := url.Parse(forward.Address)
	c, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	var buf [1]byte
	if _, err = c.Read(buf[:]); err == nil {
		t.Fatal("another board received browser traffic")
	}
	deadline := time.Now().Add(time.Second)
	for svc.ForwardStatus().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.ForwardStatus().Active {
		t.Fatal("serial mismatch left forward active")
	}
}

func TestVerificationRetryRouteNeverRepeatsWrites(t *testing.T) {
	f, svc, d, _ := guidedFixture(t)
	deb, _ := relayFiles(t)
	if rec := f.do("POST", "/api/install/retry", `{}`); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "retry-unavailable" {
		t.Fatal("retry without a failed job accepted")
	}
	if rec := f.do("POST", "/api/install/packages", bodyPath(deb)); rec.Code != 200 {
		t.Fatal("fixture package")
	}
	d.mu.Lock()
	d.failVerify = true
	d.mu.Unlock()
	if rec := f.do("POST", "/api/install/start", `{"choice":"relay"}`); rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	state, err := svc.InstallWait(ctx)
	if err != nil || state.Job.Status != "failed" || !state.Job.Retryable {
		t.Fatal("verification failure did not offer a safe retry")
	}
	started := state.Job.Started
	if rec := f.do("POST", "/api/install/retry", jsonBody(map[string]any{"started": started.Add(-time.Second)})); rec.Code != 409 {
		t.Fatal("stale job retry accepted")
	}
	d.mu.Lock()
	d.failVerify = false
	d.mu.Unlock()
	if rec := f.do("POST", "/api/install/retry", jsonBody(map[string]any{"started": started})); rec.Code != 202 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	state, err = svc.InstallWait(ctx)
	if err != nil || state.Job.Status != "succeeded" {
		t.Fatal("verification retry failed")
	}
	d.installDevice.mu.Lock()
	pushes := d.installDevice.pushes
	d.installDevice.mu.Unlock()
	if pushes != 1 || strings.Contains(svc.InstallLog(), "openxplay-relay-update --sha256") {
		t.Fatal("verification retry repeated a write")
	}
	for _, step := range state.Job.Steps {
		if step.Action != "verify" && step.Action != "installed" {
			t.Fatal("retry contains a write or reboot")
		}
	}
}

type guiCatalog struct {
	index   *catalog.Index
	err     error
	offline bool
	client  *http.Client
}

func (c *guiCatalog) Fetch(ctx context.Context, channel string, mode catalog.Mode) (*catalog.Result, error) {
	if c.client != nil {
		for _, name := range []string{"index.json", "index.json.minisig"} {
			req, err := http.NewRequestWithContext(ctx, "GET", "https://fixture.invalid/flasher/"+channel+"/"+name, nil)
			if err != nil {
				return nil, err
			}
			res, err := c.client.Do(req)
			if err != nil {
				return nil, err
			}
			res.Body.Close()
		}
	}
	return &catalog.Result{Index: c.index, Verified: true}, c.err
}
func (c *guiCatalog) LoadCached(context.Context, string, catalog.Mode) (*catalog.Result, error) {
	return &catalog.Result{Index: c.index, Verified: true}, c.err
}
func (c *guiCatalog) Offline() bool { return c.offline }

type guiRoundTrip func(*http.Request) (*http.Response, error)

func (f guiRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func digestBytes(b []byte) string                                        { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func TestCatalogPrepareArtifactRoutes(t *testing.T) {
	f, svc, _, n := guidedFixture(t)
	deb, manifest := relayFiles(t)
	files := map[string][]byte{}
	artifacts := []catalog.Artifact{}
	for i, path := range []string{deb, manifest} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		files["/"+name] = data
		role := []string{"deb", "manifest"}[i]
		artifacts = append(artifacts, catalog.Artifact{Role: role, Name: name, Size: int64(len(data)), SHA256: digestBytes(data), URLs: []string{"https://fixture.invalid/" + name}})
	}
	source := &guiCatalog{index: &catalog.Index{Schema: catalog.Schema, Channel: "beta", Sequence: 7, Generated: "2026-10-21T00:00:00Z", Devices: map[string]catalog.Device{"ufi003": {Recommended: map[string]string{"relay": "0.1.5~beta.1"}, Layers: []catalog.Layer{{Layer: "relay", Version: "0.1.5~beta.1", Artifacts: artifacts}}}}}}

	requests := 0
	client := &http.Client{Transport: guiRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "fixture.invalid" || r.URL.Path != "/flasher/beta/index.json" && r.URL.Path != "/flasher/beta/index.json.minisig" {
			t.Errorf("non-catalog network request: %s", r.URL)
			return nil, errors.New("packages must never be fetched")
		}
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("catalog fixture")), Header: http.Header{}}, nil
	})}
	source.client = client
	previousClient := http.DefaultClient
	previousTransport := http.DefaultTransport
	http.DefaultClient, http.DefaultTransport = client, client.Transport
	t.Cleanup(func() { http.DefaultClient, http.DefaultTransport = previousClient, previousTransport })
	dir := t.TempDir()

	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), source)
	if rec := f.do("GET", "/api/catalog", ""); rec.Code != 200 {
		t.Fatal("catalog status route")
	}
	if rec := f.do("POST", "/api/catalog/fetch", `{"channel":"beta"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sequence":7`) {
		t.Fatalf("fetch: %d %s", rec.Code, rec.Body)
	}

	if requests != 2 {
		t.Fatalf("catalog requests = %d", requests)
	}
	rec := f.do("POST", "/api/install/prepare", `{"choice":"relay"}`)
	if rec.Code != 200 {
		t.Fatalf("waiting Prepare: %d %s", rec.Code, rec.Body)
	}
	var waiting app.InstallState
	if err := json.Unmarshal(rec.Body.Bytes(), &waiting); err != nil {
		t.Fatal(err)
	}
	for _, c := range waiting.Choices {
		if c.ID == "relay" {
			if c.Ready || len(c.Needs[0].Artifacts) != 2 {
				t.Fatal("waiting rows missing")
			}
			for _, a := range c.Needs[0].Artifacts {
				if len(a.URLs) != 2 || a.Verified || a.Status != "waiting" {
					t.Fatalf("waiting row %+v", a)
				}
			}
		}
	}
	if requests != 2 {
		t.Fatal("Prepare fetched a package")
	}
	wrong := filepath.Join(t.TempDir(), "wrong")
	os.WriteFile(wrong, []byte("wrong"), 0o600)
	if rec := f.do("POST", "/api/artifacts/import", jsonBody(map[string]string{"layer": "relay", "role": "deb", "path": wrong})); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "unmatched-file" {
		t.Fatal("wrong artifact accepted")
	}

	if rec := f.do("POST", "/api/artifacts/retry", `{}`); rec.Code != 404 {
		t.Fatal("download retry API still exists")
	}
	for _, a := range artifacts {
		filename := filepath.Join(t.TempDir(), "any-name")
		if err := os.WriteFile(filename, files["/"+a.Name], 0o600); err != nil {
			t.Fatal(err)
		}
		n.artifact = filename
		if rec := f.do("POST", "/api/artifacts/pick", jsonBody(map[string]string{"layer": "relay", "role": a.Role})); rec.Code != 200 {
			t.Fatalf("artifact picker: %d %s", rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/api/install/prepare"} {
		body := `{"choice":"relay"}`
		if strings.Contains(path, "retry") {
			body = `{"layer":"relay","role":"deb"}`
		}
		if rec := f.do("POST", path, body); rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := f.do("GET", "/api/artifacts", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"verified":true`) {
		t.Fatalf("artifact progress: %d %s", rec.Code, rec.Body)
	}

	if requests != 2 {
		t.Fatal("picking files or preparing used the network")
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !os.IsNotExist(err) {
		t.Fatalf("picked files copied into cache: %v", err)
	}
	source.offline = true
	if rec := f.do("POST", "/api/catalog/fetch", `{}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "cached/offline") {
		t.Fatal("offline catalog missing")
	}
	source.err = errors.New("fixture unavailable")
	if rec := f.do("POST", "/api/catalog/fetch", `{}`); rec.Code == 200 {
		t.Fatal("catalog failure accepted")
	}
	if rec := f.do("GET", "/api/catalog", ""); !strings.Contains(rec.Body.String(), "fixture unavailable") {
		t.Fatal("catalog failure reason missing")
	}
}

func TestBackupFolderAndImportRoutes(t *testing.T) {
	f, svc, _, n := guidedFixture(t)
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	svc.ConfigureStorage(filepath.Join(dir, "data"), cacheDir, filepath.Join(dir, "backups"), nil)
	p := svc.Profiles()[0]
	if p.ID != "ufi003" {
		t.Fatal("fixture profile")
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for name, m := range p.Base.NeverWrite {
		p.Base.Protected[name] = []int64{64}
		w, err := zw.Create(m.Member)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(bytes.Repeat([]byte{44}, 64))
	}
	zw.Close()
	pin := p.Base.Archives["base-generic"]
	pin.Size = int64(archive.Len())
	pin.SHA256 = digestBytes(archive.Bytes())
	p.Base.Archives["base-generic"] = pin
	generic := filepath.Join(t.TempDir(), "renamed.zip")
	os.WriteFile(generic, archive.Bytes(), 0o600)
	if _, err := svc.ImportFile(t.Context(), generic); err != nil {
		t.Fatal(err)
	}
	n.backup = t.TempDir()
	for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
		os.WriteFile(filepath.Join(n.backup, name+".bin"), bytes.Repeat([]byte{99}, 64), 0o600)
	}
	if rec := f.do("POST", "/api/backups/pick", `{"serial":"fixture-board"}`); rec.Code != 200 {
		t.Fatalf("backup picker: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/backups/import", jsonBody(map[string]string{"serial": "fixture-board", "dir": n.backup})); rec.Code != 200 {
		t.Fatalf("backup import: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/backups/fixture-board/verify", `{}`); rec.Code != 200 {
		t.Fatalf("backup verify: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/backups/fixture-board/show-folder", `{}`); rec.Code != 204 || len(n.opened) != 1 || n.opened[0] != filepath.Join(dir, "backups", "ufi003-fixture-board") {
		t.Fatal("wrong backup reveal folder")
	}
	if rec := f.do("POST", "/api/backups/other-board/show-folder", `{}`); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "backup-folder" {
		t.Fatal("missing folder accepted")
	}
	os.WriteFile(filepath.Join(n.backup, "fsg.bin"), []byte{1}, 0o600)
	if rec := f.do("POST", "/api/backups/import", jsonBody(map[string]string{"serial": "fixture-board", "dir": n.backup})); rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "backup-size" || !strings.Contains(rec.Body.String(), "fsg") {
		t.Fatalf("invalid import: %d %s", rec.Code, rec.Body)
	}
}

type stockCheckDevice struct {
	*installDevice
	properties map[string]string
	commands   []string
}

func (d *stockCheckDevice) Serial() string { return "fixture-board" }
func (d *stockCheckDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	d.commands = append(d.commands, cmd)
	if !strings.HasPrefix(cmd, "getprop ") {
		return adb.Result{}, errors.New("stock designation must only read model properties")
	}
	return adb.Result{Stdout: []byte(d.properties[strings.TrimPrefix(cmd, "getprop ")] + "\r\n")}, nil
}
func TestDesignationRouteConfirmsOnlyExplicitDevice(t *testing.T) {
	f, svc, bus, dev, _ := installFixture(t, true)
	stock := &stockCheckDevice{installDevice: dev, properties: map[string]string{}}
	for key, value := range svc.Profiles()[0].Base.Stock.Properties {
		stock.properties[key] = value
	}
	stock.properties["ro.serialno"] = "fixture-board"
	stock.properties["ro.build.version.release"] = "4.4.4"
	svc.SetADB(oneDevice{stock})
	bus.Set(usb.DeviceInfo{VendorID: 0x05c6, ProductID: 0x9091, Bus: 1, Address: 8, Path: []int{4}, Serial: "fixture-board", Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 1}}})
	svc.Rescan(t.Context())
	installState(t, f)
	if len(stock.commands) != 0 {
		t.Fatal("ignored adb device was opened without designation")
	}
	stock.properties["ro.product.model"] = "another-model"
	rec := f.do("POST", "/api/devices/designate-stock", `{"location":"1-4"}`)
	if rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "stock-mismatch" || !strings.Contains(rec.Body.String(), "another-model") {
		t.Fatalf("mismatched designation: %d %s", rec.Code, rec.Body)
	}
	stock.properties["ro.product.model"] = svc.Profiles()[0].Base.Stock.Properties["ro.product.model"]
	rec = f.do("POST", "/api/devices/designate-stock", `{"location":"1-4"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"stock"`) {
		t.Fatalf("stock designation: %d %s", rec.Code, rec.Body)
	}
	if dev.pushes != 0 {
		t.Fatal("designation wrote to the device")
	}
	if rec = f.do("POST", "/api/devices/designate-stock", `{"location":"1-99"}`); rec.Code != 409 {
		t.Fatal("wrong USB location accepted")
	}
}
