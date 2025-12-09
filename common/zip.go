package common

import (
	"archive/zip"
	"fmt"
	"io"
)

func ReadFileFromZip(zip_reader *zip.Reader, name string) ([]byte, error) {
	var chosen_file *zip.File
	for _, file := range zip_reader.File {
		if file.Name == name {
			chosen_file = file
			break
		}
	}

	if chosen_file == nil {
		return []byte{}, fmt.Errorf("could not find file %s in zip", name)
	}

	opened_file, err := chosen_file.Open()
	if err != nil {
		return nil, err
	}

	contents, err := io.ReadAll(opened_file)
	if err != nil {
		return nil, err
	}

	return contents, nil
}
