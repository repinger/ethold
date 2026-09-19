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

func (am *AcademicManager) addProcessedNotifLocked(id string) bool {
	if am.processedNotifIDs == nil {
		am.processedNotifIDs = make(map[string]struct{})
	}
	if _, exists := am.processedNotifIDs[id]; exists {
		return false
	}
	if len(am.processedNotifQueue) < maxNotifHistory {
		am.processedNotifQueue = append(am.processedNotifQueue, id)
	} else {
		oldest := am.processedNotifQueue[am.notifHead]
		delete(am.processedNotifIDs, oldest)
		am.processedNotifQueue[am.notifHead] = id
		am.notifHead = (am.notifHead + 1) % maxNotifHistory
	}
	am.processedNotifIDs[id] = struct{}{}
	return true
}

func (am *AcademicManager) fetchUnreadNotifications(ctx context.Context) ([]NotificationItem, error) {
	checkURL := fmt.Sprintf("%s/api/notifikasi/mahasiswa-belum-baca", am.baseURL)
	reqCheck, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create check unread req: %w", err)
	}

	respCheck, err := am.client.Do(reqCheck)
	if err != nil {
		return nil, fmt.Errorf("fetch unread count: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respCheck.Body, 4096))
		_ = respCheck.Body.Close()
	}()

	if respCheck.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if respCheck.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check unread count failed: HTTP %d", respCheck.StatusCode)
	}

	var countData struct {
		Jumlah any `json:"jumlah"`
	}
	if err := json.NewDecoder(io.LimitReader(respCheck.Body, 64*1024)).Decode(&countData); err != nil {
		return nil, fmt.Errorf("decode unread count: %w", err)
	}

	if parseCount(countData.Jumlah) <= 0 {
		return nil, nil
	}

	notifURL := fmt.Sprintf("%s/api/notifikasi/mahasiswa?filterNotif=SEMUA", am.baseURL)
	reqNotif, err := http.NewRequestWithContext(ctx, http.MethodGet, notifURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create fetch notifications req: %w", err)
	}

	respNotif, err := am.client.Do(reqNotif)
	if err != nil {
		return nil, fmt.Errorf("fetch notifications: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respNotif.Body, 4096))
		_ = respNotif.Body.Close()
	}()

	if respNotif.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if respNotif.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch notifications failed: HTTP %d", respNotif.StatusCode)
	}

	var items []NotificationItem
	if err := json.NewDecoder(io.LimitReader(respNotif.Body, 512*1024)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode notifications: %w", err)
	}

	return items, nil
}

func (am *AcademicManager) InitNotificationBaseline(ctx context.Context) error {
	items, err := am.fetchUnreadNotifications(ctx)
	if err != nil {
		return err
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	for _, item := range items {
		if strings.TrimSpace(item.IDNotifikasi) == "" {
			continue
		}
		if item.Status != nil && parseCount(item.Status) != 1 {
			continue
		}
		am.addProcessedNotifLocked(item.IDNotifikasi)
	}
	am.notifBaselineDone = true
	return nil
}

func (am *AcademicManager) PollNotifications(
	ctx context.Context,
	onPresenceNotif func(keterangan string),
	onTaskNotif func(keterangan string),
	onOtherNotif func(kode, keterangan string),
) error {
	items, err := am.fetchUnreadNotifications(ctx)
	if err != nil || len(items) == 0 {
		return err
	}

	for _, item := range items {
		if strings.TrimSpace(item.IDNotifikasi) == "" {
			continue
		}
		if item.Status != nil && parseCount(item.Status) != 1 {
			continue
		}

		am.mu.Lock()
		isNew := am.addProcessedNotifLocked(item.IDNotifikasi)
		am.mu.Unlock()
		if !isNew {
			continue
		}

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

	ensureBaseline := func(pollCtx context.Context) error {
		am.mu.RLock()
		done := am.notifBaselineDone
		am.mu.RUnlock()
		if done {
			return nil
		}
		err := am.InitNotificationBaseline(pollCtx)
		if errors.Is(err, ErrUnauthorized) && ensureAuth != nil {
			if reErr := ensureAuth(pollCtx); reErr == nil {
				err = am.InitNotificationBaseline(pollCtx)
			} else {
				err = reErr
			}
		}
		return err
	}

	poll := func() {
		pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()

		am.mu.RLock()
		baselineDone := am.notifBaselineDone
		am.mu.RUnlock()

		if !baselineDone {
			if err := ensureBaseline(pollCtx); err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Warn("Notification poller baseline cycle failed", "error", err)
				}
			}
			return
		}

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
