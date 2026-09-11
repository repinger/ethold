package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"
)

type TaskItem struct {
	Title      string `json:"title"`
	Judul      string `json:"judul"`
	Deadline   string `json:"deadline"`
	DeadlineID string `json:"deadline_indonesia"`
	Submission any    `json:"submission_time"`
	Tutup      any    `json:"tutup"`
	KuliahID   int    `json:"kuliah_id"`
	CourseName string `json:"matkul"`
}

func (am *AcademicManager) fetchTasks(ctx context.Context, c Course) ([]TaskItem, error) {
	taskURL := fmt.Sprintf("%s/api/tugas?kuliah=%d&jenisSchema=%d", am.baseURL, c.Nomor, c.JenisSchema)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, taskURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create tasks req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tasks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var items []TaskItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode tasks: %w", err)
	}

	am.mu.Lock()
	am.taskCache[c.Nomor] = taskCacheEntry{
		items:     items,
		timestamp: time.Now(),
	}
	am.mu.Unlock()

	return items, nil
}

func (am *AcademicManager) GetPendingTasks(ctx context.Context, courses []Course) ([]TaskItem, error) {
	results := make([][]TaskItem, len(courses))
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i, c := range courses {
		am.mu.RLock()
		entry, cached := am.taskCache[c.Nomor]
		am.mu.RUnlock()

		if cached && time.Since(entry.timestamp) < am.ttl {
			results[i] = entry.items
			continue
		}

		wg.Add(1)
		go func(idx int, crs Course) {
			defer wg.Done()
			items, err := am.fetchTasks(ctx, crs)
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

	var pending []TaskItem
	for i, c := range courses {
		for _, item := range results[i] {
			if isTaskSubmitted(item.Submission) || isTaskClosed(item.Tutup) {
				continue
			}
			if item.Title == "" && item.Judul != "" {
				item.Title = item.Judul
			}
			if item.Deadline == "" && item.DeadlineID != "" {
				item.Deadline = item.DeadlineID
			}
			if item.KuliahID == 0 {
				item.KuliahID = c.Nomor
			}
			if item.CourseName == "" {
				item.CourseName = c.CourseName()
			}
			pending = append(pending, item)
		}
	}
	return pending, nil
}

func (am *AcademicManager) FormatTasksText(ctx context.Context, courses []Course) (string, error) {
	tasks, err := am.GetPendingTasks(ctx, courses)
	if err != nil {
		return "", err
	}

	if len(tasks) == 0 {
		return "📝 <b>DAFTAR TUGAS BELUM SELESAI</b>\n\nTidak ada tugas aktif atau semua tugas sudah dikumpulkan! 🎉", nil
	}

	var sb strings.Builder
	sb.Grow(len(tasks) * 128)
	sb.WriteString(fmt.Sprintf("📝 <b>DAFTAR TUGAS BELUM SELESAI (%d)</b>\n\n", len(tasks)))

	for i, task := range tasks {
		title := task.Title
		if title == "" {
			title = task.Judul
		}
		title = html.EscapeString(title)

		matkul := html.EscapeString(task.CourseName)
		deadline := task.Deadline
		if deadline == "" {
			deadline = task.DeadlineID
		}
		if deadline == "" {
			deadline = "-"
		}
		deadline = html.EscapeString(deadline)

		sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n", i+1, title))
		if matkul != "" {
			sb.WriteString(fmt.Sprintf("   📚 %s\n", matkul))
		}
		sb.WriteString(fmt.Sprintf("   ⏰ Deadline: %s\n", deadline))
		if task.KuliahID > 0 {
			sb.WriteString(fmt.Sprintf("   🔗 https://ethol.pens.ac.id/mahasiswa/matakuliah/%d/tugas\n", task.KuliahID))
		}
		if i < len(tasks)-1 {
			sb.WriteString("\n")
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

func isTaskSubmitted(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case float64:
		return val != 0
	case string:
		s := strings.TrimSpace(val)
		if s == "" || s == "null" || s == "0" || strings.EqualFold(s, "false") {
			return false
		}
		return true
	default:
		return true
	}
}

func isTaskClosed(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val == 1
	case float64:
		return int(val) == 1
	case string:
		s := strings.TrimSpace(val)
		return s == "1" || strings.EqualFold(s, "true")
	default:
		return false
	}
}
