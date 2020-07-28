package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"

	"github.com/pkg/errors"
)

func createTarball(tarballFilePath string, filePaths []string) error {
	file, err := os.Create(tarballFilePath)
	if err != nil {
		return errors.Wrapf(err, "failed creating tarball file '%s'", tarballFilePath)
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for _, filePath := range filePaths {
		err := addFileToTarWriter(filePath, tarWriter)
		if err != nil {
			return errors.Wrapf(err, "failed adding file '%s' to tarball", filePath)
		}
	}
	return nil
}

func addFileToTarWriter(filePath string, tarWriter *tar.Writer) error {
	file, err := os.Open(filePath)
	if err != nil {
		return errors.Wrapf(err, "failed opening file '%s'", filePath)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return errors.Wrapf(err, "failed getting stat for file '%s'", filePath)
	}

	header := &tar.Header{
		Name:    filePath,
		Size:    stat.Size(),
		Mode:    int64(stat.Mode()),
		ModTime: stat.ModTime(),
	}

	err = tarWriter.WriteHeader(header)
	if err != nil {
		return errors.Wrapf(err, "failed writting header for file '%s'", filePath)
	}

	_, err = io.Copy(tarWriter, file)
	if err != nil {
		return errors.Wrapf(err, "failed copying file '%s' data to the tarball, got error '%s'", filePath)
	}

	return nil
}
