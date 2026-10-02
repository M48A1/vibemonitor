package server

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"vibemonitor/internal/store"
)

func (s *Server) registerThemeRoutes(mux *http.ServeMux) {
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录管理员"})
			return false
		}
		return true
	}
	fail := func(w http.ResponseWriter, err error) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/admin/themes", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		themes, err := s.store.Themes()
		if err != nil {
			http.Error(w, "could not load themes", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"themes": themes, "selected": s.store.GetConfig().SiteTheme})
	})
	mux.HandleFunc("POST /api/admin/themes", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		if err := r.ParseMultipartForm(store.MaxThemePackageBytes); err != nil {
			fail(w, errors.New("主题包最大 2MB，请上传 ZIP 文件"))
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, errors.New("请选择主题包"))
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, store.MaxThemePackageBytes+1))
		if err != nil {
			fail(w, err)
			return
		}
		t, err := s.store.ImportTheme(data)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, t)
	})
	mux.HandleFunc("POST /api/admin/themes/select", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fail(w, errors.New("无效的主题选择"))
			return
		}
		if err := s.store.SelectTheme(req.ID); err != nil {
			fail(w, err)
			return
		}
		s.wsHub.forceBroadcastNodes()
		writeJSON(w, 200, map[string]string{"status": "success"})
	})
	mux.HandleFunc("DELETE /api/admin/themes/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		if err := s.store.DeleteTheme(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.wsHub.forceBroadcastNodes()
		writeJSON(w, 200, map[string]string{"status": "success"})
	})
	mux.HandleFunc("GET /api/themes/{id}/style.css", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		css, err := s.store.ThemeCSS(id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "could not load theme", 500)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+id+`"`)
		http.ServeContent(w, r, "style.css", time.Time{}, bytes.NewReader([]byte(css)))
	})
	mux.HandleFunc("GET /api/admin/themes/example", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create("theme.json")
		f.Write([]byte(`{"name":"海蓝主题","version":"1.0.0","description":"基于默认主题修改主色","css":"theme.css"}`))
		f, _ = z.Create("theme.css")
		f.Write([]byte(":root { --primary: #0284c7; --border-focus: #0284c7; --background: #dceef5; }\n"))
		z.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="vibemonitor-theme-example.zip"`)
		w.Write(b.Bytes())
	})
}
