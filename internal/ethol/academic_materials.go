package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type MaterialItem struct {
	ID               int    `json:"id"`
	Title            string `json:"title"`
	Judul            string `json:"judul"`
	Path             string `json:"path"`
	Tipe             int    `json:"tipe"`
	CreatedIndonesia string `json:"created_indonesia"`
	KuliahID         int    `json:"kuliah_id"`
	CourseName       string `json:"matkul"`
}

func (m MaterialItem) ItemTitle() string {
	if m.Title != "" {
		return m.Title
	}
	return m.Judul
}

type VideoItem = MaterialItem

func (am *AcademicManager) fetchMaterials(ctx context.Context, c Course) ([]MaterialItem, error) {
	materiURL := fmt.Sprintf("%s/api/materi?matakuliah=%d&jenis_schema=%d", am.baseURL, c.Nomor, c.JenisSchema)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, materiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create materi req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch materi: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var items []MaterialItem
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode materi: %w", err)
	}

	am.mu.Lock()
	now := time.Now()
	am.sweepExpiredLocked(now)
	am.materialCache[c.Nomor] = materialCacheEntry{
		items:     items,
		timestamp: now,
	}
	am.mu.Unlock()

	return items, nil
}

func (am *AcademicManager) GetCourseMaterials(ctx context.Context, courses []Course) ([]MaterialItem, error) {
	results := make([][]MaterialItem, len(courses))
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxAcademicConcurrency)
materialLoop:
	for i, c := range courses {
		am.mu.RLock()
		entry, cached := am.materialCache[c.Nomor]
		am.mu.RUnlock()

		if cached && time.Since(entry.timestamp) < am.ttl {
			results[i] = entry.items
			continue
		}

		select {
		case <-ctx.Done():
			break materialLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, crs Course) {
			defer func() {
				<-sem
				wg.Done()
			}()
			items, err := am.fetchMaterials(ctx, crs)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			results[idx] = items
		}(i, c)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	var all []MaterialItem
	for i, c := range courses {
		for _, item := range results[i] {
			if item.KuliahID == 0 {
				item.KuliahID = c.Nomor
			}
			if item.CourseName == "" {
				item.CourseName = c.CourseName()
			}
			all = append(all, item)
		}
	}
	return all, nil
}

func (am *AcademicManager) fetchVideos(ctx context.Context, c Course) ([]VideoItem, error) {
	videoURL := fmt.Sprintf("%s/api/video?kuliah=%d&jenis_schema=%d", am.baseURL, c.Nomor, c.JenisSchema)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create video req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch video: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var items []VideoItem
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode video: %w", err)
	}

	am.mu.Lock()
	now := time.Now()
	am.sweepExpiredLocked(now)
	am.videoCache[c.Nomor] = videoCacheEntry{
		items:     items,
		timestamp: now,
	}
	am.mu.Unlock()

	return items, nil
}

func (am *AcademicManager) GetCourseVideos(ctx context.Context, courses []Course) ([]VideoItem, error) {
	results := make([][]VideoItem, len(courses))
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxAcademicConcurrency)
videoLoop:
	for i, c := range courses {
		am.mu.RLock()
		entry, cached := am.videoCache[c.Nomor]
		am.mu.RUnlock()

		if cached && time.Since(entry.timestamp) < am.ttl {
			results[i] = entry.items
			continue
		}

		select {
		case <-ctx.Done():
			break videoLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, crs Course) {
			defer func() {
				<-sem
				wg.Done()
			}()
			items, err := am.fetchVideos(ctx, crs)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			results[idx] = items
		}(i, c)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	var all []VideoItem
	for i, c := range courses {
		for _, item := range results[i] {
			if item.KuliahID == 0 {
				item.KuliahID = c.Nomor
			}
			if item.CourseName == "" {
				item.CourseName = c.CourseName()
			}
			all = append(all, item)
		}
	}
	return all, nil
}

func (am *AcademicManager) FormatMaterialsText(ctx context.Context, courses []Course) (string, error) {
	if len(courses) == 0 {
		return "📚 <b>MATERI & VIDEO KULIAH</b>\n\nTidak ada mata kuliah yang terdaftar.", nil
	}

	var (
		materials []MaterialItem
		videos    []VideoItem
		errMat    error
		errVid    error
		wg        sync.WaitGroup
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(2)
	go func() {
		defer wg.Done()
		materials, errMat = am.GetCourseMaterials(ctx, courses)
		if errMat != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		videos, errVid = am.GetCourseVideos(ctx, courses)
		if errVid != nil {
			cancel()
		}
	}()
	wg.Wait()

	if errMat != nil {
		return "", errMat
	}
	if errVid != nil {
		return "", errVid
	}

	if len(materials) == 0 && len(videos) == 0 {
		return "📚 <b>MATERI & VIDEO KULIAH</b>\n\nBelum ada materi atau video perkuliahan yang diunggah.", nil
	}

	var sb strings.Builder
	sb.Grow((len(materials) + len(videos)) * 128)
	sb.WriteString(fmt.Sprintf("📚 <b>MATERI & VIDEO KULIAH (%d materi, %d video)</b>\n\n", len(materials), len(videos)))

	courseMatMap := make(map[int][]MaterialItem)
	for _, m := range materials {
		courseMatMap[m.KuliahID] = append(courseMatMap[m.KuliahID], m)
	}
	courseVidMap := make(map[int][]VideoItem)
	for _, v := range videos {
		courseVidMap[v.KuliahID] = append(courseVidMap[v.KuliahID], v)
	}

	hasContent := false
	for _, c := range courses {
		cMats := courseMatMap[c.Nomor]
		cVids := courseVidMap[c.Nomor]
		if len(cMats) == 0 && len(cVids) == 0 {
			continue
		}
		hasContent = true

		sb.WriteString(fmt.Sprintf("📖 <b>%s</b>\n", html.EscapeString(c.CourseName())))
		for _, m := range cMats {
			tag := "📄 File"
			if m.Tipe == 2 {
				tag = "🔗 Link"
			}
			tgl := m.CreatedIndonesia
			if tgl == "" {
				tgl = "-"
			}
			title := m.ItemTitle()
			if title == "" {
				title = "Materi Kuliah"
			}
			sb.WriteString(fmt.Sprintf("• [%s] <b>%s</b> (%s)\n", tag, html.EscapeString(title), html.EscapeString(tgl)))
		}
		for _, v := range cVids {
			tgl := v.CreatedIndonesia
			if tgl == "" {
				tgl = "-"
			}
			title := v.ItemTitle()
			if title == "" {
				title = "Video Kuliah"
			}
			sb.WriteString(fmt.Sprintf("• [🎥 Video] <b>%s</b> (%s)\n", html.EscapeString(title), html.EscapeString(tgl)))
		}
		sb.WriteString(fmt.Sprintf("   🔗 https://ethol.pens.ac.id/mahasiswa/matakuliah/%d/materi\n\n", c.Nomor))
	}

	if !hasContent {
		return "📚 <b>MATERI & VIDEO KULIAH</b>\n\nBelum ada materi atau video perkuliahan yang diunggah.", nil
	}

	return strings.TrimSpace(sb.String()), nil
}
