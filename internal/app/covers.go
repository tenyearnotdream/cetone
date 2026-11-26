package app

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/vivaui/viva/gfx"
)

// Cover art. A page asks for a bitmap by URL and pixel size; the first ask
// starts a worker that reads the disk cache or the network, decodes, scales and
// publishes the result. Until then the page draws a placeholder, so a slow
// image never blocks a paint.

// coverBitmap returns the decoded art, or the zero bitmap while it loads. The
// bitmap is square.
func (a *App) coverBitmap(rawURL string, size int) gfx.Bitmap {
	return a.coverBitmapBox(rawURL, size, size)
}

// coverBitmapBox is coverBitmap for an image that is not square, such as a
// banner: the server is asked for that box, and the bitmap keeps its aspect.
func (a *App) coverBitmapBox(rawURL string, width, height int) gfx.Bitmap {
	if rawURL == "" || width <= 0 || height <= 0 || a.noRemote {
		return gfx.Bitmap{}
	}
	key := coverKey{url: rawURL, width: width, height: height}
	if bitmap, ok := a.bitmaps[key]; ok {
		return bitmap
	}
	if a.coverMisses[key] || a.coverLoading[key] {
		return gfx.Bitmap{}
	}
	a.coverLoading[key] = true
	go a.fetchCover(key)
	return gfx.Bitmap{}
}

// fetchCover loads, decodes and publishes one cover.
func (a *App) fetchCover(key coverKey) {
	data, ok := a.covers.Load(key.url, key.width)
	if !ok {
		fetched, err := downloadCover(a.client.HTTPClient(), key.url)
		if err != nil {
			a.work.push(func() {
				delete(a.coverLoading, key)
				a.coverMisses[key] = true
			})
			a.invalidate()
			return
		}
		data = fetched
		_ = a.covers.Save(key.url, key.width, data)
	}
	bitmap, err := decodeBitmap(data, key.width, key.height)
	a.work.push(func() {
		delete(a.coverLoading, key)
		if err != nil {
			a.coverMisses[key] = true
			return
		}
		bitmap.ID = nextBitmapID()
		a.bitmaps[key] = bitmap
	})
	a.invalidate()
}

// downloadCover fetches one image with a short timeout: art is decoration, and
// a stalled server must not hold a worker.
func downloadCover(client *http.Client, rawURL string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Referer", "https://music.163.com")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errUnavailable
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

var errUnavailable = &coverError{}

type coverError struct{}

func (*coverError) Error() string { return "封面地址不可用" }

// decodeBitmap turns an encoded image into the premultiplied BGRA a canvas
// draws, scaled to the requested box.
func decodeBitmap(data []byte, width, height int) (gfx.Bitmap, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return gfx.Bitmap{}, err
	}
	return bitmapFromImage(src, width, height), nil
}

// bitmapFromImage converts and scales an image into Viva's bitmap layout. The
// scaling is an area average, which is what a cover needs: no ringing around
// text in an album title.
func bitmapFromImage(src image.Image, width, height int) gfx.Bitmap {
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	width, height = max(width, 1), max(height, 1)
	out := gfx.Bitmap{
		Pix:    make([]byte, width*height*4),
		Width:  width,
		Height: height,
		Stride: width * 4,
	}
	// Normalise the source into 8-bit RGBA (non-premultiplied) once, so the
	// sampling loop reads four bytes per pixel whatever the source format.
	normalised := image.NewRGBA(image.Rect(0, 0, srcWidth, srcHeight))
	draw.Draw(normalised, normalised.Bounds(), src, bounds.Min, draw.Src)

	for y := 0; y < height; y++ {
		y0 := y * srcHeight / height
		y1 := (y + 1) * srcHeight / height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0 := x * srcWidth / width
			x1 := (x + 1) * srcWidth / width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha uint64
			var count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					offset := normalised.PixOffset(sx, sy)
					red += uint64(normalised.Pix[offset])
					green += uint64(normalised.Pix[offset+1])
					blue += uint64(normalised.Pix[offset+2])
					alpha += uint64(normalised.Pix[offset+3])
					count++
				}
			}
			if count == 0 {
				continue
			}
			r := uint8(red / count)
			g := uint8(green / count)
			b := uint8(blue / count)
			a := uint8(alpha / count)
			dst := (y*width + x) * 4
			// Premultiply, then lay the bytes out as BGRA.
			premultiply := func(c uint8) uint8 {
				return uint8(uint32(c) * uint32(a) / 255)
			}
			out.Pix[dst] = premultiply(b)
			out.Pix[dst+1] = premultiply(g)
			out.Pix[dst+2] = premultiply(r)
			out.Pix[dst+3] = a
		}
	}
	return out
}

// bitmapSeq hands out the IDs the renderer uses to tell one bitmap from
// another. A zero ID is uploaded on every frame, so each decoded cover gets one.
var bitmapSeq atomic.Uint64

func nextBitmapID() uint64 { return bitmapSeq.Add(1) }
