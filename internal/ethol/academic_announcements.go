package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"time"
)

type AnnouncementItem struct {
	ID               int    `json:"id"`
	Judul            string `json:"judul"`
	Isi              string `json:"isi"`
	IsiPengumuman    string `json:"isi_pengumuman"`
	WaktuIndonesia   string `json:"waktu_indonesia"`
	TanggalIndonesia string `json:"tanggal_indonesia"`
	IsImportant      int    `json:"is_important"`
	IsPinned         int    `json:"is_pinned"`
}

func (a AnnouncementItem) ItemTime() string {
	if a.WaktuIndonesia != "" {
		return a.WaktuIndonesia
	}
	return a.TanggalIndonesia
}

func (a AnnouncementItem) ItemContent() string {
	if a.Isi != "" {
		return a.Isi
	}
	return a.IsiPengumuman
}

func (am *AcademicManager) GetAnnouncements(ctx context.Context) ([]AnnouncementItem, error) {
	am.mu.RLock()
	if !am.announcementCache.timestamp.IsZero() && time.Since(am.announcementCache.timestamp) < am.ttl {
		items := make([]AnnouncementItem, len(am.announcementCache.items))
		copy(items, am.announcementCache.items)
		am.mu.RUnlock()
		return items, nil
	}
	am.mu.RUnlock()

	endpoint := fmt.Sprintf("%s/api/pengumuman-admin", am.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create announcements req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch announcements: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch announcements failed: HTTP %d", resp.StatusCode)
	}

	var items []AnnouncementItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode announcements: %w", err)
	}

	slices.SortStableFunc(items, func(a, b AnnouncementItem) int {
		if a.IsPinned != b.IsPinned {
			return b.IsPinned - a.IsPinned
		}
		if a.IsImportant != b.IsImportant {
			return b.IsImportant - a.IsImportant
		}
		return b.ID - a.ID
	})

	am.mu.Lock()
	am.announcementCache = announcementCacheEntry{
		items:     items,
		timestamp: time.Now(),
	}
	am.mu.Unlock()

	result := make([]AnnouncementItem, len(items))
	copy(result, items)
	return result, nil
}

func (am *AcademicManager) FormatAnnouncementsText(ctx context.Context) (string, error) {
	items, err := am.GetAnnouncements(ctx)
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "📢 <b>PENGUMUMAN KAMPUS</b>\n\nTidak ada pengumuman aktif saat ini.", nil
	}

	// ponytail: display up to 5 most recent announcements with expandable blockquotes (capped at 750 runes) to avoid Telegram length overflow.
	limit := len(items)
	if limit > 5 {
		limit = 5
	}

	var sb strings.Builder
	sb.Grow(limit * 512)
	sb.WriteString(fmt.Sprintf("📢 <b>PENGUMUMAN KAMPUS (%d)</b>\n\n", len(items)))

	for i := 0; i < limit; i++ {
		item := items[i]
		var badges []string
		if item.IsPinned == 1 {
			badges = append(badges, "📌 [PINNED]")
		}
		if item.IsImportant == 1 {
			badges = append(badges, "🚨 [PENTING]")
		}
		badgeStr := ""
		if len(badges) > 0 {
			badgeStr = strings.Join(badges, " ") + " "
		}

		judul := html.EscapeString(item.Judul)
		tgl := html.EscapeString(item.ItemTime())
		if tgl == "" {
			tgl = "-"
		}

		cleanIsi := stripHTMLTags(item.ItemContent())
		runes := []rune(cleanIsi)
		if len(runes) > 750 {
			cleanIsi = string(runes[:750]) + "..."
		}
		cleanIsi = html.EscapeString(cleanIsi)

		sb.WriteString(fmt.Sprintf("• %s<b>%s</b>\n  📅 %s\n", badgeStr, judul, tgl))
		if cleanIsi != "" {
			sb.WriteString(fmt.Sprintf("  <blockquote expandable>%s</blockquote>\n", cleanIsi))
		}
		if i < limit-1 {
			sb.WriteString("\n")
		}
	}

	if len(items) > limit {
		sb.WriteString(fmt.Sprintf("\n<i>... dan %d pengumuman lainnya di ETHOL.</i>", len(items)-limit))
	}

	return strings.TrimSpace(sb.String()), nil
}

func stripHTMLTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteByte(' ')
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	unescaped := html.UnescapeString(b.String())
	cleaned := strings.Join(strings.Fields(unescaped), " ")
	for _, punct := range []string{".", ",", "!", "?", ";", ":"} {
		cleaned = strings.ReplaceAll(cleaned, " "+punct, punct)
	}
	return cleaned
}
