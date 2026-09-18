// package-release generates a Homebrew formula and source AUR package from
// GoReleaser artifacts. It does not publish anything or need registry credentials.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

var validVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)

type releaseData struct {
	Version        string `json:"version"`
	Tag            string
	PackageVersion string
	Checksums      map[string]string
}

func main() {
	dist := flag.String("dist", "dist", "GoReleaser output directory")
	flag.Parse()
	if err := generate(*dist); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(dist string) error {
	metadata, err := os.ReadFile(filepath.Clean(filepath.Join(dist, "metadata.json")))
	if err != nil {
		return err
	}
	var data releaseData
	if err := json.Unmarshal(metadata, &data); err != nil {
		return err
	}
	if !validVersion.MatchString(data.Version) {
		return fmt.Errorf("invalid release version %q", data.Version)
	}
	data.Tag = "v" + data.Version
	data.PackageVersion = strings.ReplaceAll(data.Version, "-", ".")
	checksums, err := readChecksums(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		return err
	}
	data.Checksums = make(map[string]string)
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64", "source"} {
		ext := ".tar.gz"
		if strings.HasPrefix(platform, "windows_") {
			ext = ".zip"
		}
		name := "playplace_" + data.Version + "_" + platform + ext
		want, ok := checksums[name]
		if !ok {
			return fmt.Errorf("missing checksum for %s", name)
		}
		got, err := fileChecksum(filepath.Join(dist, name))
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
		data.Checksums[platform] = want
	}

	output := filepath.Join(dist, "packages")
	if err := os.MkdirAll(output, 0750); err != nil {
		return err
	}
	var manifest strings.Builder
	for _, file := range []struct{ template, name string }{
		{"playplace.rb.tmpl", "playplace.rb"},
		{"PKGBUILD.tmpl", "PKGBUILD"},
		// GitHub rewrites leading-dot asset names; users rename this for AUR submission.
		{"SRCINFO.tmpl", "SRCINFO"},
	} {
		tmpl, err := template.ParseFS(templates, "templates/"+file.template)
		if err != nil {
			return err
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(output, file.name), out.Bytes(), 0600); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(out.Bytes()), file.name)
	}
	return os.WriteFile(filepath.Join(output, "packaging_checksums.txt"), []byte(manifest.String()), 0600)
}

func readChecksums(path string) (map[string]string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid checksum line")
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 for %s", fields[1])
		}
		if _, exists := result[fields[1]]; exists {
			return nil, fmt.Errorf("duplicate checksum for %s", fields[1])
		}
		result[fields[1]] = strings.ToLower(fields[0])
	}
	return result, scanner.Err()
}

func fileChecksum(path string) (string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
