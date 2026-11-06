package bridge

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDocumentFormats(t *testing.T) {
	for _, name := range []string{"notes.pdf", "notes.doc", "notes.docx", "tasks.xls", "tasks.xlsx"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "attachments", name))
			if err != nil {
				t.Fatal(err)
			}
			text, err := convertNativeDocument(t.Context(), documentRequest{Name: name, Data: data})
			if err != nil || !strings.Contains(text, "saffron") {
				t.Fatalf("%s: %q, %v", name, text, err)
			}
		})
	}
}
func TestNativeDocumentRejection(t *testing.T) {
	for _, in := range []documentRequest{{Name: "broken.doc", Data: []byte("not a Word file")}, {Name: "broken.xls", Data: []byte("not a spreadsheet")}, {Name: "broken.pdf", Data: []byte("%PDF-invalid")}, {Name: "unknown.bin", Data: []byte{0xff, 0, 0, 0, 1}}} {
		if _, err := convertNativeDocument(t.Context(), in); err == nil {
			t.Fatalf("accepted unreadable %s", in.Name)
		}
	}
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	file, _ := archive.Create("word/document.xml")
	file.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + strings.Repeat("x", nativeDocumentLimit+1) + `</w:t></w:r></w:p></w:body></w:document>`))
	archive.Close()
	if _, err := convertNativeDocument(t.Context(), documentRequest{Name: "too-large.docx", Data: data.Bytes()}); err == nil {
		t.Fatal("accepted oversized expanded text")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := convertNativeDocument(ctx, documentRequest{Name: "document.pdf", Data: []byte("%PDF-1.7")}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestNativeTextAndImagesStayUnchanged(t *testing.T) {
	for _, media := range []string{"text/plain", "text/markdown", "text/csv", "application/json"} {
		data := []byte("the original saffron bytes")
		inputs, err := prepareProviderInputs(t.Context(), []nativeInput{{ID: "id", Name: "file.txt", MediaType: media, Data: data, Size: len(data)}}, "codex")
		if err != nil || len(inputs) != 1 || !bytes.Equal(inputs[0].Data, data) || inputs[0].MediaType != media {
			t.Fatal(media, err)
		}
	}
	image, err := os.ReadFile("web/claude-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := prepareProviderInputs(t.Context(), []nativeInput{{ID: "image", Name: "original.png", MediaType: "image/png", Data: image, Size: len(image)}}, "codex")
	if err != nil || !bytes.Equal(inputs[0].Data, image) {
		t.Fatal("image original changed", err)
	}
	if _, err := prepareProviderInputs(t.Context(), []nativeInput{{Name: "fake.png", MediaType: "image/png", Data: []byte("fake")}}, "codex"); err == nil {
		t.Fatal("accepted invalid image")
	}
}

func TestNativeDocumentReaderProcess(t *testing.T) {
	for _, name := range []string{"notes.pdf", "notes.doc", "notes.docx", "tasks.xls", "tasks.xlsx"} {
		data, err := os.ReadFile(filepath.Join("testdata", "attachments", name))
		if err != nil {
			t.Fatal(err)
		}
		text, err := readNativeDocument(t.Context(), nativeInput{Name: name, Data: data})
		if err != nil || !bytes.Contains(text, []byte("saffron")) {
			t.Fatal(name, err, string(text))
		}
	}
	if _, err := readNativeDocument(t.Context(), nativeInput{Name: "invalid.xls", Data: []byte("invalid")}); err == nil {
		t.Fatal("invalid helper result accepted")
	}
}

func TestClaudePDFInputPreservesOriginalBytes(t *testing.T) {
	data, err := os.ReadFile("testdata/attachments/notes.pdf")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := prepareProviderInputs(t.Context(), []nativeInput{{Name: "original.pdf", MediaType: "application/octet-stream", Data: data, Size: len(data)}}, "claude")
	if err != nil || len(inputs) != 1 || inputs[0].MediaType != "application/pdf" || !bytes.Equal(inputs[0].Data, data) {
		t.Fatal("PDF was flattened or changed", err)
	}
	message := claudeInput(&conversationTurn{Prompt: "Read this", Inputs: inputs}, "session").(map[string]any)
	blocks := message["message"].(map[string]any)["content"].([]any)
	source := blocks[1].(map[string]any)
	if source["type"] != "document" {
		t.Fatal("PDF was sent as text", source)
	}
}

func TestNativeUploadSlotsCoverConcurrentExecutions(t *testing.T) {
	f := setup(t)
	origin := "https://file-slots.example"
	token, _ := f.pair(t, "File slots", origin, "owner", true)
	grantFiles(t, f, token)
	lease := f.lease(t, origin, token)
	meta := map[string]any{"name": "input.bin", "mediaType": "application/octet-stream", "size": 1, "sha256": fileHash([]byte{1})}
	for index := 0; index < 9; index++ {
		object(t, f.req("POST", "/v2/inputs", origin, token, lease, meta, false), 201)
	}
	// The HTTP fixture creates one secure channel per request. Seed the other
	// completed uploads instead of exhausting that separate channel budget.
	for index := 9; index < 64; index++ {
		id := secret()
		f.a.inputs[id] = &nativeInput{ID: id, Name: "input.bin", MediaType: "application/octet-stream", Size: 1, Data: []byte{1}, Hash: fileHash([]byte{1}), PairingID: token, LeaseID: lease}
	}
	object(t, f.req("POST", "/v2/inputs", origin, token, lease, meta, false), 429)
}
