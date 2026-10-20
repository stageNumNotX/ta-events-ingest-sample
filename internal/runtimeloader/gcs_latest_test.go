package runtimeloader

import (
	"context"
	"testing"
)

func TestReadLatestRejectsEmptyName(t *testing.T) {
	r := &GCSReader{}
	if _, err := r.ReadLatest(context.Background(), "", "obj"); err == nil {
		t.Fatal("empty bucket")
	}
	if _, err := r.ReadLatest(context.Background(), "bucket", ""); err == nil {
		t.Fatal("empty object")
	}
}

func TestReadObjectStillRequiresGeneration(t *testing.T) {
	r := &GCSReader{}
	_, err := r.ReadObject(context.Background(), ObjectPin{
		Bucket: PinBucket,
		Object: PointerObject,
		Size:   PointerSize,
		SHA256: PointerSHA256,
	})
	if err == nil {
		t.Fatal("ReadObject without generation must fail")
	}
}
