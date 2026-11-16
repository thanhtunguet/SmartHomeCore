package mobile

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/storage"
)

func TestInitialAccountLifecycleAndPasswordChange(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf("controller:\n  listen: 127.0.0.1:%d\n", port)
	if err := Start(dir, cfg, nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = Stop(5000) }()

	waitForMobile(t, func() bool {
		if !IsRunning() {
			return false
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	count, err := AccountCount()
	if err != nil || count != 0 {
		t.Fatalf("AccountCount() = %d, %v; want 0", count, err)
	}
	if err := CreateInitialAccount("alice", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := CreateInitialAccount("bob", "correct horse battery staple"); err == nil {
		t.Fatal("second initial account creation unexpectedly succeeded")
	}
	if err := VerifyAccountPassword("alice", "wrong password"); err == nil {
		t.Fatal("wrong password unexpectedly verified")
	}
	if err := ChangeAccountPassword("alice", "wrong password", "a newer correct horse battery staple"); err == nil {
		t.Fatal("password change accepted an incorrect current password")
	}
	if err := ChangeAccountPassword("alice", "correct horse battery staple", "a newer correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAccountPassword("alice", "a newer correct horse battery staple"); err != nil {
		t.Fatalf("new password verification failed: %v", err)
	}
	count, err = AccountCount()
	if err != nil || count != 1 {
		t.Fatalf("AccountCount() = %d, %v; want 1", count, err)
	}

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "homecore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	ok, _, err := auth.NewHasher(auth.DefaultParams).Verify(ctx, "a newer correct horse battery staple", user.PasswordHash)
	if err != nil || !ok {
		t.Fatalf("new password verify = %v, %v", ok, err)
	}
	if _, err := store.UserByUsername(ctx, "bob"); err == nil {
		t.Fatal("unrequested second user exists")
	}
}

func TestStartStopRaceRemainsSerialized(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf("controller:\n  listen: 127.0.0.1:%d\n", port)
	if err := Start(dir, cfg, nil); err != nil {
		t.Fatal(err)
	}
	waitForMobile(t, func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	if err := Stop(5000); err != nil {
		t.Fatal(err)
	}
	if IsRunning() {
		t.Fatal("IsRunning() remained true after Stop")
	}
	if err := Start(dir, cfg, nil); err != nil {
		t.Fatalf("Start after completed Stop: %v", err)
	}
	if err := Stop(5000); err != nil {
		t.Fatal(err)
	}
}

func waitForMobile(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for mobile controller startup: %s", LastError())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
