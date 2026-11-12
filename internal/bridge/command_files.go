package bridge

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

type stagedFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}
type commandFiles struct {
	Inputs          []stagedFile `json:"inputs"`
	OutputDirectory string       `json:"outputDirectory"`
}

func stageCommandFiles(dir string, inputs []nativeInput) (commandFiles, error) {
	out := commandFiles{Inputs: []stagedFile{}, OutputDirectory: filepath.Join(dir, "outputs")}
	if err := os.MkdirAll(out.OutputDirectory, 0700); err != nil {
		return out, err
	}
	for _, input := range inputs {
		name := filepath.Base(strings.ReplaceAll(input.Name, "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = "input"
		}
		path := filepath.Join(dir, "inputs", input.ID, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return out, err
		}
		if err := os.WriteFile(path, input.Data, 0400); err != nil {
			return out, err
		}
		out.Inputs = append(out.Inputs, stagedFile{input.ID, input.Name, path, input.MediaType, input.Size})
	}
	return out, nil
}
func (a *App) collectCommandFiles(job string, dir string) ([]Artifact, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	entries, err := folder.ReadDir(17)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 16 {
		return nil, errors.New("at most 16 output files are allowed")
	}
	out := []Artifact{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, errors.New("output files must be regular files, without directories or links")
		}
		f, err := root.Open(entry.Name())
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxNativeFile {
			f.Close()
			return nil, errors.New("invalid or oversized output file")
		}
		data, err := io.ReadAll(io.LimitReader(f, maxNativeFile+1))
		f.Close()
		if err != nil || len(data) > maxNativeFile {
			return nil, errors.New("output file exceeds 8 MiB")
		}
		media := mime.TypeByExtension(filepath.Ext(entry.Name()))
		if media == "" {
			media = "application/octet-stream"
		}
		artifact, err := a.saveArtifact(job, entry.Name(), media, data)
		if err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, nil
}
func (a *App) saveArtifact(jobID, name, media string, data []byte) (Artifact, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	job := findJob(&a.state, jobID)
	if job == nil || job.Status != "running" {
		return Artifact{}, errors.New("request has ended")
	}
	p := a.state.Pairings[job.PairingID]
	if p == nil || !p.Files {
		return Artifact{}, filePermissionDenied()
	}
	used := a.deliveryArtifactBytes()
	root := filepath.Join(a.dir, "artifacts")
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			used += info.Size()
		}
		return nil
	})
	if err != nil {
		return Artifact{}, err
	}
	if used+int64(len(data)) > 64<<20 {
		return Artifact{}, fail(409, "artifact_limit", "Clear finished history to free output storage.")
	}
	id := secret()
	artifact := Artifact{ID: id, Name: filepath.Base(name), MediaType: media, Size: len(data), Hash: fileHash(data)}
	if err = os.MkdirAll(filepath.Join(root, jobID), 0700); err != nil {
		return Artifact{}, err
	}
	return artifact, writeLocalFile(a.artifactPath(jobID, id), data)
}
func commandFileEnv(files commandFiles) []string {
	data, _ := json.Marshal(files)
	return []string{"WDC_FILES=" + string(data), "WDC_OUTPUT_DIR=" + files.OutputDirectory}
}
