// attach.go — drag-and-drop file handling for the chat input.
//
// Terminals have no drag-and-drop protocol: a file dropped on the window
// arrives as a bracketed paste of its path. Terminals differ in the shape
// of that paste — a bare path, shell-quoted paths (spaces), a file:// URL,
// several paths separated by spaces or newlines — so droppedPaths recognizes
// the shapes and only claims a paste when every token resolves to an
// existing file or directory.
//
// Images are attached to the next session turn (the runner's `attachments`
// argument): Niffler's read tool is text-only, so an image path alone tells
// the model nothing. Dropped text files and directories are inserted into
// the input as plain paths instead — the agent opens them with its own
// tools, exactly like an @-reference, and no file content travels the bus.
//
// Image bytes are read and prepared lazily in the send goroutine, resized
// to the limits the runner enforces (2000px longest edge, ~4MB base64 per
// image / ~4.5MB per turn), and sent as base64 image parts. The runner
// turns them into provider-native image blocks for the LLM adapter.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/image/draw"

	// Image format decoders registered through image.Decode: the stdlib
	// covers png/jpeg/gif, x/image adds webp and bmp (bmp is transcoded to
	// png before sending — providers do not accept it).
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	// maxImageEdge is the longest edge (pixels) an attached image keeps;
	// larger images are scaled down before encoding.
	maxImageEdge = 2000
	// maxImageB64 / maxTurnB64 mirror the session runner's caps (base64
	// characters): per image and across one turn's attachments. The numbers
	// stay far below the bus max_payload (8MiB) with room for the rest of
	// the session call.
	maxImageB64 = 4_000_000
	maxTurnB64  = 4_500_000
	// maxDropFileBytes refuses absurd files before decoding; anything the
	// resize target can actually use is far smaller.
	maxDropFileBytes = 64 << 20
	// maxAttachments bounds one turn's chip list (mirrors the runner cap).
	maxAttachments = 8
)

// imageMimeByExt maps recognized image extensions to the provider MIME
// types; the empty string (or an unknown extension) falls back to sniffing
// the file header.
var imageMimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// attachment is one image queued for the next session turn.
type attachment struct {
	Path string // absolute path on this machine
	Name string // display name (filepath.Base)
	Mime string // provider MIME type (image/png, image/jpeg, ...)
	Size int64  // original file size in bytes
}

// ---- drop recognition -------------------------------------------------------

// droppedPaths reports whether a bracketed paste is a file drop and returns
// the resolved paths. It claims the paste only when it is entirely path-
// shaped and every token exists (a paste with prose or a stale path falls
// back to the textarea untouched).
func droppedPaths(content, cwd string) []string {
	if strings.TrimSpace(content) == "" || strings.ContainsRune(content, 0) {
		return nil
	}
	tokens, ok := tokenizeDrop(content)
	if !ok || len(tokens) == 0 {
		return nil
	}
	if len(tokens) == 1 && !looksLikePathToken(tokens[0]) {
		// A bare word that happens to name a file ("Makefile") is text, not
		// a drop; real drops are path-shaped (or file:// URLs).
		return nil
	}
	seen := make(map[string]bool, len(tokens))
	var paths []string
	for _, tok := range tokens {
		p := resolveDropToken(tok, cwd)
		if p == "" {
			return nil
		}
		fi, err := os.Stat(p)
		if err != nil || (!fi.Mode().IsRegular() && !fi.IsDir()) {
			return nil
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 || len(paths) > maxAttachments {
		return nil
	}
	return paths
}

// tokenizeDrop splits a shell-ish paste into tokens: whitespace (including
// newlines) separates, single and double quotes group (the quotes are
// dropped), and a backslash escapes whitespace, quotes and itself — the
// form macOS terminals use for paths with spaces. It reports ok=false for
// unbalanced quotes so a snippet of prose is never half-parsed as a path.
func tokenizeDrop(content string) ([]string, bool) {
	var tokens []string
	var b strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	runes := []rune(content)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				b.WriteRune(r)
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			} else {
				b.WriteRune(r)
			}
		case r == '\'':
			inSingle = true
		case r == '"':
			inDouble = true
		case r == '\\' && i+1 < len(runes) &&
			(runes[i+1] == ' ' || runes[i+1] == '\t' || runes[i+1] == '\n' ||
				runes[i+1] == '\\' || runes[i+1] == '\'' || runes[i+1] == '"'):
			// Windows paths (C:\Users\...) keep their backslashes: only the
			// escapable set above is consumed.
			i++
			b.WriteRune(runes[i])
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	flush()
	return tokens, true
}

// resolveDropToken turns one token into an absolute path: file:// URLs are
// percent-decoded (WezTerm and browsers drop those), ~ expands, and
// relative tokens resolve against the conversation workspace. It returns ""
// for a malformed or unsupported token (a remote file:// host).
func resolveDropToken(tok, cwd string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ""
	}
	if strings.HasPrefix(tok, "file://") {
		rest := strings.TrimPrefix(tok, "file://")
		if slash := strings.IndexByte(rest, '/'); slash < 0 {
			return ""
		} else if host := rest[:slash]; host != "" && !strings.EqualFold(host, "localhost") {
			return "" // file://host/... names another machine
		}
		if p, err := url.PathUnescape(rest); err == nil {
			tok = p
		} else {
			return ""
		}
		if runtime.GOOS == "windows" && len(tok) >= 3 && tok[0] == '/' && tok[2] == ':' {
			tok = tok[1:] // file:///C:/dir → C:/dir
		}
	}
	if tok == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Clean(home)
	}
	if strings.HasPrefix(tok, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Clean(filepath.Join(home, tok[2:]))
	}
	if !filepath.IsAbs(tok) {
		if cwd == "" {
			return ""
		}
		return filepath.Clean(filepath.Join(cwd, filepath.FromSlash(tok)))
	}
	return filepath.Clean(filepath.FromSlash(tok))
}

