package ethol

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrettyHandler_FormatNoColor(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, &PrettyHandlerOptions{
		Level:   slog.LevelDebug,
		NoColor: true,
	})

	recordTime := time.Date(2026, 9, 16, 15, 4, 5, 0, time.UTC)
	r := slog.NewRecord(recordTime, slog.LevelInfo, "Hello world", 0)
	r.AddAttrs(
		slog.String("key", "value"),
		slog.String("sentence", "hello world"),
		slog.Int("count", 42),
	)

	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got := buf.String()
	want := "15:04:05 [INFO ] Hello world key=value sentence=\"hello world\" count=42\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrettyHandler_FormatColor(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, &PrettyHandlerOptions{
		Level:      slog.LevelDebug,
		ForceColor: true,
	})

	recordTime := time.Date(2026, 9, 16, 15, 4, 5, 0, time.UTC)
	r := slog.NewRecord(recordTime, slog.LevelError, "Error occurred", 0)
	r.AddAttrs(slog.String("err", "boom"))

	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, ansiRed+"[ERROR]"+ansiReset) {
		t.Errorf("expected red error badge in output: %q", got)
	}
	if !strings.Contains(got, ansiDim+"err="+ansiReset+"boom") {
		t.Errorf("expected dimmed key attribute in output: %q", got)
	}
}

func TestPrettyHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, &PrettyHandlerOptions{
		Level:   slog.LevelWarn,
		NoColor: true,
	})

	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Errorf("expected debug to be disabled")
	}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("expected info to be disabled")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Errorf("expected warn to be enabled")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Errorf("expected error to be enabled")
	}
}

func TestPrettyHandler_WithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, &PrettyHandlerOptions{
		Level:   slog.LevelDebug,
		NoColor: true,
	})

	h2 := h.WithAttrs([]slog.Attr{slog.String("app", "ethold")}).WithGroup("scan")

	recordTime := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	r := slog.NewRecord(recordTime, slog.LevelInfo, "Scan completed", 0)
	r.AddAttrs(slog.Int("attended", 2))

	if err := h2.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got := buf.String()
	want := "12:00:00 [INFO ] Scan completed app=ethold scan.attended=2\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrettyHandler_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, &PrettyHandlerOptions{
		Level:   slog.LevelDebug,
		NoColor: true,
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
			r.AddAttrs(slog.Int("id", id))
			_ = h.Handle(context.Background(), r)
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 50 {
		t.Errorf("expected 50 lines, got %d", len(lines))
	}
}

func BenchmarkPrettyHandler(b *testing.B) {
	h := NewPrettyHandler(io.Discard, &PrettyHandlerOptions{
		Level:   slog.LevelInfo,
		NoColor: true,
	})

	ctx := context.Background()
	recordTime := time.Now()
	b.ResetTimer()
	for b.Loop() {
		r := slog.NewRecord(recordTime, slog.LevelInfo, "Benchmark test message", 0)
		r.AddAttrs(slog.String("course", "Matematika"), slog.Int("key", 12345), slog.Duration("duration", time.Second))
		_ = h.Handle(ctx, r)
	}
}

func BenchmarkTextHandler(b *testing.B) {
	//nolint:sloglint // benchmark comparison against slog.NewTextHandler
	h := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	ctx := context.Background()
	recordTime := time.Now()
	b.ResetTimer()
	for b.Loop() {
		r := slog.NewRecord(recordTime, slog.LevelInfo, "Benchmark test message", 0)
		r.AddAttrs(slog.String("course", "Matematika"), slog.Int("key", 12345), slog.Duration("duration", time.Second))
		_ = h.Handle(ctx, r)
	}
}
