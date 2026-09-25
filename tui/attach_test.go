// attach_test.go — drag-and-drop parsing, image preparation, and the model
// integration (paste → chips → send → remove).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ---- helpers ----------------------------------------------------------------

func writeTestPNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 96, A: 255})
		}
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", p, err)
	}
	f.Close()
	return p
}

// ---- drop recognition -------------------------------------------------------

func TestTokenizeDropQuotingAndNewlines(t *testing.T) {
	got, ok := tokenizeDrop("'/a/my file.png' /b/plain.txt\n\"/c/two words.txt\" /d/esc\\ aped.txt")
	if !ok {
		t.Fatal("tokenizeDrop rejected balanced quoting")
	}
	want := []string{"/a/my file.png", "/b/plain.txt", "/c/two words.txt", "/d/esc aped.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
	if _, ok := tokenizeDrop("he said \"hi"); ok {
		t.Fatal("unbalanced quote accepted")
	}
	// Windows separators are not escape sequences.
	got, _ = tokenizeDrop(`C:\Users\me\shot.png`)
	if len(got) != 1 || got[0] != `C:\Users\me\shot.png` {
		t.Fatalf("windows path tokenized to %#v", got)
	}
}

func TestDroppedPathsTerminalShapes(t *testing.T) {
	dir := t.TempDir()
	img := writeTestPNG(t, dir, "shot.png", 8, 8)
	spaced := writeTestPNG(t, dir, "my shot.png", 8, 8)
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + spaced + "' " + text
	escaped := strings.ReplaceAll(spaced, " ", `\ `)
	lines := img + "\n" + text
	url := "file://" + strings.ReplaceAll(spaced, " ", "%20")
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"absolute", img, []string{img}},
		{"quoted", quoted, []string{spaced, text}},
		{"escaped", escaped, []string{spaced}},
		{"newline", lines, []string{img, text}},
		{"url", url, []string{spaced}},
	}
	for _, tc := range cases {
		got := droppedPaths(tc.content, dir)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("%s: paths = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestDroppedPathsRejectsProseAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := droppedPaths("please explain Makefile", dir); got != nil {
		t.Fatalf("prose claimed as a drop: %#v", got)
	}
	if got := droppedPaths("Makefile", dir); got != nil {
		t.Fatalf("bare existing filename claimed as a drop: %#v", got)
	}
	if got := droppedPaths("/no/such/file-xyz.png", dir); got != nil {
		t.Fatalf("missing path claimed as a drop: %#v", got)
	}
	if got := droppedPaths("./Makefile", dir); len(got) != 1 || got[0] != filepath.Join(dir, "Makefile") {
		t.Fatalf("relative path drop = %#v", got)
	}
}

func TestResolveDropTokenTildeAndHost(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := resolveDropToken("~/x.png", ""); got != filepath.Join(home, "x.png") {
		t.Fatalf("tilde expansion = %q", got)
	}
	if got := resolveDropToken("file://otherhost/path/x.png", "/tmp"); got != "" {
		t.Fatalf("remote host accepted: %q", got)
	}
}

// ---- image detection / preparation -----------------------------------------

