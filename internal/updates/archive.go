package updates

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extract accepts regular files and directories only. The destination must be new.
// macOS frameworks contain symlinks and are handled by Sparkle, not this extractor.
func Extract(archive, destination string, zipped bool) error {
	if e := os.Mkdir(destination, 0700); e != nil {
		return e
	}
	root, e := os.OpenRoot(destination)
	if e != nil {
		return e
	}
	defer root.Close()
	var total int64
	count := 0
	seen := map[string]bool{}
	put := func(name string, size int64, mode os.FileMode, body io.Reader) error {
		count++
		if count > 10000 || size < 0 || size > MaxExpanded-total {
			return errors.New("update archive exceeds limits")
		}
		if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
			return errors.New("unsafe update path")
		}
		clean := path.Clean(name)
		parts := strings.Split(clean, "/")
		for _, part := range parts {
			base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if strings.TrimRight(part, " .") != part || base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
				return errors.New("unsafe platform filename")
			}
		}
		if len(parts) == 1 && mode.IsDir() && parts[0] != ".." && parts[0] != "." {
			return nil
		}
		if len(parts) < 2 || parts[0] == ".." || parts[0] == "." || strings.Contains(name, "../") {
			return errors.New("unsafe update path")
		}
		name = strings.Join(parts[1:], "/")
		if name == "" || name == "." {
			return nil
		}
		if seen[name] {
			return errors.New("duplicate update file")
		}
		seen[name] = true
		if mode.IsDir() {
			return root.MkdirAll(name, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("links and special files are forbidden in updates")
		}
		if e := root.MkdirAll(path.Dir(name), 0700); e != nil {
			return e
		}
		f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		n, copyErr := io.Copy(f, io.LimitReader(body, size+1))
		if copyErr == nil && n != size {
			copyErr = errors.New("invalid update file length")
		}
		if copyErr == nil {
			copyErr = f.Sync()
		}
		if copyErr == nil {
			copyErr = f.Chmod(0755&mode.Perm() | 0600)
		}
		closeErr := f.Close()
		total += n
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if zipped {
		z, e := zip.OpenReader(archive)
		if e != nil {
			return e
		}
		defer z.Close()
		for _, f := range z.File {
			if f.UncompressedSize64 > uint64(MaxExpanded) {
				return errors.New("oversized update file")
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = put(f.Name, int64(f.UncompressedSize64), f.Mode(), r)
			r.Close()
			if e != nil {
				return e
			}
		}
	} else {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		gz, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer gz.Close()
		t := tar.NewReader(gz)
		for {
			h, e := t.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
				return errors.New("links and special files are forbidden in updates")
			}
			if e = put(h.Name, h.Size, h.FileInfo().Mode(), t); e != nil {
				return e
			}
		}
	}
	return nil
}
func regular(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return errors.New("update file must be regular")
	}
	return nil
}
func Inside(root, p string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
