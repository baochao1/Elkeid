package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bytedance/Elkeid/agent/proto"
)

const (
	scannerSignature = "527c6ea0caac3b0604021de5aa2d34e4b9fae715e5e6cdd37e8f485869f923c2"
	hitText          = "Elkeid local AV demonstration marker 2026\n"
	cleanText        = "An unrelated local text fixture.\n"
	scannerVersion   = "local-demo-1"
	scannerPath      = "/scanner-fixture.tar.gz"
)

type scannerDemo struct {
	config  *proto.Config
	archive []byte
	targets map[string]string
}

func loadScannerDemo(dir, origin string) (*scannerDemo, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	targets := map[string]string{}
	for token, content := range map[string]string{"demo-hit": hitText, "demo-clean": cleanText} {
		name := filepath.Join(dir, token+".txt")
		actual, err := readFixture(name, int64(len(content)))
		if err != nil || string(actual) != content {
			return nil, fmt.Errorf("invalid scanner text fixture %q", name)
		}
		targets[token] = name
	}
	archive, err := readFixture(filepath.Join(dir, "scanner-fixture.tar.gz"), 64<<20)
	if err != nil {
		return nil, err
	}
	dbChecksum, err := scannerDatabaseChecksum(archive)
	if err != nil {
		return nil, err
	}
	detail, err := json.Marshal(map[string]any{
		"version": scannerVersion, "dbname": "fixture", "url": []string{}, "sha256": dbChecksum, "passwd": "",
	})
	if err != nil {
		return nil, err
	}
	return &scannerDemo{
		targets: targets,
		archive: archive,
		config: &proto.Config{
			Name: "scanner", Type: "tar.gz", Version: scannerVersion,
			Sha256: fmt.Sprintf("%x", sha256.Sum256(archive)), Signature: scannerSignature,
			DownloadUrls: []string{origin + scannerPath}, Detail: string(detail),
		},
	}, nil
}

func readFixture(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("unexpected fixture file %q", name)
	}
	return os.ReadFile(name)
}

func scannerDatabaseChecksum(archive []byte) (string, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var scannerSeen bool
	var checksum string
	for {
		item, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if item.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("unexpected scanner archive type for %q", item.Name)
		}
		switch {
		case item.Name == "scanner" && !scannerSeen && item.Size <= 64<<20:
			hash := sha256.New()
			if _, err := io.Copy(hash, reader); err != nil {
				return "", err
			}
			if fmt.Sprintf("%x", hash.Sum(nil)) != scannerSignature {
				return "", errors.New("unexpected scanner release checksum")
			}
			scannerSeen = true
		case item.Name == "tmp/fixture.zip" && checksum == "" && item.Size <= 4096:
			data, err := io.ReadAll(reader)
			if err != nil {
				return "", err
			}
			if err := checkDatabase(data); err != nil {
				return "", err
			}
			checksum = fmt.Sprintf("%x", sha256.Sum256(data))
		default:
			return "", fmt.Errorf("unexpected scanner archive entry %q", item.Name)
		}
	}
	if !scannerSeen || checksum == "" {
		return "", errors.New("scanner archive is missing the verified binary or demo database")
	}
	// Drain gzip to validate its checksum and ensure the tar end is the end of the archive, aside from padding.
	padding, err := io.ReadAll(io.LimitReader(compressed, 64<<10))
	if err != nil {
		return "", err
	}
	if len(padding) == 64<<10 || len(bytes.Trim(padding, "\x00")) != 0 {
		return "", errors.New("unexpected data after scanner archive")
	}
	return checksum, nil
}

func checkDatabase(data []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	expected := map[string]string{
		"version":    scannerVersion + "\n",
		"marker.ndb": fmt.Sprintf("Elkeid.LocalDemo.Marker:0:*:%x\n", hitText),
	}
	for _, item := range archive.File {
		content, ok := expected[item.Name]
		if !ok || item.UncompressedSize64 != uint64(len(content)) || !item.Mode().IsRegular() {
			return fmt.Errorf("unexpected scanner database entry %q", item.Name)
		}
		reader, err := item.Open()
		if err != nil {
			return err
		}
		actual, err := io.ReadAll(io.LimitReader(reader, int64(len(content))+1))
		reader.Close()
		if err != nil || string(actual) != content {
			return fmt.Errorf("unexpected contents of scanner database entry %q", item.Name)
		}
		delete(expected, item.Name)
	}
	if len(expected) != 0 {
		return errors.New("scanner database is missing fixture entries")
	}
	return nil
}

func (d *scannerDemo) sendTask(stream proto.Transfer_TransferServer, token string) error {
	data, err := json.Marshal(map[string]string{"exe": d.targets[token]})
	if err != nil {
		return err
	}
	task := &proto.Task{
		DataType: 6053, ObjectName: "scanner", Token: token,
		Data: string(data),
	}
	return stream.Send(&proto.Command{Task: task})
}
