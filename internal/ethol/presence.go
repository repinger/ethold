package ethol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrUnauthorized = errors.New("unauthorized: session expired")

type PresenceEngine struct {
	client  *http.Client
	baseURL string
}

func NewPresenceEngine(client *http.Client, baseURL string) *PresenceEngine {
	if baseURL == "" {
		baseURL = "https://ethol.pens.ac.id"
	}
	return &PresenceEngine{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (pe *PresenceEngine) CheckCourse(ctx context.Context, c Course) (string, bool, error) {
	url := fmt.Sprintf("%s/api/presensi/aktif-kuliah?kuliah=%d&jenis_schema=%d", pe.baseURL, c.Nomor, c.JenisSchema)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("create presence check req: %w", err)
	}

	resp, err := pe.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("check presence request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", false, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("check presence HTTP %d", resp.StatusCode)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", false, fmt.Errorf("decode presence check: %w", err)
	}

	key := extractPresenceKey(raw)
	devLog("Presence checked", "course", c.CourseName(), "raw_len", len(raw), "key_found", key != "", "key", key)
	if key != "" {
		return key, true, nil
	}
	return "", false, nil
}

type presenceSubmitPayload struct {
	Kuliah      int    `json:"kuliah"`
	JenisSchema int    `json:"jenis_schema"`
	Mahasiswa   int    `json:"mahasiswa"`
	Key         string `json:"key"`
	KuliahAsal  int    `json:"kuliah_asal"`
}

type presenceSubmitResponse struct {
	Sukses  any `json:"sukses"`
	Success any `json:"success"`
	Status  any `json:"status"`
	Pesan   any `json:"pesan"`
	Message any `json:"message"`
}

func (pe *PresenceEngine) Submit(ctx context.Context, c Course, key string, studentID int) (string, bool, error) {
	payload := presenceSubmitPayload{
		Kuliah:      c.Nomor,
		JenisSchema: c.JenisSchema,
		Mahasiswa:   studentID,
		Key:         key,
		KuliahAsal:  c.KuliahAsal,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", false, fmt.Errorf("marshal presence payload: %w", err)
	}
	devLog("Submitting presence payload", "course", c.CourseName(), "key", key, "student_id", studentID)

	url := pe.baseURL + "/api/presensi/mahasiswa"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", false, fmt.Errorf("create presence submit req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := pe.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("submit presence request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", false, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("submit presence HTTP %d", resp.StatusCode)
	}

	var res presenceSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", false, fmt.Errorf("decode submit response: %w", err)
	}

	msg := "Berhasil"
	if res.Pesan != nil {
		msg = fmt.Sprintf("%v", res.Pesan)
	} else if res.Message != nil {
		msg = fmt.Sprintf("%v", res.Message)
	}

	isSuccess := false
	if b, ok := res.Sukses.(bool); ok && b {
		isSuccess = true
	} else if b, ok := res.Success.(bool); ok && b {
		isSuccess = true
	} else if s := fmt.Sprintf("%v", res.Status); s == "200" || s == "true" {
		isSuccess = true
	} else {
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "sudah") || strings.Contains(lowerMsg, "berhasil") {
			isSuccess = true
		}
	}
	devLog("Presence submitted", "course", c.CourseName(), "is_success", isSuccess, "msg", msg)

	return msg, isSuccess, nil
}

func extractPresenceKey(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	type keyItem struct {
		Key string `json:"key"`
	}

	switch trimmed[0] {
	case '[':
		var list []keyItem
		if err := json.Unmarshal(trimmed, &list); err == nil && len(list) > 0 {
			return list[0].Key
		}
	case '{':
		var single keyItem
		if err := json.Unmarshal(trimmed, &single); err == nil {
			return single.Key
		}
	}

	return ""
}