// looksLikePathToken decides whether a single-token paste is plausibly a
// path rather than a word that happens to collide with a file name.
func looksLikePathToken(tok string) bool {
	switch {
	case tok == "", tok == ".":
		return false
	case strings.HasPrefix(tok, "file://"), strings.HasPrefix(tok, "~"):
		return true
	case strings.ContainsAny(tok, `/\`):
		return true
	case strings.HasPrefix(tok, "."):
		return true
	case strings.ContainsAny(tok, " \t"):
		return true // was quoted: one name, not prose
	case len(tok) >= 2 && tok[1] == ':':
		return true // Windows drive path
	}
	return false
}

// ---- image detection --------------------------------------------------------

// imageMimeForFile names the supported image type of a file: the extension
// when recognized, else a header sniff (screenshots sometimes drop without
// an extension). "" means "not a supported image".
func imageMimeForFile(path string) string {
	if mime, ok := imageMimeByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return mime
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return ""
	}
	switch http.DetectContentType(buf[:n]) {
	case "image/png":
		return "image/png"
	case "image/jpeg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	case "image/bmp":
		return "image/bmp"
	}
	return ""
}

// attachmentFromPath builds the queued attachment for a dropped image path.
func attachmentFromPath(path string) (attachment, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return attachment{}, err
	}
	mime := imageMimeForFile(path)
	if mime == "" {
		return attachment{}, fmt.Errorf("%s is not a supported image", filepath.Base(path))
	}
	return attachment{
		Path: path,
		Name: filepath.Base(path),
		Mime: mime,
		Size: fi.Size(),
	}, nil
}

// ---- send-time preparation --------------------------------------------------

// prepareAttachments reads the queued images and returns the session call's
// `attachments` array. Reading and resizing happen here (in the send
// goroutine) so dropping a large screenshot never blocks the UI.
func prepareAttachments(atts []attachment) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(atts))
	total := 0
	for _, a := range atts {
		raw, err := os.ReadFile(a.Path)
		if err != nil {
			return nil, fmt.Errorf("attach %s: %w", a.Name, err)
		}
		if len(raw) > maxDropFileBytes {
			return nil, fmt.Errorf("attach %s: file exceeds the %dMB limit",
				a.Name, maxDropFileBytes>>20)
		}
		data, mime, w, h, err := prepareImage(raw, a.Mime)
		if err != nil {
			return nil, fmt.Errorf("attach %s: %w", a.Name, err)
		}
		enc := base64.StdEncoding.EncodeToString(data)
		if len(enc) > maxImageB64 {
			return nil, fmt.Errorf("attach %s: image remains over the %dMB inline limit",
				a.Name, maxImageB64/(1<<20))
		}
		total += len(enc)
		if total > maxTurnB64 {
			return nil, fmt.Errorf("attachments exceed the %dMB per-message limit",
				maxTurnB64/(1<<20))
		}
		out = append(out, map[string]any{
			"type":     "image",
			"name":     a.Name,
			"mimeType": mime,
			"data":     enc,
			"width":    w,
			"height":   h,
		})
	}
	return out, nil
}

// prepareImage normalizes one image for the wire: supported MIME types under
// the edges/size limit pass through untouched; bmp and oversized images are
// decoded, scaled to fit maxImageEdge, and re-encoded as the smaller of png
// and jpeg (alpha images keep png, flattened jpeg is the fallback). Returns
// the encoded bytes, final MIME type and pixel dimensions.
func prepareImage(raw []byte, mime string) ([]byte, string, int, int, error) {
	if mime != "image/bmp" {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil &&
			cfg.Width <= maxImageEdge && cfg.Height <= maxImageEdge &&
			base64.StdEncoding.EncodedLen(len(raw)) <= maxImageB64 {
			return raw, mime, cfg.Width, cfg.Height, nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("cannot decode image: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, "", 0, 0, fmt.Errorf("image has no pixels")
	}
	edge := maxImageEdge
	if max(w, h) < edge {
		edge = max(w, h)
	}
	for {
		scaled := scaleToEdge(img, edge)
		opaque := isOpaque(scaled)
		best, bestMime := encodeCandidates(scaled, opaque)
		if base64.StdEncoding.EncodedLen(len(best)) <= maxImageB64 || edge <= 256 {
			sb := scaled.Bounds()
			return best, bestMime, sb.Dx(), sb.Dy(), nil
		}
		edge = edge * 3 / 4
	}
}

// scaleToEdge scales img so its longest edge is at most edge (never up).
func scaleToEdge(img image.Image, edge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= edge {
		return img
	}
	nw, nh := w, h
	if w >= h {
		nw = edge
		nh = max(1, h*edge/w)
	} else {
		nh = edge
		nw = max(1, w*edge/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// encodeCandidates encodes the scaled image as png and/or jpeg and returns
// the smaller encoding. Opaque images try both; images with alpha prefer
// png and fall back to jpeg flattened onto white (so transparency degrades
// to white instead of black).
func encodeCandidates(img image.Image, opaque bool) ([]byte, string) {
	var pngBuf bytes.Buffer
	pngErr := png.Encode(&pngBuf, img)

	var jpegBuf bytes.Buffer
	jpegErr := jpeg.Encode(&jpegBuf, flattenOnWhite(img), &jpeg.Options{Quality: 85})

	switch {
	case pngErr != nil && jpegErr != nil:
		// Neither encoder worked (should not happen for RGBA); return the
		// png buffer anyway so the caller fails with a size check rather
		// than a nil dereference.
		return append([]byte(nil), pngBuf.Bytes()...), "image/png"
	case jpegErr != nil:
		return pngBuf.Bytes(), "image/png"
	case pngErr != nil:
		return jpegBuf.Bytes(), "image/jpeg"
	case opaque || jpegBuf.Len() < pngBuf.Len():
		return jpegBuf.Bytes(), "image/jpeg"
	default:
		return pngBuf.Bytes(), "image/png"
	}
}

// isOpaque reports whether every pixel is fully opaque, so the jpeg
// candidate is lossy-but-faithful.
func isOpaque(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return false
			}
		}
	}
	return true
}

// flattenOnWhite composites an image over white for jpeg encoding.
func flattenOnWhite(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// ---- view -------------------------------------------------------------------

// attachmentChips renders the queued attachments as one line above the input
// rule: "▣ shot.png (1.2 MB)  ▣ chart.png (340 KB)".
func attachmentChips(atts []attachment, width int) string {
	if len(atts) == 0 || width <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(badgeAgentStyle.Render("▣"))
	for i, a := range atts {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(" ")
		b.WriteString(badgeAgentStyle.Render(a.Name))
		b.WriteString(metaStyle.Render(" (" + humanSize(a.Size) + ")"))
	}
	return truncate(b.String(), width)
}

// humanSize renders a byte count compactly for the chip line.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ---- model integration ------------------------------------------------------

// attachDroppedPaths queues dropped images and inserts dropped non-image
// paths into the input at the cursor. Directories and text files stay plain
// text: the agent opens them with its own tools, and no file content travels
// the bus.
func (m model) attachDroppedPaths(paths []string) (model, tea.Cmd) {
	var added []attachment
	var insert []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			if mime := imageMimeForFile(p); mime != "" {
				if len(m.attachments)+len(added) >= maxAttachments {
					m.contextNote = t(m.loc, "attach.limit", strconv.Itoa(maxAttachments))
					break
				}
				added = append(added, attachment{
					Path: p,
					Name: filepath.Base(p),
					Mime: mime,
					Size: fi.Size(),
				})
				continue
			}
		}
		insert = append(insert, quoteDroppedPath(p))
	}
	if len(added) > 0 {
		m.attachments = append(m.attachments, added...)
		m.contextNote = t(m.loc, "attach.added", added[len(added)-1].Name)
	}
	if len(insert) > 0 {
		text := strings.Join(insert, " ")
		if cur := m.input.Value(); cur != "" && !strings.HasSuffix(cur, " ") && !strings.HasSuffix(cur, "\n") {
			text = " " + text
		}
		m.input.InsertString(text + " ")
	}
	m.layout()
	return m, nil
}

// quoteDroppedPath renders a path for insertion into the message text: a
// path with whitespace is wrapped in backticks (the same code-span shape the
// transcript uses for @-references) so the model sees one path, not two
// words.
func quoteDroppedPath(p string) string {
	slash := filepath.ToSlash(p)
	if strings.ContainsAny(slash, " \t") {
		return "`" + slash + "`"
	}
	return slash
}

// userMessageText is the local transcript rendering of a sent message: the
// typed text plus one marker line per attachment, so the user sees exactly
// which images rode the turn.
func userMessageText(content string, atts []attachment) string {
	if len(atts) == 0 {
		return content
	}
	var b strings.Builder
	b.WriteString(content)
	for _, a := range atts {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[image: " + a.Name + "]")
	}
	return b.String()
}
