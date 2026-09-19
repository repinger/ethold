package ethol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type NotificationItem struct {
	IDNotifikasi   string `json:"idNotifikasi"`
	KodeNotifikasi string `json:"kodeNotifikasi"`
	Keterangan     string `json:"keterangan"`
	Status         any    `json:"status"`
}

func (n *NotificationItem) UnmarshalJSON(data []byte) error {
	// ponytail: coercing string|int|float ID; upgrade if API introduces nested ID structures.
	type Alias NotificationItem
	aux := &struct {
		IDNotifikasi any `json:"idNotifikasi"`
		*Alias
	}{
		Alias: (*Alias)(n),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	switch val := aux.IDNotifikasi.(type) {
	case string:
		n.IDNotifikasi = strings.TrimSpace(val)
	case float64:
		n.IDNotifikasi = strconv.FormatInt(int64(val), 10)
	case int:
		n.IDNotifikasi = strconv.Itoa(val)
	default:
		n.IDNotifikasi = ""
	}
	return nil
}

func (am *AcademicManager) PollNotifications(
	ctx context.Context,
	onPresenceNotif func(keterangan string),
	onTaskNotif func(keterangan string),
	onOtherNotif func(kode, keterangan string),
) error {
	checkURL := fmt.Sprintf("%s/api/notifikasi/mahasiswa-belum-baca", am.baseURL)
	reqCheck, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return fmt.Errorf("create check unread req: %w", err)
	}

	respCheck, err := am.client.Do(reqCheck)
	if err != nil {
		return fmt.Errorf("fetch unread count: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respCheck.Body, 4096))
		_ = respCheck.Body.Close()
	}()

	if respCheck.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if respCheck.StatusCode != http.StatusOK {
		return fmt.Errorf("check unread count failed: HTTP %d", respCheck.StatusCode)
	}

	var countData struct {
		Jumlah any `json:"jumlah"`
	}
	if err := json.NewDecoder(io.LimitReader(respCheck.Body, 64*1024)).Decode(&countData); err != nil {
		return fmt.Errorf("decode unread count: %w", err)
	}

	if parseCount(countData.Jumlah) <= 0 {
		return nil
	}

	notifURL := fmt.Sprintf("%s/api/notifikasi/mahasiswa?filterNotif=SEMUA", am.baseURL)
	reqNotif, err := http.NewRequestWithContext(ctx, http.MethodGet, notifURL, nil)
	if err != nil {
		return fmt.Errorf("create fetch notifications req: %w", err)
	}

	respNotif, err := am.client.Do(reqNotif)
	if err != nil {
		return fmt.Errorf("fetch notifications: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respNotif.Body, 4096))
		_ = respNotif.Body.Close()
	}()

	if respNotif.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if respNotif.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch notifications failed: HTTP %d", respNotif.StatusCode)
	}

	var items []NotificationItem
	if err := json.NewDecoder(io.LimitReader(respNotif.Body, 512*1024)).Decode(&items); err != nil {
		return fmt.Errorf("decode notifications: %w", err)
	}

	for _, item := range items {
		if strings.TrimSpace(item.IDNotifikasi) == "" {
			continue
		}
		if item.Status != nil && parseCount(item.Status) != 1 {
			continue
		}

		am.mu.Lock()
		if am.processedNotifIDs == nil {
			am.processedNotifIDs = make(map[string]struct{})
		}
		if _, exists := am.processedNotifIDs[item.IDNotifikasi]; exists {
			am.mu.Unlock()
			continue
		}
		if len(am.processedNotifQueue) < maxNotifHistory {
			am.processedNotifQueue = append(am.processedNotifQueue, item.IDNotifikasi)
		} else {
			oldest := am.processedNotifQueue[am.notifHead]
			delete(am.processedNotifIDs, oldest)
			am.processedNotifQueue[am.notifHead] = item.IDNotifikasi
			am.notifHead = (am.notifHead + 1) % maxNotifHistory
		}
		am.processedNotifIDs[item.IDNotifikasi] = struct{}{}
		am.mu.Unlock()

		_ = am.markNotificationRead(ctx, item.IDNotifikasi)

		kode := strings.ToUpper(strings.TrimSpace(item.KodeNotifikasi))
		switch {
		case strings.Contains(kode, "PRESENSI"):
			if onPresenceNotif != nil {
				onPresenceNotif(item.Keterangan)
			}
		case strings.Contains(kode, "TUGAS"):
			if onTaskNotif != nil {
				onTaskNotif(item.Keterangan)
			}
		default:
			if onOtherNotif != nil {
				onOtherNotif(item.KodeNotifikasi, item.Keterangan)
			} else {
				slog.Debug("Unhandled notification code", "kode", item.KodeNotifikasi, "keterangan", item.Keterangan)
			}
		}
	}

	return nil
}

func (am *AcademicManager) markNotificationRead(ctx context.Context, id string) error {
	readURL := fmt.Sprintf("%s/api/notifikasi/mahasiswa-baca-notif", am.baseURL)
	payload, err := json.Marshal(map[string]string{"idNotifikasi": id})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, readURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := am.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

func (am *AcademicManager) StartNotificationPoller(
	ctx context.Context,
	interval time.Duration,
	ensureAuth func(ctx context.Context) error,
	onPresenceNotif func(keterangan string),
	onTaskNotif func(keterangan string),
	onOtherNotif func(kode, keterangan string),
) {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	poll := func() {
		pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()

		err := am.PollNotifications(pollCtx, onPresenceNotif, onTaskNotif, onOtherNotif)
		if errors.Is(err, ErrUnauthorized) && ensureAuth != nil {
			if reErr := ensureAuth(pollCtx); reErr == nil {
				err = am.PollNotifications(pollCtx, onPresenceNotif, onTaskNotif, onOtherNotif)
			} else {
				err = reErr
			}
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("Notification poller cycle failed", "error", err)
		}
	}

	poll()

	for {
		timer := time.NewTimer(calculatePollerInterval(interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			poll()
		}
	}
}

func calculatePollerInterval(base time.Duration) time.Duration {
	return calculateJitter(base, 0.20)
}
