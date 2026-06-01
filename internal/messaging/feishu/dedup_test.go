package feishu

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDedup_FreshAndDuplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newDedup(ctx, 4, time.Minute)

	if !d.check("a") {
		t.Fatal("first 'a' should be fresh")
	}
	if d.check("a") {
		t.Fatal("second 'a' should be duplicate")
	}
	if !d.check("b") {
		t.Fatal("first 'b' should be fresh")
	}
}

func TestDedup_CapacityEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newDedup(ctx, 2, time.Minute)

	d.check("a")
	d.check("b")
	d.check("c")

	if !d.check("a") {
		t.Fatal("'a' should have been evicted by capacity and now be fresh")
	}
}

func TestDedup_TTLEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newDedup(ctx, 1024, 50*time.Millisecond)

	d.check("a")
	if d.check("a") {
		t.Fatal("immediate re-check should be duplicate")
	}
	time.Sleep(80 * time.Millisecond)
	if !d.check("a") {
		t.Fatal("after TTL, 'a' should be fresh again")
	}
}

func TestDedup_Concurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newDedup(ctx, 10000, time.Minute)

	const N = 200
	results := make(chan bool, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			results <- d.check(fmt.Sprintf("id-%d", i%10))
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
