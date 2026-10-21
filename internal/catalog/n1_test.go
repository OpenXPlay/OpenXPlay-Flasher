// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import "testing"

func TestN1CatalogSharedRelay(t *testing.T) {
	i := testIndex()
	l := i.Devices["ufi003"].Layers[0]
	l.Layer = "relay"
	l.Version = "1.0"
	l.Artifacts[0].Role = "deb"
	shared := Device{Recommended: map[string]string{"relay": "1.0"}, Layers: []Layer{l}}
	i.Devices["ufi003"] = shared
	i.Devices["phicomm-n1"] = shared
	if _, err := Decode(indexBytes(t, i)); err != nil {
		t.Fatal(err)
	}
}
