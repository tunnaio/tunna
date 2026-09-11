// Package sqlbench is the driver benchmark behind ADR-0002. It is its own Go
// module so its dependencies never enter the server. Run from this directory:
//
//	go test -run xxx -bench . -benchmem -benchtime=2s
//
// Results and the decision they led to are in docs/decisions/0002-metadata-store.md.
package sqlbench

// Driver benchmark for ADR-0002: the two pure-Go SQLite drivers on the
// object-store workload. WAL, synchronous=FULL, one connection.
//
//   PointRead      one SELECT by (bucket, key) over 100k rows  -- the GET hot path
//   InsertSingle   one row per transaction, fsync each          -- PUT without group commit
//   InsertGrouped  100 rows per transaction, one fsync          -- PUT with group commit
//   PrefixScan     1000 keys under one prefix, ordered          -- LIST

import (
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	_ "modernc.org/sqlite"
)

var drivers = []struct{ name, driver string }{
	{"modernc", "sqlite"},
	{"ncruces", "sqlite3"},
}

func open(b *testing.B, driver string) *sql.DB {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := sql.Open(driver, "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(p); err != nil {
			b.Fatalf("%s: %v", p, err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE objects (
		bucket TEXT NOT NULL,
		key    TEXT NOT NULL,
		size   INTEGER NOT NULL,
		PRIMARY KEY (bucket, key)
	) WITHOUT ROWID`); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

func seed(b *testing.B, db *sql.DB, n int) {
	b.Helper()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	st, err := tx.Prepare("INSERT INTO objects (bucket, key, size) VALUES (?, ?, ?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := st.Exec("photos", fmt.Sprintf("2026/%02d/img-%06d.jpg", i%12, i), i); err != nil {
			b.Fatal(err)
		}
	}
	st.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkPointRead(b *testing.B) {
	for _, d := range drivers {
		b.Run(d.name, func(b *testing.B) {
			db := open(b, d.driver)
			seed(b, db, 100_000)
			st, err := db.Prepare("SELECT size FROM objects WHERE bucket = ? AND key = ?")
			if err != nil {
				b.Fatal(err)
			}
			defer st.Close()
			rng := rand.New(rand.NewSource(1))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n := rng.Intn(100_000)
				var size int64
				if err := st.QueryRow("photos", fmt.Sprintf("2026/%02d/img-%06d.jpg", n%12, n)).Scan(&size); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInsertSingle(b *testing.B) {
	for _, d := range drivers {
		b.Run(d.name, func(b *testing.B) {
			db := open(b, d.driver)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx, err := db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := tx.Exec("INSERT INTO objects (bucket, key, size) VALUES (?, ?, ?)", "b", fmt.Sprintf("k-%09d", i), i); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInsertGrouped100(b *testing.B) {
	for _, d := range drivers {
		b.Run(d.name, func(b *testing.B) {
			db := open(b, d.driver)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx, err := db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				st, err := tx.Prepare("INSERT INTO objects (bucket, key, size) VALUES (?, ?, ?)")
				if err != nil {
					b.Fatal(err)
				}
				for j := 0; j < 100; j++ {
					if _, err := st.Exec("b", fmt.Sprintf("k-%09d-%03d", i, j), j); err != nil {
						b.Fatal(err)
					}
				}
				st.Close()
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPrefixScan1000(b *testing.B) {
	for _, d := range drivers {
		b.Run(d.name, func(b *testing.B) {
			db := open(b, d.driver)
			seed(b, db, 100_000)
			st, err := db.Prepare("SELECT key, size FROM objects WHERE bucket = ? AND key >= ? AND key < ? ORDER BY key LIMIT 1000")
			if err != nil {
				b.Fatal(err)
			}
			defer st.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows, err := st.Query("photos", "2026/03/", "2026/04/")
				if err != nil {
					b.Fatal(err)
				}
				n := 0
				for rows.Next() {
					var key string
					var size int64
					if err := rows.Scan(&key, &size); err != nil {
						b.Fatal(err)
					}
					n++
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					b.Fatal(err)
				}
				if n != 1000 {
					b.Fatalf("scanned %d rows, want 1000", n)
				}
			}
		})
	}
}
