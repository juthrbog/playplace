package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	metadata, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "metadata.json", string(metadata))
	var checksums strings.Builder
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64", "source"} {
		ext := ".tar.gz"
		if strings.HasPrefix(platform, "windows_") {
			ext = ".zip"
		}
		name := "playplace_" + version + "_" + platform + ext
		writeFixture(t, dir, name, platform)
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(platform)), name)
	}
	writeFixture(t, dir, "checksums.txt", checksums.String())
	return dir
}

func writeFixture(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerate(t *testing.T) {
	for _, version := range []string{"1.2.3", "1.2.3-rc.1", "0.0.0-SNAPSHOT-abc1234"} {
		t.Run(version, func(t *testing.T) {
			dir := fixture(t, version)
			if err := generate(dir); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"playplace.rb", "PKGBUILD", "SRCINFO"} {
				data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "packages", name)))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "/releases/download/v"+version+"/") || strings.Contains(string(data), "<no value>") || strings.Contains(string(data), "SKIP") {
					t.Errorf("invalid package recipe: %s", name)
				}
				if name != "playplace.rb" {
					if !strings.Contains(string(data), fmt.Sprintf("%x", sha256.Sum256([]byte("source")))) {
						t.Errorf("%s does not verify the source archive", name)
					}
					if !strings.Contains(string(data), strings.ReplaceAll(version, "-", ".")) {
						t.Errorf("%s has invalid Arch package version", name)
					}
				}
			}
			formula, err := os.ReadFile(filepath.Join(dir, "packages", "playplace.rb")) // #nosec G304 -- test-generated directory
			if err != nil {
				t.Fatal(err)
			}
			for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
				if !strings.Contains(string(formula), fmt.Sprintf("%x", sha256.Sum256([]byte(platform)))) {
					t.Errorf("formula missing %s checksum", platform)
				}
			}
			manifest, err := readChecksums(filepath.Join(dir, "packages", "packaging_checksums.txt"))
			if err != nil || len(manifest) != 3 {
				t.Fatalf("invalid package checksums: %v, %v", manifest, err)
			}
			for name, want := range manifest {
				got, err := fileChecksum(filepath.Join(dir, "packages", name))
				if err != nil || got != want {
					t.Errorf("%s checksum mismatch: %v", name, err)
				}
			}
		})
	}
}

func TestRejectInvalidVersion(t *testing.T) {
	for _, version := range []string{"", "v1.2.3", "../1.2.3", "1.2.3'; touch unsafe", "1.2.3\n"} {
		dir := t.TempDir()
		data, err := json.Marshal(map[string]string{"version": version})
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, dir, "metadata.json", string(data))
		if err := generate(dir); err == nil || !strings.Contains(err.Error(), "invalid release version") {
			t.Fatalf("accepted invalid version %q: %v", version, err)
		}
	}
}

func TestRejectMissingOrCorruptArtifact(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "missing-checksum", "duplicate-checksum", "bad-checksum"} {
		t.Run(mode, func(t *testing.T) {
			dir := fixture(t, "1.2.3")
			name := "playplace_1.2.3_linux_arm64.tar.gz"
			switch mode {
			case "missing":
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				writeFixture(t, dir, name, "corrupt")
			case "missing-checksum":
				writeFixture(t, dir, "checksums.txt", "")
			case "duplicate-checksum":
				line := fmt.Sprintf("%x  %s\n", sha256.Sum256(nil), name)
				writeFixture(t, dir, "checksums.txt", line+line)
			case "bad-checksum":
				writeFixture(t, dir, "checksums.txt", "not-a-sha256  "+name+"\n")
			}
			if err := generate(dir); err == nil {
				t.Fatalf("accepted %s release artifacts", mode)
			}
			if _, err := os.Stat(filepath.Join(dir, "packages")); !os.IsNotExist(err) {
				t.Fatal("invalid inputs must not produce package recipes")
			}
		})
	}
}
