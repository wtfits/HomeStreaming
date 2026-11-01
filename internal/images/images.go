// Package images stores extracted artwork and serves resized variants.
package images

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decode local PNG artwork
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode local WebP artwork
)

// Store manages <data>/images and <data>/cache/images.
type Store struct {
	DataDir string
	mu      sync.Mutex
}

// ExtractedPath returns where an extracted image of an item lives.
func (s *Store) ExtractedPath(itemID, kind string) string {
	return filepath.Join(s.DataDir, "images", itemID, kind+".jpg")
}

// Rel converts an absolute extracted path to a data-dir relative one.
func (s *Store) Rel(abs string) string {
	if r, err := filepath.Rel(s.DataDir, abs); err == nil {
		return filepath.ToSlash(r)
	}
	return abs
}

// Resolve returns the absolute path for a stored image path (local images are
// stored absolute, extracted ones relative to the data dir).
func (s *Store) Resolve(p, source string) string {
	if source == "extracted" && !filepath.IsAbs(p) {
		return filepath.Join(s.DataDir, filepath.FromSlash(p))
	}
	return p
}

// Variant returns the bytes of a JPEG of src resized to fit w×h (0 = keep),
// using an on-disk cache keyed by item/kind/size/source mtime. The returned
// etag identifies the content.
func (s *Store) Variant(itemID, kind, src string, w, h int) (data []byte, etag string, err error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, "", err
	}
	tag := etagFor(src, st.ModTime().UnixNano(), st.Size(), w, h)
	if w <= 0 && h <= 0 {
		data, err = os.ReadFile(src)
		if err != nil {
			return nil, "", err
		}
		if !isJPEG(data) {
			data, err = reencode(data, 0, 0)
		}
		return data, tag, err
	}
	cache := filepath.Join(s.DataDir, "cache", "images", fmt.Sprintf("%s-%s-%dx%d-%s.jpg", itemID, kind, w, h, tag[:8]))
	if b, err := os.ReadFile(cache); err == nil {
		return b, tag, nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, "", err
	}
	out, err := reencode(raw, w, h)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(cache), 0o755)
	_ = os.WriteFile(cache, out, 0o644)
	return out, tag, nil
}

func etagFor(src string, mtime, size int64, w, h int) string {
	h1 := sha1.New()
	h1.Write([]byte(src + "|" + strconv.FormatInt(mtime, 10) + "|" + strconv.FormatInt(size, 10) + "|" + strconv.Itoa(w) + "x" + strconv.Itoa(h)))
	return hex.EncodeToString(h1.Sum(nil))[:16]
}

func isJPEG(b []byte) bool { return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 }

func reencode(raw []byte, w, h int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	tw, th := fit(sw, sh, w, h)
	if tw != sw || th != sh {
		dst := image.NewRGBA(image.Rect(0, 0, tw, th))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fit scales (sw,sh) to fit inside (w,h) without upscaling; 0 = unconstrained.
func fit(sw, sh, w, h int) (int, int) {
	if sw == 0 || sh == 0 {
		return sw, sh
	}
	scale := 1.0
	if w > 0 && sw > w {
		scale = float64(w) / float64(sw)
	}
	if h > 0 && float64(sh)*scale > float64(h) {
		scale = float64(h) / float64(sh)
	}
	tw, th := int(float64(sw)*scale+0.5), int(float64(sh)*scale+0.5)
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	return tw, th
}

// Dimensions reads the pixel size of an image file.
func Dimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
