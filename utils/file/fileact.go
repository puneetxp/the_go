package file

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

type FileAct struct {
	Dir        string
	PublicPath string
	BaseURL    string
}

func Init(prefix string) *FileAct {
	// ensure dir
	if _, err := os.Stat(prefix); os.IsNotExist(err) {
		os.MkdirAll(prefix, 0755)
	}
	return &FileAct{Dir: prefix}
}

func (f *FileAct) Public(publicDir string, prefix string) *FileAct {
	f.BaseURL = prefix
	f.PublicPath = filepath.Join(prefix, publicDir)
	f.Dir = filepath.Join(f.Dir, "public", publicDir)
	os.MkdirAll(f.Dir, 0755)
	return f
}

func (f *FileAct) Upload(file multipart.File, header *multipart.FileHeader, name string) interface{} {
	if header == nil {
		return "Can't Upload"
	}

	filename := header.Filename
	if name != "" {
		filename = name + filepath.Ext(header.Filename)
	}

	targetPath := filepath.Join(f.Dir, filename)

	out, err := os.Create(targetPath)
	if err != nil {
		return "Upload Failed"
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		return "Upload Failed"
	}

	return map[string]string{
		"name":   header.Filename,
		"path":   targetPath,
		"dir":    f.Dir,
		"public": filepath.Join(f.PublicPath, filename),
	}
}
