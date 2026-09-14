package fixtures_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash/crc32"
	"testing"
	"time"

	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/fixtures"
	"github.com/tunnaio/tunna/sig"
)

const file = "../../spec/conformance/fixtures.json"

func TestLoadReadsTheRealFile(t *testing.T) {
	f, err := fixtures.Load(file)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !f.Has("alice") || !f.Has("photos") || f.Has("nobody") {
		t.Errorf("Has: alice=%v photos=%v nobody=%v", f.Has("alice"), f.Has("photos"), f.Has("nobody"))
	}
	if _, ok := f.SigKey("reader"); !ok {
		t.Error("SigKey(reader) missing")
	}
	if _, err := f.Origins(); err != nil {
		t.Errorf("Origins: %v", err)
	}
}

func TestAPIKeysCarryScopesAndAdmin(t *testing.T) {
	f, err := fixtures.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	var admin, scoped, disabled int
	for _, k := range f.APIKeys(time.Now()) {
		switch {
		case k.Admin && k.Disabled:
			disabled++
		case k.Admin:
			admin++
		case len(k.Scopes) > 0:
			scoped++
		default:
			t.Errorf("key %s is neither admin nor scoped; the fixture file should not hold such a key", k.ID)
		}
	}
	if admin == 0 || scoped == 0 || disabled == 0 {
		t.Errorf("admin=%d scoped=%d disabled=%d; the cases need at least one of each", admin, scoped, disabled)
	}
}

func TestObjectRecordsChecksumMatchesTheWire(t *testing.T) {
	// Provisioning must checksum the way a PUT would (ADR-0006), so a case
	// reading a fixture object sees the same ETag a real upload produces.
	want := sig.EncodeCRC32C(crc32.Checksum([]byte("hello, tunna\n"), crc32.MakeTable(crc32.Castagnoli)))
	f, err := fixtures.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objs, err := f.ObjectRecords(context.Background(), blobs, time.Now())
	if err != nil {
		t.Fatalf("ObjectRecords: %v", err)
	}
	var found bool
	for _, o := range objs {
		if o.Bucket == "photos" && o.Key == "hello.txt" {
			found = true
			if o.Checksum != want {
				t.Errorf("hello.txt checksum = %q, want %q", o.Checksum, want)
			}
			if o.ContentType != "text/plain" || o.Size != 13 {
				t.Errorf("hello.txt = %s/%d bytes, want text/plain/13", o.ContentType, o.Size)
			}
		}
		if o.ContentType == "" {
			t.Errorf("%s/%s has no content type; the default must apply", o.Bucket, o.Key)
		}
	}
	if !found {
		t.Error("photos/hello.txt not provisioned")
	}
}

func TestGenerateIsDeterministicAndExact(t *testing.T) {
	a := fixtures.Generate(1, 65536)
	b := fixtures.Generate(1, 65536)
	if len(a) != 65536 || hex.EncodeToString(a[:8]) != hex.EncodeToString(b[:8]) {
		t.Fatal("Generate is not deterministic or not exact in length")
	}
	if hex.EncodeToString(fixtures.Generate(2, 8)) == hex.EncodeToString(a[:8]) {
		t.Error("different seeds produced the same bytes")
	}
	// A short stream is a prefix of a longer one with the same seed.
	if hex.EncodeToString(fixtures.Generate(1, 40)) != hex.EncodeToString(a[:40]) {
		t.Error("Generate(1, 40) is not a prefix of Generate(1, 65536)")
	}
	// Record the digest so a second-language runner has an oracle in the
	// test output if fixtures.json ever gains "check" entries.
	sum := sha256.Sum256(a)
	t.Logf("sha256(Generate(1, 65536)) = %s", hex.EncodeToString(sum[:]))
}