func TestImageMimeForFile(t *testing.T) {
	dir := t.TempDir()
	png := writeTestPNG(t, dir, "shot.PNG", 4, 4)
	if got := imageMimeForFile(png); got != "image/png" {
		t.Fatalf("uppercase extension = %q", got)
	}
	// No extension: sniff the header.
	raw, err := os.ReadFile(png)
	if err != nil {
		t.Fatal(err)
	}
	noExt := filepath.Join(dir, "clipboard")
	if err := os.WriteFile(noExt, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := imageMimeForFile(noExt); got != "image/png" {
		t.Fatalf("sniffed = %q", got)
	}
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := imageMimeForFile(text); got != "" {
		t.Fatalf("text file detected as image: %q", got)
	}
}

func TestPrepareImageKeepsSmallImageUntouched(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPNG(t, dir, "small.png", 20, 10)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out, mime, w, h, err := prepareImage(raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || w != 20 || h != 10 {
		t.Fatalf("prepareImage = (%s, %d, %d), want (image/png, 20, 10)", mime, w, h)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("small image was re-encoded")
	}
}

func TestPrepareImageScalesOversizedImage(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPNG(t, dir, "big.png", 3000, 1500)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out, mime, w, h, err := prepareImage(raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if (mime != "image/png" && mime != "image/jpeg") || max(w, h) > maxImageEdge {
		t.Fatalf("prepareImage = (%s, %d, %d), want a supported mime within %dpx", mime, w, h, maxImageEdge)
	}
	if w*1500 != h*3000 && absInt(w*1500-h*3000) > 1500 {
		t.Fatalf("aspect ratio drifted: %dx%d", w, h)
	}
	if base64Len(out) > maxImageB64 {
		t.Fatalf("encoded image over budget: %d", base64Len(out))
	}
}

func TestPrepareAttachmentsBuildsWireShape(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPNG(t, dir, "shot.png", 16, 16)
	atts := []attachment{{Path: p, Name: "shot.png", Mime: "image/png", Size: 100}}
	out, err := prepareAttachments(atts)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("attachments = %#v", out)
	}
	for _, key := range []string{"type", "name", "mimeType", "data", "width", "height"} {
		if _, ok := out[0][key]; !ok {
			t.Fatalf("wire attachment missing %q: %#v", key, out[0])
		}
	}
	if _, err := prepareAttachments([]attachment{{Path: filepath.Join(dir, "gone.png")}}); err == nil {
		t.Fatal("missing file prepared without error")
	}
}

func TestAttachmentChipsNamesAndSizes(t *testing.T) {
	line := attachmentChips([]attachment{
		{Name: "shot.png", Size: 1 << 20},
		{Name: "chart.png", Size: 2048},
	}, 200)
	for _, want := range []string{"shot.png", "1.0 MB", "chart.png", "2 KB"} {
		if !strings.Contains(line, want) {
			t.Fatalf("chips %q missing %q", line, want)
		}
	}
	if attachmentChips(nil, 80) != "" {
		t.Fatal("empty attachment list rendered a line")
	}
}

// ---- model integration ------------------------------------------------------

func TestPasteOfDroppedImageQueuesAttachment(t *testing.T) {
	dir := t.TempDir()
	img := writeTestPNG(t, dir, "shot.png", 8, 8)
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.connected = true
	updated, _ := m.Update(tea.PasteMsg{Content: img + " " + text})
	m = updated.(model)
	if len(m.attachments) != 1 || m.attachments[0].Name != "shot.png" {
		t.Fatalf("attachments = %#v", m.attachments)
	}
	if !strings.Contains(m.input.Value(), text) {
		t.Fatalf("non-image drop not inserted into input: %q", m.input.Value())
	}
}

func TestEnterSendsAndClearsAttachments(t *testing.T) {
	m := newTestModel()
	m.connected = true
	m.attachments = []attachment{{Path: "/tmp/x.png", Name: "x.png", Mime: "image/png"}}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if len(m.attachments) != 0 {
		t.Fatalf("attachments survived send: %#v", m.attachments)
	}
	if cmd == nil {
		t.Fatal("send returned no command")
	}
	if len(m.blocks) != 1 || !strings.Contains(m.blocks[0].text, "[image: x.png]") {
		t.Fatalf("user block = %#v, want the image marker", m.blocks)
	}
}

func TestBackspaceRemovesAttachmentOnlyWithEmptyInput(t *testing.T) {
	m := newTestModel()
	m.attachments = []attachment{{Name: "a.png"}, {Name: "b.png"}}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(model)
	if len(m.attachments) != 1 || m.attachments[0].Name != "a.png" {
		t.Fatalf("attachments after backspace = %#v", m.attachments)
	}
	// With text present the textarea owns backspace.
	m.input.SetValue("keep")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(model)
	if len(m.attachments) != 1 {
		t.Fatalf("backspace with text removed an attachment: %#v", m.attachments)
	}
}

func TestBusyEnterKeepsAttachmentsQueued(t *testing.T) {
	m := newTestModel()
	m.connected = true
	m.busy = true
	m.attachments = []attachment{{Name: "x.png"}}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if len(m.attachments) != 1 {
		t.Fatalf("busy steer dropped attachments: %#v", m.attachments)
	}
	if m.contextNote == "" {
		t.Fatal("busy steer with attachments left no explanation")
	}
}

func TestSessionSwitchClearsAttachments(t *testing.T) {
	m := newTestModel()
	m.attachments = []attachment{{Name: "x.png"}}
	m = m.switchSession("other")
	if len(m.attachments) != 0 {
		t.Fatalf("attachments survived a session switch: %#v", m.attachments)
	}
}

func TestUserMessageTextMarksAttachments(t *testing.T) {
	got := userMessageText("look at this", []attachment{{Name: "a.png"}, {Name: "b.png"}})
	if got != "look at this\n[image: a.png]\n[image: b.png]" {
		t.Fatalf("userMessageText = %q", got)
	}
	if userMessageText("plain", nil) != "plain" {
		t.Fatal("plain text changed")
	}
}

func TestReplayConversationFlattensImageContent(t *testing.T) {
	raw := `{"role":"user","content":[
		{"type":"text","text":"what do you see?"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}`
	var msg storedMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Content != "what do you see?\n[image]" {
		t.Fatalf("flattened content = %q", msg.Content)
	}
	blocks := replayConversation([]storedMessage{msg})
	if len(blocks) != 1 || blocks[0].kind != blockUser ||
		!strings.Contains(blocks[0].text, "what do you see?") ||
		!strings.Contains(blocks[0].text, "[image]") {
		t.Fatalf("replay blocks = %#v", blocks)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// base64Len is the encoded length of raw without copying it.
func base64Len(raw []byte) int {
	return (len(raw) + 2) / 3 * 4
}

// ---- resizing ---------------------------------------------------------------

// screenshotLike builds an opaque, FLAT image: white background with thin
// black marks, the shape a terminal/editor screenshot has. Flat images are
// the case where lossless PNG beats JPEG by a wide margin, so this is the
// fixture that catches a "always prefer JPEG when opaque" shortcut.
func screenshotLike(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	black := color.RGBA{A: 255}
	for y := 8; y < h-8; y += 12 {
		for x := 20; x < w-20; x += 7 {
			for dx := 0; dx < 4 && x+dx < w-20; dx++ {
				img.Set(x+dx, y, black)
				img.Set(x+dx, y+1, black)
			}
		}
	}
	return img
}

// photoLike builds a photographic image — a smooth base with per-pixel
// noise and millions of distinct colours. This is the case JPEG genuinely
// wins: measured at 2000x1200, PNG costs ~7 MB here against ~1.2 MB for
// JPEG. (A low-colour gradient is NOT such a case: PNG's filters beat JPEG
// on it, which is why the assertion above uses noise rather than a
// gradient.)
func photoLike(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := uint32(12345)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rnd = rnd*1664525 + 1013904223
			base := uint8((x*3 + y) % 256)
			img.Set(x, y, color.RGBA{
				R: base ^ uint8(rnd>>28), G: base ^ uint8(rnd>>26),
				B: base ^ uint8(rnd>>24), A: 255})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func TestPrepareImageTakesTheSmallerEncodingForOpaqueImages(t *testing.T) {
	// A flat opaque screenshot: PNG must win, and win by a lot. Before this
	// comparison was measured, an opaque image always came back as JPEG —
	// this fixture encoded ~5x larger AND lossy.
	shot := screenshotLike(2400, 1400)
	out, mime, w, h, err := prepareImage(encodePNG(t, shot), "image/png")
	if err != nil {
		t.Fatalf("prepareImage: %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("a flat screenshot chose %s (%d bytes); PNG at the same size is %d bytes",
			mime, len(out), len(encodePNG(t, scaleToEdge(shot, 2000))))
	}
	if w != 2000 || h != 1166 {
		t.Fatalf("scaled to %dx%d, want 2000x1166", w, h)
	}
	if got := len(encodePNG(t, scaleToEdge(shot, 2000))); len(out) > got {
		t.Fatalf("output %d bytes exceeds the png encoding %d bytes", len(out), got)
	}

	// A noisy photo: JPEG is genuinely smaller here (PNG ~7MB vs JPEG
	// ~1.2MB at this size), so the measurement must pick it.
	photo := photoLike(2400, 1400)
	outP, mimeP, wp, hp, err := prepareImage(encodePNG(t, photo), "image/png")
	if err != nil {
		t.Fatalf("prepareImage photo: %v", err)
	}
	if mimeP != "image/jpeg" {
		t.Fatalf("a noisy photo chose %s for %d bytes; the measured jpeg is smaller",
			mimeP, len(outP))
	}
	if wp != 2000 || hp != 1166 {
		t.Fatalf("photo scaled to %dx%d, want 2000x1166", wp, hp)
	}
	if got := base64.StdEncoding.EncodedLen(len(outP)); got > maxImageB64 {
		t.Fatalf("chosen encoding is over the inline cap: %d > %d", got, maxImageB64)
	}
}

func TestPrepareImageKeepsTransparencyAsPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2500, 1400))
	for y := 0; y < 1400; y++ {
		for x := 0; x < 2500; x++ {
			// A hard diagonal edge with a fully transparent field: JPEG must
			// not silently flatten this to white.
			if x > y {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
	}
	out, mime, _, _, err := prepareImage(encodePNG(t, img), "image/png")
	if err != nil {
		t.Fatalf("prepareImage: %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("an image with alpha chose %s; transparency cannot survive JPEG", mime)
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not a decodable PNG: %v", err)
	}
	// Sample a point well INSIDE the transparent field (below the diagonal):
	// pixels on the diagonal itself are CatmullRom-interpolated blends, so
	// asserting on one would test the resampler's edge behavior, not whether
	// alpha survived.
	db := decoded.Bounds()
	if _, _, _, a := decoded.At(db.Dx()/8, db.Dy()-db.Dy()/8).RGBA(); a != 0 {
		t.Fatalf("a transparent-field pixel became opaque (alpha=%d)", a)
	}
	if _, _, _, a := decoded.At(db.Dx()-db.Dx()/8, db.Dy()/8).RGBA(); a == 0 {
		t.Fatal("an opaque-region pixel became transparent")
	}
	// Even when JPEG would be smaller, alpha must keep PNG: flattening onto
	// white silently changes what the model sees (here it would erase the
	// white diagonal's contrast entirely).
	scaled := scaleToEdge(img, maxImageEdge)
	if !isOpaque(scaled) {
		var jpegBuf bytes.Buffer
		if err := jpeg.Encode(&jpegBuf, flattenOnWhite(scaled), &jpeg.Options{Quality: 85}); err == nil &&
			jpegBuf.Len() < len(out) {
			t.Logf("jpeg (%d) is smaller than the chosen png (%d) — PNG is still correct for alpha",
				jpegBuf.Len(), len(out))
		}
	}
}

func TestPrepareImageLeavesSmallImagesByteIdentical(t *testing.T) {
	raw := encodePNG(t, screenshotLike(800, 600))
	out, mime, w, h, err := prepareImage(raw, "image/png")
	if err != nil {
		t.Fatalf("prepareImage: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("a small, in-budget image was re-encoded instead of passed through")
	}
	if mime != "image/png" || w != 800 || h != 600 {
		t.Fatalf("passthrough changed the metadata: %s %dx%d", mime, w, h)
	}
}

func TestPrepareImageNeverUpscales(t *testing.T) {
	raw := encodePNG(t, screenshotLike(320, 200))
	_, _, w, h, err := prepareImage(raw, "image/png")
	if err != nil {
		t.Fatalf("prepareImage: %v", err)
	}
	if w != 320 || h != 200 {
		t.Fatalf("a small image was upscaled to %dx%d", w, h)
	}
	// scaleToEdge must be a no-op at or below the cap, in both orientations.
	// (A 2001x1 image is genuinely over the cap and is downscaled — see the
	// aspect-ratio test below, not here.)
	for _, d := range [][2]int{{320, 200}, {200, 320}, {2000, 2000}, {1, 2000}, {2000, 1}} {
		img := image.NewRGBA(image.Rect(0, 0, d[0], d[1]))
		got := scaleToEdge(img, maxImageEdge).Bounds()
		if got.Dx() != d[0] || got.Dy() != d[1] {
			t.Fatalf("scaleToEdge(%dx%d) changed the image to %dx%d",
				d[0], d[1], got.Dx(), got.Dy())
		}
	}
}

func TestScaleToEdgePreservesAspectRatioInBothOrientations(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{4000, 2000, 2000, 1000}, // landscape
		{2000, 4000, 1000, 2000}, // portrait
		{4000, 4000, 2000, 2000}, // square
		{2001, 1000, 2000, 999},  // odd ratio, rounds down
		{1, 10000, 1, 2000},      // extreme portrait keeps >=1px
	} {
		got := scaleToEdge(image.NewRGBA(image.Rect(0, 0, tc.w, tc.h)),
			maxImageEdge).Bounds()
		if got.Dx() != tc.wantW || got.Dy() != tc.wantH {
			t.Fatalf("%dx%d scaled to %dx%d, want %dx%d",
				tc.w, tc.h, got.Dx(), got.Dy(), tc.wantW, tc.wantH)
		}
	}
}

func TestEncodeCandidatesMeasuredRatherThanAssumed(t *testing.T) {
	// Opaque + flat: the smaller of the two wins, and for this shape that is
	// PNG. (The regression: opaque short-circuited to JPEG unconditionally.)
	flat := scaleToEdge(screenshotLike(1400, 900), maxImageEdge)
	out, mime := encodeCandidates(flat, isOpaque(flat))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, flat); err != nil {
		t.Fatal(err)
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, flattenOnWhite(flat), &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if jpegBuf.Len() < pngBuf.Len() {
		t.Skip("fixture no longer favors png; the assertion below is the invariant")
	}
	if mime != "image/png" || len(out) != pngBuf.Len() {
		t.Fatalf("flat opaque chose mime=%s len=%d; png=%d jpeg=%d",
			mime, len(out), pngBuf.Len(), jpegBuf.Len())
	}
}
