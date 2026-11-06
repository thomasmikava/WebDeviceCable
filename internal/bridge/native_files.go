package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxNativeFile = 8 << 20
const fileChunk = 24 << 10

type nativeInput struct {
	Context   bool   `json:"-"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Hash      string `json:"sha256"`
	Data      []byte `json:"-"`
	PairingID string `json:"-"`
	LeaseID   string `json:"-"`
}
type Artifact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Hash      string `json:"sha256"`
}
type TokenUsage struct {
	Input     int64 `json:"inputTokens"`
	Cached    int64 `json:"cachedInputTokens"`
	Output    int64 `json:"outputTokens"`
	Reasoning int64 `json:"reasoningOutputTokens"`
	Total     int64 `json:"totalTokens"`
}

func fileHash(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }

// Transport does not interpret document contents. Provider adapters validate their own native inputs.
func validateNativeFile(media string, data []byte) error {
	if len(data) > maxNativeFile || len(media) > 150 || strings.ContainsAny(media, "\r\n\x00") {
		return errors.New("invalid file metadata or file exceeds 8 MiB")
	}
	return nil
}
func validateProviderFile(media string, data []byte) error {
	if len(data) < 1 || len(data) > maxNativeFile {
		return errors.New("file must be 1 byte–8 MiB")
	}
	if media == "text/plain" || media == "text/markdown" || media == "application/json" {
		if len(data) > 128<<10 || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return errors.New("text documents must be UTF-8 and at most 128 KiB")
		}
		return nil
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 32e6 {
		return errors.New("invalid or oversized image")
	}
	if media != "image/"+format {
		return errors.New("image mediaType does not match its contents")
	}
	return nil
}
func (a *App) selectedInputs(p *Pairing, lease string, ids []string) ([]nativeInput, error) {
	if len(ids) > 0 && !p.Files {
		return nil, filePermissionDenied()
	}
	result := []nativeInput{}
	total := 0
	seen := map[string]bool{}
	for _, id := range ids {
		input := a.inputs[id]
		if input == nil || input.Context || input.PairingID != p.ID || input.LeaseID != lease || len(input.Data) != input.Size || fileHash(input.Data) != input.Hash || seen[id] {
			return nil, fail(400, "invalid_input", "Use completed inputs uploaded by this requesting tab.")
		}
		if e := validateNativeFile(input.MediaType, input.Data); e != nil {
			return nil, fail(400, "invalid_input", e.Error())
		}
		seen[id] = true
		total += input.Size
		if total > 16<<20 {
			return nil, fail(400, "invalid_input", "A request accepts up to 16 MiB of supplied inputs.")
		}
		result = append(result, *input)
	}
	return result, nil
}
func (a *App) nativeFileRoutes(mux *http.ServeMux, paired func(func(http.ResponseWriter, *http.Request, *Pairing) error) http.HandlerFunc, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	createInput := paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, e := a.liveLease(r, p)
		if e != nil {
			return e
		}
		contextInput := r.URL.Path == "/v2/contexts"
		if !contextInput && !p.Files {
			return filePermissionDenied()
		}
		var in nativeInput
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if len(in.Name) < 1 || len(in.Name) > 100 || strings.ContainsAny(in.Name, "\x00\r\n") || in.Size < 0 || in.Size > maxNativeFile || len(in.Hash) != 64 || strings.Trim(in.Hash, "0123456789abcdef") != "" {
			return fail(400, "invalid_input", "Supply a file name, size and SHA-256 hash.")
		}
		if in.MediaType == "" {
			in.MediaType = "application/octet-stream"
		}
		if contextInput && (in.MediaType != "text/plain" || in.Size > 128<<10) {
			return fail(400, "invalid_context", "Text context must be UTF-8 plain text of at most 128 KiB.")
		}
		in.Context = contextInput
		if e := validateNativeFile(in.MediaType, nil); e != nil {
			return fail(400, "invalid_input", e.Error())
		}
		count, reserved := 0, 0
		for _, item := range a.inputs {
			reserved += item.Size
			if item.LeaseID == lease {
				count++
			}
		}
		if count >= 64 || reserved+in.Size > 64<<20 {
			return fail(429, "input_limit", "Remove unused inputs before uploading more.")
		}
		in.ID = secret()
		in.PairingID = p.ID
		in.LeaseID = lease
		in.Data = make([]byte, 0, in.Size)
		a.inputs[in.ID] = &in
		writeJSON(w, 201, in)
		return nil
	})
	mux.HandleFunc("POST /v2/inputs", createInput)
	mux.HandleFunc("POST /v2/contexts", createInput)
	mux.HandleFunc("PUT /v2/inputs/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, e := a.liveLease(r, p)
		if e != nil {
			return e
		}
		input := a.inputs[r.PathValue("id")]
		if input == nil || input.PairingID != p.ID || input.LeaseID != lease {
			return fail(404, "not_found", "Input not found.")
		}
		if !input.Context && !p.Files {
			return filePermissionDenied()
		}
		var in struct {
			Offset int    `json:"offset"`
			Data   string `json:"data"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		data, e := base64.StdEncoding.DecodeString(in.Data)
		if e != nil || len(data) < 1 || len(data) > fileChunk || in.Offset != len(input.Data) || len(data) > input.Size-len(input.Data) {
			return fail(409, "input_offset", "Upload ordered chunks of at most 24 KiB. Restart an ambiguous upload explicitly.")
		}
		input.Data = append(input.Data, data...)
		if len(input.Data) == input.Size {
			if fileHash(input.Data) != input.Hash {
				delete(a.inputs, input.ID)
				return fail(400, "invalid_input", "File hash mismatch.")
			}
			if input.Context && (!utf8.Valid(input.Data) || bytes.IndexByte(input.Data, 0) >= 0) {
				delete(a.inputs, input.ID)
				return fail(400, "invalid_context", "Text context must be UTF-8 without NUL bytes.")
			}
			if e = validateNativeFile(input.MediaType, input.Data); e != nil {
				delete(a.inputs, input.ID)
				return fail(400, "invalid_input", e.Error())
			}
		}
		writeJSON(w, 200, map[string]any{"id": input.ID, "offset": len(input.Data), "complete": len(input.Data) == input.Size})
		return nil
	}))
	mux.HandleFunc("DELETE /v2/inputs/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, e := a.liveLease(r, p)
		if e != nil {
			return e
		}
		if input := a.inputs[r.PathValue("id")]; input != nil && input.PairingID == p.ID && input.LeaseID == lease {
			delete(a.inputs, input.ID)
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("GET /v2/jobs/{id}/artifacts/{artifact}/{offset}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		j := findJob(&a.state, r.PathValue("id"))
		if j == nil || j.PairingID != p.ID {
			return fail(404, "not_found", "Artifact not found.")
		}
		if !p.Files {
			return filePermissionDenied()
		}
		artifact, e := findArtifact(j, r.PathValue("artifact"))
		if e != nil {
			return e
		}
		offset, e := strconv.Atoi(r.PathValue("offset"))
		if e != nil || offset < 0 || offset > artifact.Size {
			return fail(400, "invalid_offset", "Supply a valid artifact offset.")
		}
		f, e := openLocalFile(a.artifactPath(j.ID, artifact.ID), 8<<20)
		if e != nil {
			return fail(410, "artifact_missing", "The output file is no longer available.")
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || info.Size() != int64(artifact.Size) {
			return fail(410, "artifact_missing", "The output file is incomplete.")
		}
		part := make([]byte, min(fileChunk, artifact.Size-offset))
		n, e := f.ReadAt(part, int64(offset))
		if (e != nil && e != io.EOF) || n != len(part) {
			return fail(410, "artifact_missing", "The output file is incomplete.")
		}
		writeJSON(w, 200, map[string]any{"id": artifact.ID, "offset": offset, "data": base64.StdEncoding.EncodeToString(part), "complete": offset+len(part) == artifact.Size})
		return nil
	}))
	mux.HandleFunc("GET /api/jobs/{id}/artifacts/{artifact}", control(func(w http.ResponseWriter, r *http.Request) error {
		j := findJob(&a.state, r.PathValue("id"))
		if j == nil {
			return fail(404, "not_found", "Artifact not found.")
		}
		artifact, e := findArtifact(j, r.PathValue("artifact"))
		if e != nil {
			return e
		}
		data, e := ReadLocalFile(a.artifactPath(j.ID, artifact.ID), 8<<20)
		if e != nil || len(data) != artifact.Size || fileHash(data) != artifact.Hash {
			return fail(410, "artifact_missing", "The output file is no longer available.")
		}
		w.Header().Set("Content-Type", artifact.MediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Name}))
		w.Write(data)
		return nil
	}))
}
func findArtifact(job *Job, id string) (Artifact, error) {
	for _, item := range job.Artifacts {
		if item.ID == id {
			return item, nil
		}
	}
	return Artifact{}, fail(404, "not_found", "Artifact not found.")
}
func (a *App) artifactPath(job, id string) string {
	return filepath.Join(a.dir, "artifacts", job, id+".png")
}
func (a *App) saveNativeImage(jobID, encoded string) (Artifact, error) {
	if len(encoded) > 12<<20 {
		return Artifact{}, errors.New("native image exceeds 8 MiB")
	}
	data, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil {
		return Artifact{}, errors.New("native image did not contain base64 output")
	}
	if e = validateProviderFile("image/png", data); e != nil {
		return Artifact{}, e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	job := findJob(&a.state, jobID)
	if job == nil || job.Status != "running" {
		return Artifact{}, errors.New("request ended before image completed")
	}
	p := a.state.Pairings[job.PairingID]
	if p == nil || !p.Files {
		return Artifact{}, filePermissionDenied()
	}
	used := int64(0)
	root := filepath.Join(a.dir, "artifacts")
	_ = filepath.WalkDir(root, func(_ string, item os.DirEntry, err error) error {
		if err == nil && !item.IsDir() {
			if info, e := item.Info(); e == nil {
				used += info.Size()
			}
		}
		return nil
	})
	if used+int64(len(data)) > 64<<20 {
		return Artifact{}, fail(409, "artifact_limit", "Clear finished history to free output storage.")
	}
	id := secret()
	artifact := Artifact{ID: id, Name: "image.png", MediaType: "image/png", Size: len(data), Hash: fileHash(data)}
	if e = os.MkdirAll(filepath.Join(root, jobID), 0700); e != nil {
		return Artifact{}, e
	}
	if e = writeLocalFile(a.artifactPath(jobID, id), data); e != nil {
		return Artifact{}, e
	}
	return artifact, nil
}
