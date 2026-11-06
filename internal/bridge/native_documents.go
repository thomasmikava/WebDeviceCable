package bridge

// Only native adapters interpret documents. The transport and file-enabled
// commands/custom programs continue to receive the exact original bytes.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/extrame/xls"
	"github.com/giraffesyo/downmark"
	"github.com/giraffesyo/downmark/convert/doc"
	"github.com/giraffesyo/downmark/convert/docx"
	"github.com/giraffesyo/downmark/convert/pdf"
	"github.com/giraffesyo/downmark/convert/xlsx"
	"github.com/richardlehane/mscfb"
)

const nativeDocumentLimit = 128 << 10

type documentRequest struct {
	Name      string
	MediaType string
	Data      []byte
}
type documentResult struct {
	Text  string
	Error string
}

// RunDocumentHelper is a private, short-lived converter with no tools or source-path
// interface. A malformed parser input cannot hang or panic the desktop process.
func RunDocumentHelper() (err error) {
	defer func() {
		if recover() != nil {
			err = json.NewEncoder(os.Stdout).Encode(documentResult{Error: "invalid document"})
		}
	}()
	var request documentRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 12<<20)).Decode(&request) != nil || len(request.Data) > maxNativeFile {
		return json.NewEncoder(os.Stdout).Encode(documentResult{Error: "invalid document request"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, conversionError := convertNativeDocument(ctx, request)
	result := documentResult{Text: text}
	if conversionError != nil {
		result = documentResult{Error: conversionError.Error()}
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

var documentSlots = make(chan struct{}, 2)

func readNativeDocument(ctx context.Context, file nativeInput) ([]byte, error) {
	select {
	case documentSlots <- struct{}{}:
		defer func() { <-documentSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	data, _ := json.Marshal(documentRequest{file.Name, file.MediaType, file.Data})
	cmd := exec.CommandContext(ctx, exe, "internal-document")
	cmd.Env = []string{"GOMEMLIMIT=128MiB", "GOMAXPROCS=2"}
	cmd.Stdin = bytes.NewReader(data)
	var output boundedDocumentOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	cleanup, startErr := startOwnedCommand(cmd)
	if startErr != nil {
		return nil, errors.New("the attachment reader could not start")
	}
	defer cleanup()
	if err = cmd.Wait(); err != nil {
		return nil, errors.New("the attachment reader failed or exceeded its processing limit")
	}
	var result documentResult
	if json.Unmarshal(output.Bytes(), &result) != nil {
		return nil, errors.New("invalid attachment reader response")
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	if len(result.Text) == 0 || len(result.Text) > nativeDocumentLimit || !utf8.ValidString(result.Text) {
		return nil, errors.New("document text must be nonempty UTF-8 and at most 128 KiB")
	}
	return []byte(result.Text), nil
}

type boundedDocumentOutput struct{ bytes.Buffer }

func (b *boundedDocumentOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("attachment reader output limit")
	}
	return b.Buffer.Write(p)
}

func convertNativeDocument(ctx context.Context, in documentRequest) (string, error) {
	if len(in.Data) == 0 || len(in.Data) > maxNativeFile {
		return "", errors.New("empty or oversized attachment")
	}
	if strings.EqualFold(filepath.Ext(in.Name), ".xls") || in.MediaType == "application/vnd.ms-excel" {
		return readLegacySheet(ctx, in.Data)
	}
	engine := downmark.New()
	switch strings.ToLower(filepath.Ext(in.Name)) {
	case ".pdf", ".doc", ".docx", ".xlsx":
		engine = downmark.New(downmark.WithoutBuiltins())
	}
	switch in.MediaType {
	case "application/pdf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		engine = downmark.New(downmark.WithoutBuiltins())
	}
	doc.Register(engine)
	docx.Register(engine, docx.Options{})
	xlsx.Register(engine)
	pdf.Register(engine, pdf.Options{})
	result, err := engine.Convert(downmark.WithResultLimit(ctx, nativeDocumentLimit-4096), bytes.NewReader(in.Data), downmark.StreamInfo{Filename: in.Name, Extension: strings.ToLower(filepath.Ext(in.Name)), MIMEType: in.MediaType})
	if err != nil {
		return "", fmt.Errorf("cannot read this attachment: %w", err)
	}
	if strings.TrimSpace(result.Markdown) == "" {
		return "", errors.New("this document has no readable text; supply scanned pages as images")
	}
	prefix := "[WDC attachment representation: extracted document text. Formatting, embedded images and charts may be absent; do not claim to have inspected those.]\n"
	if len(result.Warnings) > 0 {
		prefix += "[The document reader reported incomplete content. Ask for the missing content before claiming a complete review.]\n"
	}
	text := prefix + result.Markdown
	if len(text) > nativeDocumentLimit {
		return "", errors.New("document text exceeds 128 KiB; supply smaller documents")
	}
	return text, nil
}

func readLegacySheet(ctx context.Context, data []byte) (string, error) {
	// Validate the compound directory and allocation claims before the legacy BIFF reader.
	if len(data) < 512 || !bytes.Equal(data[:8], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		return "", errors.New("invalid XLS compound document")
	}
	sectors := uint32(len(data) / 512)
	for _, at := range []int{40, 44, 64, 72} {
		if binary.LittleEndian.Uint32(data[at:]) > sectors {
			return "", errors.New("invalid XLS allocation metadata")
		}
	}
	container, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var total int64
	for _, file := range container.File {
		if file.Size < 0 || file.Size > maxNativeFile {
			return "", errors.New("oversized XLS stream")
		}
		total += file.Size
		if total > 16<<20 {
			return "", errors.New("oversized XLS streams")
		}
	}
	// Bound BIFF allocation claims before the legacy reader allocates its tables.
	var workbookBytes []byte
	for _, file := range container.File {
		if file.Name == "Workbook" || file.Name == "Book" {
			workbookBytes, err = io.ReadAll(io.LimitReader(file, maxNativeFile+1))
			if err != nil {
				return "", err
			}
			break
		}
	}
	if len(workbookBytes) == 0 || len(workbookBytes) > maxNativeFile {
		return "", errors.New("missing XLS workbook stream")
	}
	sheets := 0
	for offset := 0; offset+4 <= len(workbookBytes); {
		kind := binary.LittleEndian.Uint16(workbookBytes[offset:])
		size := int(binary.LittleEndian.Uint16(workbookBytes[offset+2:]))
		offset += 4
		if size > 8224 || offset+size > len(workbookBytes) {
			return "", errors.New("invalid XLS record length")
		}
		if kind == 0x002f {
			return "", errors.New("encrypted XLS documents are unsupported")
		}
		if kind == 0x0085 {
			sheets++
			if sheets > 100 {
				return "", errors.New("XLS must have at most 100 sheets")
			}
		}
		if kind == 0x00fc && (size < 8 || binary.LittleEndian.Uint32(workbookBytes[offset+4:]) > 32768) {
			return "", errors.New("XLS shared string table exceeds the processing limit")
		}
		offset += size
	}
	workbook, err := xls.OpenReader(bytes.NewReader(data), "utf-8")
	if err != nil || workbook == nil {
		return "", errors.New("invalid XLS workbook")
	}
	if workbook.NumSheets() < 1 || workbook.NumSheets() > 100 {
		return "", errors.New("XLS must have 1–100 sheets")
	}
	var text strings.Builder
	text.WriteString("[WDC attachment representation: saved spreadsheet cell values, in sheet/row/column order. Macros are never executed; charts and formatting are absent.]\n")
	for index := 0; index < workbook.NumSheets(); index++ {
		sheet := workbook.GetSheet(index)
		if sheet == nil {
			return "", errors.New("invalid XLS sheet")
		}
		text.WriteString("\nSheet: " + sheet.Name + "\n")
		for i := 0; i <= int(sheet.MaxRow); i++ {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			row := sheet.Row(i)
			if row == nil {
				continue
			}
			if row.LastCol() > 256 {
				return "", errors.New("invalid XLS column count")
			}
			text.WriteString(fmt.Sprintf("%d\t", i+1))
			for col := 0; col < row.LastCol(); col++ {
				value := row.Col(col)
				if text.Len()+len(value)+2 > nativeDocumentLimit {
					return "", errors.New("spreadsheet text exceeds 128 KiB; supply a smaller range")
				}
				text.WriteString(value)
				text.WriteByte('\t')
			}
			text.WriteByte('\n')
			if text.Len() > nativeDocumentLimit {
				return "", errors.New("spreadsheet text exceeds 128 KiB")
			}
		}
	}
	return text.String(), nil
}

func prepareProviderInputs(ctx context.Context, inputs []nativeInput, provider string) ([]nativeInput, error) {
	out := make([]nativeInput, 0, len(inputs))
	for _, input := range inputs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Claude's native message protocol accepts PDF documents directly,
		// retaining scanned pages/figures instead of flattening them to text.
		if provider == "claude" && (input.MediaType == "application/pdf" || bytes.HasPrefix(input.Data, []byte("%PDF-"))) {
			if len(input.Data) > maxNativeFile || !bytes.HasPrefix(input.Data, []byte("%PDF-")) {
				return nil, fail(400, "unsupported_input", "A valid PDF attachment is required.")
			}
			input.MediaType = "application/pdf"
			out = append(out, input)
			continue
		}
		// Images stay binary. Text is validated as text; other formats are adapted
		// without adding an extension allowlist to upload/admission.
		if strings.HasPrefix(input.MediaType, "image/") || input.MediaType == "text/plain" || input.MediaType == "text/markdown" || input.MediaType == "application/json" || input.MediaType == "text/csv" {
			media := input.MediaType
			if media == "text/csv" {
				media = "text/plain"
			}
			if err := validateProviderFile(media, input.Data); err != nil {
				return nil, fail(400, "unsupported_input", fmt.Sprintf("Cannot read attachment %q: %v", input.Name, err))
			}
		} else {
			data, err := readNativeDocument(ctx, input)
			if err != nil {
				return nil, fail(400, "unsupported_input", fmt.Sprintf("Cannot read attachment %q: %v", input.Name, err))
			}
			input.Data = data
			input.MediaType = "text/plain"
			input.Size = len(data)
			input.Hash = fileHash(data)
		}
		out = append(out, input)
	}
	return out, nil
}
