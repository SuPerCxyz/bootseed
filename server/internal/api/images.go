package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/anomalyco/bootseed/server/internal/imagesvc"
)

// GET /api/images  |  POST /api/images(按 URL/本地路径添加,需鉴权)
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		idx, err := s.images.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取清单失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, idx)
	case http.MethodPost:
		if !s.requireAuth(w, r) {
			return
		}
		var req struct {
			Mode         string   `json:"mode"` // url|path
			Source       string   `json:"source"`
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			OS           string   `json:"os"`
			Version      string   `json:"version"`
			Architecture string   `json:"architecture"`
			Firmware     []string `json:"firmware"`
			Description  string   `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体无效")
			return
		}
		if req.Mode != "url" && req.Mode != "path" {
			writeErr(w, http.StatusBadRequest, "mode 必须为 url/path")
			return
		}
		jobID, err := s.images.StartAdd(imagesvc.AddSpec{
			Mode: req.Mode, Source: req.Source, ID: req.ID, Name: req.Name,
			OS: req.OS, Version: req.Version, Architecture: req.Architecture,
			Firmware: req.Firmware, Description: req.Description,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

// DELETE /api/images/{id} | PUT /api/images/{id}(需鉴权)
func (s *Server) handleImageItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	if r.Method == http.MethodPut && !s.requireAuth(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/images/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusBadRequest, "非法 id")
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.images.Delete(id); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
		return
	}
	var req struct {
		Name         string   `json:"name"`
		OS           string   `json:"os"`
		Version      string   `json:"version"`
		Architecture string   `json:"architecture"`
		Firmware     []string `json:"firmware"`
		Description  string   `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if err := s.images.Update(id, imagesvc.UpdateSpec{
		Name: req.Name, OS: req.OS, Version: req.Version,
		Architecture: req.Architecture, Firmware: req.Firmware,
		Description: req.Description,
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": id})
}

// GET /api/images/jobs/{jobId}
func (s *Server) handleImageJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/images/jobs/")
	job, ok := s.images.GetJob(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// POST /api/images/upload(multipart:file + 元数据字段;需鉴权)
// 大文件不推荐,但提供该能力.先落临时文件再走与 path 相同的添加流程.
func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "解析上传失败: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "缺少上传文件 file")
		return
	}
	defer file.Close()
	imgDir := filepath.Join(s.cfg.DataRoot, "http", "images")
	tmp, err := os.CreateTemp(imgDir, ".upload-*-"+filepath.Base(hdr.Filename))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建临时文件失败")
		return
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		writeErr(w, http.StatusInternalServerError, "保存上传失败: "+err.Error())
		return
	}
	tmp.Close()

	fw := strings.Split(r.FormValue("firmware"), ",")
	jobID, err := s.images.StartAdd(imagesvc.AddSpec{
		Mode: "upload", Source: tmpPath,
		ID: r.FormValue("id"), Name: r.FormValue("name"), OS: r.FormValue("os"),
		Version: r.FormValue("version"), Architecture: r.FormValue("architecture"),
		Firmware: fw, Description: r.FormValue("description"),
	})
	if err != nil {
		os.Remove(tmpPath)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}
