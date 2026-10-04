package server

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Attachments used to be stored inline: a pasted photo arrives from the browser as
// bare base64 in ChatAttach.Data, and keeping it in the session JSON meant a 3 MB
// image added 4 MB to a file that gets rewritten on every streamed token. The bytes
// now live in their own files under <sessions>/blobs and the JSON only keeps a
// reference, so the transcript stays cheap to write no matter how many images it
// holds.
//
// Data is bare base64, never a data URL. The browser strips the prefix when it
// reads a file (web/app-svg.js splits the data URL on ","), Ollama's chat API
// rejects a prefixed image, and the preview helper rebuilds the data URL itself
// from the mime type. So the prefix is never stored, never added back, and only
// tolerated on the way in.

// maxAttachBlobNameLen guards the generated file name. Session ids are short and
// the suffix is a counter plus a random token, so this is only a sanity bound.
const maxAttachBlobNameLen = 120

// chatSessionBlobDir is where attachment bytes are kept. It sits next to the
// session files so a single data directory still holds everything.
func (st *chatSessionStore) chatSessionBlobDir() string {
	return filepath.Join(st.dir, "blobs")
}

// chatAttachExt maps a MIME type to a file extension. Unknown types fall back to
// "bin"; the extension is cosmetic, the MIME type in the JSON is what counts.
func chatAttachExt(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	switch mime {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	case "image/svg+xml":
		return "svg"
	case "audio/ogg":
		return "ogg"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/webm":
		return "weba"
	case "audio/mp4", "audio/m4a":
		return "m4a"
	case "audio/flac":
		return "flac"
	case "application/pdf":
		return "pdf"
	}
	if i := strings.IndexByte(mime, '/'); i > 0 {
		if sub := mime[i+1:]; sub != "" && !strings.ContainsAny(sub, " +;") {
			return sub
		}
	}
	return "bin"
}

// decodeAttachData turns an inline attachment payload into its MIME type and raw
// bytes. The normal input is bare base64, which is what the browser sends; a full
// data URL is also accepted so a hand-crafted request or an older client still
// works, and in that case the MIME type is picked up from the URL. It reports
// ok=false for anything it cannot decode, which is how it recognises an
// attachment that is already only a reference to a stored blob.
func decodeAttachData(data string) (mime string, body []byte, ok bool) {
	if data == "" {
		return "", nil, false
	}
	payload := data
	isBase64 := true
	if rest, found := strings.CutPrefix(data, "data:"); found {
		meta, cut, ok2 := strings.Cut(rest, ",")
		if !ok2 {
			return "", nil, false
		}
		// ";base64" is an encoding marker, not part of the media type, so it has to
		// be stripped before the mime is stored or turned into a file extension.
		isBase64 = strings.HasSuffix(meta, ";base64")
		mime = strings.TrimSuffix(meta, ";base64")
		payload = cut
	}
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			// Some browsers hand over base64 with URL-safe characters or missing
			// padding; fall back to the permissive decoders before giving up.
			if decoded, err = base64.RawStdEncoding.DecodeString(payload); err != nil {
				decoded, err = base64.URLEncoding.DecodeString(payload)
				if err != nil {
					return "", nil, false
				}
			}
		}
		return mime, decoded, true
	}
	// Percent-encoded payload, as a data URL produces for text and some types.
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return "", nil, false
	}
	return mime, []byte(decoded), true
}

// storeAttachBlobs moves the bytes of every inline attachment into a file and
// returns the list with Data emptied. Callers store the returned slice, so the
// in-memory session and the JSON on disk never hold the base64 payload.
//
// The store lock must be held by the caller.
func (st *chatSessionStore) storeAttachBlobs(sessionID string, attach []ChatAttach) []ChatAttach {
	if len(attach) == 0 {
		return attach
	}
	dir := st.chatSessionBlobDir()
	out := make([]ChatAttach, len(attach))
	copy(out, attach)
	changed := false
	for i := range out {
		a := &out[i]
		if a.Data == "" || a.Blob != "" {
			continue
		}
		mime, body, ok := decodeAttachData(a.Data)
		if !ok {
			// Undecodable: leave it alone rather than lose it.
			continue
		}
		if mime != "" {
			a.MimeType = mime
		}
		name := fmt.Sprintf("%s-%d-%s.%s", sessionID, i, blobToken(), chatAttachExt(a.MimeType))
		if len(name) > maxAttachBlobNameLen {
			name = name[len(name)-maxAttachBlobNameLen:]
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("[chat-sessions] mkdir %s: %v", dir, err)
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			log.Printf("[chat-sessions] write attachment %s: %v", name, err)
			continue
		}
		a.Blob = name
		a.Size = len(body)
		a.Data = ""
		changed = true
	}
	if !changed {
		return attach
	}
	return out
}

