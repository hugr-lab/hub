package hubapp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeExtractTarGz_Normal(t *testing.T) {
	dst := t.TempDir()
	data := buildPublishTarGz(map[string]string{
		"SKILL.md":       "name: x\n",
		"scripts/run.py": "print(1)\n",
	})
	if err := safeExtractTarGz(bytes.NewReader(data), dst, maxPublishBytes, maxPublishFiles); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, p := range []string{"SKILL.md", "scripts/run.py"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSafeExtractTarGz_RejectsTraversalAndLinks(t *testing.T) {
	cases := map[string]*tar.Header{
		"dotdot":   {Name: "../escape.txt", Typeflag: tar.TypeReg, Size: 1},
		"absolute": {Name: "/etc/evil", Typeflag: tar.TypeReg, Size: 1},
		"symlink":  {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hardlink": {Name: "hl", Typeflag: tar.TypeLink, Linkname: "SKILL.md"},
	}
	for name, hdr := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			_ = tw.WriteHeader(hdr)
			if hdr.Size > 0 {
				_, _ = tw.Write([]byte("x"))
			}
			_ = tw.Close()
			_ = gz.Close()
			if err := safeExtractTarGz(&buf, t.TempDir(), maxPublishBytes, maxPublishFiles); err == nil {
				t.Errorf("%s: extraction accepted an unsafe entry", name)
			}
		})
	}
}

func TestSafeExtractTarGz_Caps(t *testing.T) {
	big := buildPublishTarGz(map[string]string{"big.txt": strings.Repeat("A", 4096)})
	if err := safeExtractTarGz(bytes.NewReader(big), t.TempDir(), 1024, maxPublishFiles); err == nil {
		t.Error("size cap not enforced")
	}
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("f%d.txt", i)] = "x"
	}
	if err := safeExtractTarGz(bytes.NewReader(buildPublishTarGz(files)), t.TempDir(), maxPublishBytes, 3); err == nil {
		t.Error("file-count cap not enforced")
	}
}

func buildPublishTarGz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
