package store

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const MaxThemePackageBytes = 2 << 20
const MaxThemeCSSBytes = 1 << 20
const MaxCustomThemes = 32
const DefaultTheme = "hex"
const RakugakiTheme = "rakugaki"

func isBuiltinTheme(id string) bool { return id == DefaultTheme || id == RakugakiTheme }

type Theme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CSS         string `json:"-"`
	Builtin     bool   `json:"builtin"`
}

type themeManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CSS         string `json:"css"`
}

func validTheme(t Theme) bool {
	h := sha256.Sum256([]byte(t.CSS))
	return t.ID == "custom-"+hex.EncodeToString(h[:]) && strings.TrimSpace(t.Name) != "" && len(t.Name) <= 120 && len(t.Version) <= 64 && len(t.Description) <= 1000 && len(t.CSS) > 0 && len(t.CSS) <= MaxThemeCSSBytes && utf8.ValidString(t.Name+t.Version+t.Description+t.CSS)
}

// Packages contain exactly a root manifest and one CSS file. Nothing is extracted.
func ParseThemePackage(data []byte) (Theme, error) {
	var t Theme
	if len(data) == 0 || len(data) > MaxThemePackageBytes {
		return t, errors.New("主题包最大 2MB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return t, errors.New("无效的 ZIP 主题包")
	}
	if len(z.File) > 16 {
		return t, errors.New("主题包包含过多文件")
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if len(files) >= 2 || !f.Mode().IsRegular() || strings.Contains(f.Name, "\\") || path.Clean(f.Name) != f.Name || strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, "../") {
			return t, errors.New("主题包只能包含 theme.json 和一个 CSS 文件")
		}
		if _, ok := files[f.Name]; ok {
			return t, errors.New("主题包存在重复文件")
		}
		limit := MaxThemeCSSBytes
		if f.Name == "theme.json" {
			limit = 8192
		}
		if f.UncompressedSize64 > uint64(limit) {
			return t, errors.New("主题文件过大")
		}
		r, e := f.Open()
		if e != nil {
			return t, e
		}
		b, e := io.ReadAll(io.LimitReader(r, int64(limit+1)))
		r.Close()
		if e != nil {
			return t, e
		}
		if len(b) > limit {
			return t, errors.New("主题文件过大")
		}
		files[f.Name] = b
	}
	if !utf8.Valid(files["theme.json"]) {
		return t, errors.New("theme.json 必须使用 UTF-8")
	}
	var m themeManifest
	if err = json.Unmarshal(files["theme.json"], &m); err != nil {
		return t, errors.New("缺少或无效的 theme.json")
	}
	if len(files) != 2 || m.CSS == "theme.json" || !strings.HasSuffix(m.CSS, ".css") {
		return t, errors.New("theme.json 需要指定 CSS 文件")
	}
	css, ok := files[m.CSS]
	if !ok {
		return t, errors.New("找不到主题 CSS 文件")
	}
	h := sha256.Sum256(css)
	t = Theme{ID: "custom-" + hex.EncodeToString(h[:]), Name: strings.TrimSpace(m.Name), Version: m.Version, Description: m.Description, CSS: string(css)}
	if !validTheme(t) {
		return Theme{}, errors.New("主题名称、说明或 CSS 无效（请使用 UTF-8）")
	}
	return t, nil
}

func (s *Store) Themes() ([]Theme, error) {
	list := []Theme{
		{ID: DefaultTheme, Name: "默认主题", Description: "当前内置外观", Builtin: true},
		{ID: RakugakiTheme, Name: "Rakugaki · 手绘纸感", Version: "1.0.0", Description: "米白纸面、墨线边框、手绘圆角与陶土橙", Builtin: true},
	}
	rows, err := s.sdb.db.Query("SELECT id,name,version,description FROM site_themes ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Theme
		if err := rows.Scan(&t.ID, &t.Name, &t.Version, &t.Description); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
func (s *Store) ThemeCSS(id string) (string, error) {
	var css string
	err := s.sdb.db.QueryRow("SELECT css FROM site_themes WHERE id=?", id).Scan(&css)
	return css, err
}
func (s *Store) ImportTheme(data []byte) (Theme, error) {
	t, err := ParseThemePackage(data)
	if err != nil {
		return t, err
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var count, exists int
	if err = s.sdb.db.QueryRow("SELECT count(*),count(CASE WHEN id=? THEN 1 END) FROM site_themes", t.ID).Scan(&count, &exists); err != nil {
		return t, err
	}
	if count >= MaxCustomThemes && exists == 0 {
		return t, errors.New("最多保存 32 个自定义主题")
	}
	_, err = s.sdb.db.Exec("INSERT INTO site_themes(id,name,version,description,css) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,version=excluded.version,description=excluded.description", t.ID, t.Name, t.Version, t.Description, t.CSS)
	return t, err
}
func (s *Store) SelectTheme(id string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !isBuiltinTheme(id) {
		var n int
		if err := s.sdb.db.QueryRow("SELECT count(*) FROM site_themes WHERE id=?", id).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("主题不存在")
		}
	}
	next := s.config
	next.SiteTheme = id
	return s.commitConfigLocked(next)
}
func (s *Store) DeleteTheme(id string) error {
	if isBuiltinTheme(id) {
		return errors.New("内置主题不能删除")
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec("DELETE FROM site_themes WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("主题不存在")
	}
	next := s.config
	if next.SiteTheme == id {
		next.SiteTheme = DefaultTheme
		writer := &sqliteDB{tx: tx}
		if err = writer.saveConfig(&next); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.config = next
	s.notifyUpdate()
	return nil
}
