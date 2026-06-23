package dedup

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDedup_FreshAndDuplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDedup(ctx, 4, time.Minute)

	if !d.Check("a") {
		t.Fatal("first 'a' should be fresh")
	}
	if d.Check("a") {
		t.Fatal("second 'a' should be duplicate")
	}
	if !d.Check("b") {
		t.Fatal("first 'b' should be fresh")
	}
}

func TestDedup_CapacityEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDedup(ctx, 2, time.Minute)

	d.Check("a")
	d.Check("b")
	d.Check("c")

	if !d.Check("a") {
		t.Fatal("'a' should have been evicted by capacity and now be fresh")
	}
}

func TestDedup_TTLEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDedup(ctx, 1024, 50*time.Millisecond)

	d.Check("a")
	if d.Check("a") {
		t.Fatal("immediate re-check should be duplicate")
	}
	time.Sleep(80 * time.Millisecond)
	if !d.Check("a") {
		t.Fatal("after TTL, 'a' should be fresh again")
	}
}

func TestDedup_Concurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := NewDedup(ctx, 10000, time.Minute)

	const N = 200
	results := make(chan bool, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			results <- d.Check(fmt.Sprintf("id-%d", i%10))
		}(i)
	}
	fresh := 0
	for i := 0; i < N; i++ {
		if <-results {
			fresh++
		}
	}
	if fresh != 10 {
		t.Fatalf("expected exactly 10 fresh checks, got %d", fresh)
	}
}
