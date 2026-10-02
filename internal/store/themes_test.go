package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func themeZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func testPackage(t *testing.T) []byte {
	return themeZIP(t, map[string]string{"theme.json": `{"name":"蓝色","version":"1.0","description":"测试","css":"theme.css"}`, "theme.css": `:root { --primary: blue; }`})
}
func TestThemePackageValidation(t *testing.T) {
	if _, err := ParseThemePackage(testPackage(t)); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]map[string]string{
		"missing manifest":       {"theme.css": "body{}"},
		"traversal":              {"theme.json": `{"name":"x","css":"../bad.css"}`, "../bad.css": "body{}"},
		"extra script":           {"theme.json": `{"name":"x","css":"theme.css"}`, "theme.css": "body{}", "script.js": "alert(1)"},
		"oversized expanded css": {"theme.json": `{"name":"x","css":"theme.css"}`, "theme.css": strings.Repeat(" ", MaxThemeCSSBytes+1)},
		"invalid utf8":           {"theme.json": `{"name":"x","css":"theme.css"}`, "theme.css": string([]byte{255})},
		"empty css":              {"theme.json": `{"name":"x","css":"theme.css"}`, "theme.css": ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseThemePackage(themeZIP(t, files)); err == nil {
				t.Fatal("accepted invalid package")
			}
		})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		f, _ := z.Create("theme.json")
		f.Write([]byte(`{}`))
	}
	z.Close()
	if _, err := ParseThemePackage(b.Bytes()); err == nil {
		t.Fatal("accepted duplicate")
	}
}
func TestThemePersistenceBackupAndDeletion(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data.db")
	s, err := New(db, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := s.ImportTheme(testPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SelectTheme(theme.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SelectTheme("missing"); err == nil {
		t.Fatal("accepted missing theme")
	}
	if err = s.DeleteTheme(DefaultTheme); err == nil {
		t.Fatal("deleted built-in")
	}
	s.Close()
	s, err = New(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.GetConfig().SiteTheme != theme.ID {
		t.Fatal("selection lost at restart")
	}
	s.Close()
	backup := filepath.Join(dir, "backup.db")
	dest := filepath.Join(dir, "restored.db")
	if err = ExportData(db, backup); err != nil {
		t.Fatal(err)
	}
	if err = ValidateBackup(backup); err != nil {
		t.Fatal(err)
	}
	if err = RestoreData(backup, dest); err != nil {
		t.Fatal(err)
	}
	s, err = New(dest, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.GetConfig().SiteTheme != theme.ID {
		t.Fatal("selection lost in restore")
	}
	css, err := s.ThemeCSS(theme.ID)
	if err != nil || css != theme.CSS {
		t.Fatalf("css lost: %v", err)
	}
	data, _ := json.Marshal(theme)
	if bytes.Contains(data, []byte("--primary")) {
		t.Fatal("CSS leaked in metadata")
	}
	if err = s.DeleteTheme(theme.ID); err != nil {
		t.Fatal(err)
	}
	if s.GetConfig().SiteTheme != DefaultTheme {
		t.Fatal("active deletion did not restore default")
	}
}
func TestLegacyThemeBackup(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "old.db")
	s, err := New(db, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	raw, err := openSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	raw.db.Exec("DROP TABLE site_themes")
	raw.db.Exec("ALTER TABLE config DROP COLUMN site_theme")
	raw.Close()
	dest := filepath.Join(dir, "restored.db")
	if err = RestoreData(db, dest); err != nil {
		t.Fatal(err)
	}
	s, err = New(dest, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.GetConfig().SiteTheme != DefaultTheme {
		t.Fatal("old backup did not restore default")
	}
}

func TestBuiltinThemeLifecycle(t *testing.T) {
	for _, themeID := range []string{RakugakiTheme, Win2000Theme} {
		t.Run(themeID, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "data.db")
			s, err := New(db, "test-password")
			if err != nil {
				t.Fatal(err)
			}
			themes, err := s.Themes()
			if err != nil {
				t.Fatal(err)
			}
			if len(themes) != 3 || themes[0].ID != DefaultTheme || themes[1].ID != RakugakiTheme || !themes[1].Builtin || themes[2].ID != Win2000Theme || !themes[2].Builtin {
				t.Fatalf("builtins: %+v", themes)
			}
			if s.GetConfig().SiteTheme != DefaultTheme {
				t.Fatal("initial appearance changed")
			}
			if err = s.SelectTheme(themeID); err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteTheme(themeID); err == nil {
				t.Fatal("deleted builtin")
			}
			s.Close()
			s, err = New(db, "")
			if err != nil {
				t.Fatal(err)
			}
			if s.GetConfig().SiteTheme != themeID {
				t.Fatal("builtin selection lost on restart")
			}
			s.Close()
			backup := filepath.Join(dir, "backup.db")
			dest := filepath.Join(dir, "restored.db")
			if err = ExportData(db, backup); err != nil {
				t.Fatal(err)
			}
			if err = RestoreData(backup, dest); err != nil {
				t.Fatal(err)
			}
			s, err = New(dest, "")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.GetConfig().SiteTheme != themeID {
				t.Fatal("builtin selection lost on restore")
			}
			if err = s.SelectTheme(DefaultTheme); err != nil {
				t.Fatal(err)
			}
		})
	}
}
