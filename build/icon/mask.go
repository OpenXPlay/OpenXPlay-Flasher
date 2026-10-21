// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build ignore

// mask cuts the app icon shape out of full-bleed square artwork: the artwork is scaled into a
// superellipse ("squircle") body centred on a transparent canvas. make icons runs it twice: with
// the macOS grid margin (an 824 px body on 1024) and without one (Windows and in-app icons).
//
//	go run build/icon/mask.go -in art.png -out icon.png [-size 1024] [-margin 100]
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

func main() {
	in := flag.String("in", "", "full-bleed square artwork (PNG)")
	out := flag.String("out", "", "output PNG")
	size := flag.Int("size", 1024, "output canvas size in pixels")
	margin := flag.Int("margin", 0, "transparent margin around the body in pixels")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	art, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	body := *size - 2**margin
	scaled := resize(art, body)
	dst := image.NewNRGBA(image.Rect(0, 0, *size, *size))
	for y := 0; y < body; y++ {
		for x := 0; x < body; x++ {
			c := scaled.NRGBAAt(x, y)
			c.A = uint8(math.Round(float64(c.A) * coverage(x, y, body)))
			dst.SetNRGBA(x+*margin, y+*margin, c)
		}
	}
	o, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(o, dst); err != nil {
		log.Fatal(err)
	}
	if err := o.Close(); err != nil {
		log.Fatal(err)
	}
}

// coverage is the share of pixel (x, y) inside the superellipse |u|^5 + |v|^5 <= 1 that fills a
// body of n pixels, from 4 x 4 samples, so the edge is antialiased.
func coverage(x, y, n int) float64 {
	const samples = 4
	inside := 0
	for sy := 0; sy < samples; sy++ {
		for sx := 0; sx < samples; sx++ {
			u := (float64(x)+(float64(sx)+0.5)/samples)/float64(n)*2 - 1
			v := (float64(y)+(float64(sy)+0.5)/samples)/float64(n)*2 - 1
			if math.Pow(math.Abs(u), 5)+math.Pow(math.Abs(v), 5) <= 1 {
				inside++
			}
		}
	}
	return float64(inside) / samples / samples
}

// resize scales src to n x n by averaging the source area under each target pixel (exact for
// downscaling, which is the only use here).
func resize(src image.Image, n int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		y0, y1 := float64(y)*sh/float64(n), float64(y+1)*sh/float64(n)
		for x := 0; x < n; x++ {
			x0, x1 := float64(x)*sw/float64(n), float64(x+1)*sw/float64(n)
			var r, g, bl, a, w float64
			for sy := int(y0); float64(sy) < y1; sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); float64(sx) < x1; sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					c := color.NRGBAModel.Convert(src.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA)
					k := wx * wy
					r += float64(c.R) * k
					g += float64(c.G) * k
					bl += float64(c.B) * k
					a += float64(c.A) * k
					w += k
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: uint8(r/w + 0.5), G: uint8(g/w + 0.5), B: uint8(bl/w + 0.5), A: uint8(a/w + 0.5)})
		}
	}
	return dst
}