// blobToken keeps two attachments of the same message from colliding on a name.
// The session id plus the index already make collisions unlikely, but a repeated
// write during an edit-and-resend would reuse the same slot, so a per-write token
// is the cheap way to be sure an old file is never silently overwritten. It is
// drawn from the same counter the session ids use, so it is unique for the life
// of the process and across restarts within the same millisecond window.
func blobToken() string {
	return fmt.Sprintf("%x", chatSessionSeq.Add(1))
}

// loadAttachData fills Data from the blob file when the attachment only has a
// reference. An attachment that is already inline, or whose file has gone
// missing, is returned untouched so a lost blob degrades to a visible gap rather
// than an error.
func (st *chatSessionStore) loadAttachData(a *ChatAttach) {
	if a.Data != "" || a.Blob == "" {
		return
	}
	body, err := os.ReadFile(filepath.Join(st.chatSessionBlobDir(), a.Blob))
	if err != nil {
		log.Printf("[chat-sessions] read attachment %s: %v", a.Blob, err)
		return
	}
	// Bare base64, matching what the browser sent: the preview helpers and
	// Ollama both rebuild or expect the payload on its own, so adding a
	// "data:" prefix here would double it up and break the image.
	a.Data = base64.StdEncoding.EncodeToString(body)
}

// hydrateAttach returns a copy of the attachment list with Data filled in from
// disk. The copy matters: the session in memory deliberately keeps Data empty so
// nothing can accidentally persist the payload again.
func (st *chatSessionStore) hydrateAttach(attach []ChatAttach) []ChatAttach {
	if len(attach) == 0 {
		return attach
	}
	out := make([]ChatAttach, len(attach))
	copy(out, attach)
	for i := range out {
		st.loadAttachData(&out[i])
	}
	return out
}

// hydrateMessages returns a copy of the transcript whose attachments carry their
// bytes again, for the paths that need them: the API response that repaints the
// chat and the prompt builder that feeds images to the model.
func (st *chatSessionStore) hydrateMessages(msgs []SessionMessage) []SessionMessage {
	need := false
	for i := range msgs {
		if len(msgs[i].Attach) > 0 {
			need = true
			break
		}
	}
	if !need {
		return msgs
	}
	out := make([]SessionMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Attach) > 0 {
			out[i].Attach = st.hydrateAttach(out[i].Attach)
		}
	}
	return out
}

// dropAttachBlobs deletes the files behind a set of attachments. Used when a
// session is deleted or a user turn is rewritten by edit-and-resend.
func (st *chatSessionStore) dropAttachBlobs(attach []ChatAttach) {
	dir := st.chatSessionBlobDir()
	for _, a := range attach {
		if a.Blob == "" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, a.Blob)); err != nil && !os.IsNotExist(err) {
			log.Printf("[chat-sessions] remove attachment %s: %v", a.Blob, err)
		}
	}
}

// dropSessionBlobs removes every blob belonging to a session. Blob names are
// prefixed with the session id, so this is a scan rather than a lookup; it only
// runs on delete.
func (st *chatSessionStore) dropSessionBlobs(sessionID string) {
	dir := st.chatSessionBlobDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := sessionID + "-"
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			log.Printf("[chat-sessions] remove attachment %s: %v", e.Name(), err)
		}
	}
}

// dropOrphanBlobs deletes the attachment files of a session that no surviving
// message references any more, which is what happens when the transcript is
// trimmed. It runs only right after a trim, so the scan cost is fine.
func (st *chatSessionStore) dropOrphanBlobs(sessionID string, msgs []SessionMessage) {
	live := map[string]bool{}
	for i := range msgs {
		for _, a := range msgs[i].Attach {
			if a.Blob != "" {
				live[a.Blob] = true
			}
		}
	}
	dir := st.chatSessionBlobDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := sessionID + "-"
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || live[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			log.Printf("[chat-sessions] remove orphan attachment %s: %v", name, err)
		}
	}
}

// clearAllAttachBlobs empties the blob directory for "delete every session".
func (st *chatSessionStore) clearAllAttachBlobs() {
	dir := st.chatSessionBlobDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			log.Printf("[chat-sessions] remove attachment %s: %v", e.Name(), err)
		}
	}
}
