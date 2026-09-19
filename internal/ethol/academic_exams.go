package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type ExamInfo struct {
	Nomor            int    `json:"nomor"`
	Mulai            string `json:"mulai"`
	Selesai          string `json:"selesai"`
	MulaiIndonesia   string `json:"mulai_indonesia"`
	SelesaiIndonesia string `json:"selesai_indonesia"`
}

type ExamItem struct {
	Nomor      int      `json:"nomor"`
	KodeKelas  string   `json:"kode_kelas"`
	Pararel    string   `json:"pararel"`
	Matakuliah any      `json:"matakuliah"`
	Dosen      any      `json:"dosen"`
	Ruang      string   `json:"ruang"`
	Ujian      ExamInfo `json:"ujian"`
}

func (ei ExamItem) CourseName() string {
	return parseCourseNameVal(ei.Matakuliah)
}

func (ei ExamItem) LecturerName() string {
	if ei.Dosen == nil {
		return ""
	}
	if s, ok := ei.Dosen.(string); ok && s != "" {
		return s
	}
	if m, ok := ei.Dosen.(map[string]any); ok {
		nama, _ := m["nama"].(string)
		gelarDpn, _ := m["gelar_dpn"].(string)
		gelarBlk, _ := m["gelar_blk"].(string)
		var b strings.Builder
		if gelarDpn = strings.TrimSpace(gelarDpn); gelarDpn != "" {
			b.WriteString(gelarDpn)
			b.WriteByte(' ')
		}
		if nama = strings.TrimSpace(nama); nama != "" {
			b.WriteString(nama)
		}
		if gelarBlk = strings.TrimSpace(gelarBlk); gelarBlk != "" {
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString(gelarBlk)
		}
		return b.String()
	}
	return ""
}

func (am *AcademicManager) GetExams(ctx context.Context, tahun, semester, jenis int) ([]ExamItem, error) {
	key := fmt.Sprintf("%d-%d-%d", tahun, semester, jenis)

	am.mu.RLock()
	if entry, ok := am.examCache[key]; ok && time.Since(entry.timestamp) < am.ttl {
		items := make([]ExamItem, len(entry.items))
		copy(items, entry.items)
		am.mu.RUnlock()
		return items, nil
	}
	am.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/ujian/daftar-ujian?tahun=%d&semester=%d&jenis=%d", am.baseURL, tahun, semester, jenis)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create exams req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch exams: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch exams failed: HTTP %d", resp.StatusCode)
	}

	var items []ExamItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode exams: %w", err)
	}

	slices.SortStableFunc(items, func(a, b ExamItem) int {
		if a.Ujian.Mulai != "" && b.Ujian.Mulai != "" {
			return strings.Compare(a.Ujian.Mulai, b.Ujian.Mulai)
		}
		if a.Ujian.Mulai != "" {
			return -1
		}
		if b.Ujian.Mulai != "" {
			return 1
		}
		return strings.Compare(a.CourseName(), b.CourseName())
	})

	am.mu.Lock()
	am.examCache[key] = examCacheEntry{
		items:     items,
		timestamp: time.Now(),
	}
	am.mu.Unlock()

	result := make([]ExamItem, len(items))
	copy(result, items)
	return result, nil
}

func (am *AcademicManager) FormatExamsText(ctx context.Context, tahun, semester int) (string, error) {
	var (
		utsItems, uasItems []ExamItem
		errUts, errUas     error
		wg                 sync.WaitGroup
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(2)
	go func() {
		defer wg.Done()
		utsItems, errUts = am.GetExams(ctx, tahun, semester, 1)
		if errUts != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		uasItems, errUas = am.GetExams(ctx, tahun, semester, 2)
		if errUas != nil {
			cancel()
		}
	}()
	wg.Wait()

	if errUts != nil {
		return "", errUts
	}
	if errUas != nil {
		return "", errUas
	}

	if len(utsItems) == 0 && len(uasItems) == 0 {
		return "📝 <b>JADWAL UJIAN ONLINE</b>\n\nBelum ada jadwal UTS maupun UAS untuk semester aktif.", nil
	}

	var sb strings.Builder
	sb.WriteString("📝 <b>JADWAL UJIAN ONLINE</b>\n")

	appendExamSection := func(title string, items []ExamItem) {
		if len(items) == 0 {
			return
		}
		sb.WriteString(fmt.Sprintf("\n📌 <b>%s</b>\n", title))
		for _, it := range items {
			mk := html.EscapeString(it.CourseName())
			if mk == "" {
				mk = "Mata Kuliah"
			}
			mulai := it.Ujian.MulaiIndonesia
			if mulai == "" {
				mulai = it.Ujian.Mulai
			}
			selesai := it.Ujian.SelesaiIndonesia
			if selesai == "" {
				selesai = it.Ujian.Selesai
			}
			waktuStr := "-"
			if mulai != "" || selesai != "" {
				waktuStr = fmt.Sprintf("%s – %s", html.EscapeString(mulai), html.EscapeString(selesai))
			}

			sb.WriteString(fmt.Sprintf("• <b>%s</b>\n  🕒 %s", mk, waktuStr))
			if it.Ruang != "" {
				sb.WriteString(fmt.Sprintf(" | 📍 %s", html.EscapeString(it.Ruang)))
			}
			if dosen := it.LecturerName(); dosen != "" {
				sb.WriteString(fmt.Sprintf(" | 👨‍🏫 %s", html.EscapeString(dosen)))
			}
			sb.WriteString("\n")
		}
	}

	appendExamSection("UJIAN TENGAH SEMESTER (UTS)", utsItems)
	appendExamSection("UJIAN AKHIR SEMESTER (UAS)", uasItems)

	return strings.TrimSpace(sb.String()), nil
}
