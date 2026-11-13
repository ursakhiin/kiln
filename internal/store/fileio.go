package store

import (
	"os"
	"path/filepath"
)

// tempPrefix starts the names of in-progress writes. Readers skip files
// starting with "." so a crash mid-write never exposes a partial file.
const tempPrefix = ".tmp-"

// writeFileAtomic replaces path with data. Readers see either the old file or
// the complete new one, never a partial write.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeFileExclusive creates path with data, failing with an error matching
// fs.ErrExist if path already exists.
func writeFileExclusive(path string, data []byte) error {
	tmp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	// Unlike rename, link fails if path exists.
	return os.Link(tmp, path)
}

// writeTemp writes data to a new temp file in path's directory and returns
// its name.
func writeTemp(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}
