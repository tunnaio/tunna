package sqlite_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/sqlite"
)

// BenchmarkCreateBucketParallel measures durable writes arriving from many
// goroutines at once, as HTTP handlers would deliver them. With one
// transaction per write, every call pays its own fsync and the writes
// serialize on SQLite's single writer lock. This is the number the writer
// goroutine with group commit (ADR-0002, phase C) has to beat.
//
//	go test ./internal/sqlite -run xxx -bench CreateBucket -benchtime=3s
func BenchmarkCreateBucketParallel(b *testing.B) {
	db, err := sqlite.Open(filepath.Join(b.TempDir(), "bench.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	var seq atomic.Int64
	now := time.Now().UTC()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			err := db.CreateBucket(context.Background(), tunna.Bucket{
				Name:      fmt.Sprintf("b%09d", n),
				CreatedAt: now,
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
