package servicekey

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// REQ-AUTH-024..026: rotation validates the old key, replaces it, and survives a lost response.

func (e *env) rotate(oldID string, ttl time.Duration) (Rotation, error) {
	return e.svc.Rotate(context.Background(), Caller{UserID: e.user, Username: "alice", ViaKeyID: oldID}, oldID, ttl)
}

func TestRotateIssuesANewKeyAndRetiresTheOldOneAfterTheGrace(t *testing.T) {
	e := newEnv(t)
	old := e.create("agent", ScopeMCP, 90*24*time.Hour)
	e.advance(time.Hour)

	rot, err := e.rotate(old.Key.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	next := rot.Key
	if next.ID == old.Key.ID || next.UserID != e.user || next.Name != "agent" || next.Scope != ScopeMCP {
		t.Fatalf("successor: %+v", next)
	}
	if next.TTL != 90*24*time.Hour || !next.ExpiresAt.Equal(e.clock().Add(90*24*time.Hour)) {
		t.Fatalf("the successor must keep the old ttl, counted from now: %v until %v", next.TTL, next.ExpiresAt)
	}
	if rot.Credential == old.Credential || rot.PreviousID != old.Key.ID || rot.Replayed ||
		!rot.PreviousValidUntil.Equal(e.clock().Add(RotationGrace)) {
		t.Fatalf("rotation: %+v", rot)
	}
	if p := e.mustAuth(rot.Credential); p.KeyID != next.ID || p.Scope != ScopeMCP {
		t.Fatalf("new key principal: %+v", p)
	}

	// The old key still works for the grace window and is then dead.
	e.mustAuth(old.Credential)
	e.advance(RotationGrace - time.Millisecond)
	e.mustAuth(old.Credential)
	e.advance(time.Millisecond)
	e.mustReject(old.Credential)
	e.mustAuth(rot.Credential)

	keys, _ := e.svc.List(context.Background(), e.user)
	byID := map[string]Key{}
	for _, k := range keys {
		byID[k.ID] = k
	}
	if byID[old.Key.ID].Status(e.clock()) != StatusRotated || byID[old.Key.ID].RotatedTo != next.ID ||
		byID[next.ID].Status(e.clock()) != StatusActive {
		t.Fatalf("keys after rotation: %+v", keys)
	}
}

func TestRotateCanChooseANewTTL(t *testing.T) {
	e := newEnv(t)
	old := e.create("agent", ScopeLegacy, 0)
	rot, err := e.rotate(old.Key.ID, 24*time.Hour)
	if err != nil || rot.Key.TTL != 24*time.Hour || !rot.Key.ExpiresAt.Equal(e.clock().Add(24*time.Hour)) {
		t.Fatalf("%+v %v", rot.Key, err)
	}
	for _, bad := range []time.Duration{time.Second, MaxTTL + time.Second, -time.Hour} {
		var verr *ValidationError
		if _, err := e.rotate(rot.Key.ID, bad); !errors.As(err, &verr) {
			t.Errorf("ttl %v: %v", bad, err)
		}
	}
	// A rejected rotation changed nothing.
	e.mustAuth(rot.Credential)
	if keys, _ := e.svc.List(context.Background(), e.user); len(keys) != 2 {
		t.Fatalf("%d keys", len(keys))
	}
}

// The credential of a key is a function of its row, so a repeated rotation hands out the very same
// successor: a client that lost the response simply tries again (REQ-AUTH-026).
func TestRotateRetryInsideTheGraceReturnsTheSameSuccessor(t *testing.T) {
	e := newEnv(t)
	old := e.create("agent", ScopeMCP, 0)
	first, err := e.rotate(old.Key.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(RotationGrace / 2)
	again, err := e.rotate(old.Key.ID, time.Hour) // a ttl on a retry is ignored
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Key.ID != first.Key.ID || again.Credential != first.Credential {
		t.Fatalf("retry returned another key:\nfirst %+v\nagain %+v", first, again)
	}
	if !again.PreviousValidUntil.Equal(first.PreviousValidUntil) {
		t.Fatalf("grace moved: %v vs %v", again.PreviousValidUntil, first.PreviousValidUntil)
	}
	if n := e.countKeys(); n != 2 {
		t.Fatalf("%d keys, want the old one and one successor", n)
	}
	// After the grace the old key cannot rotate any more.
	e.advance(RotationGrace)
	if _, err := e.rotate(old.Key.ID, 0); !errors.Is(err, ErrInactive) {
		t.Fatalf("rotating past the grace: %v", err)
	}
}

func TestRotateRefusesAKeyThatIsNotValid(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	revoked := e.create("revoked", ScopeMCP, 0)
	if err := e.svc.Revoke(ctx, e.caller(), revoked.Key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.rotate(revoked.Key.ID, 0); !errors.Is(err, ErrInactive) {
		t.Fatalf("revoked key: %v", err)
	}

	expired := e.create("expired", ScopeMCP, time.Hour)
	e.advance(time.Hour)
	if _, err := e.rotate(expired.Key.ID, 0); !errors.Is(err, ErrInactive) {
		t.Fatalf("expired key: %v", err)
	}

	// Someone else's key id (or an unknown one) is not found.
	live := e.create("live", ScopeMCP, 0)
	mallory := Caller{UserID: e.addUser("mallory"), Username: "mallory"}
	if _, err := e.svc.Rotate(ctx, mallory, live.Key.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign key: %v", err)
	}
	if n := e.countKeys(); n != 3 {
		t.Fatalf("refused rotations created keys: %d", n)
	}
}

// A retry whose successor was revoked meanwhile must not resurrect it.
func TestRotateRetryFailsWhenTheSuccessorIsGone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.create("agent", ScopeMCP, 0)
	first, _ := e.rotate(old.Key.ID, 0)
	if err := e.svc.Revoke(ctx, e.caller(), first.Key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.rotate(old.Key.ID, 0); !errors.Is(err, ErrRotatedAway) {
		t.Fatalf("revoked successor: %v", err)
	}

	// Same when the successor has itself been rotated: the chain moved on.
	old2 := e.create("agent2", ScopeMCP, 0)
	r1, _ := e.rotate(old2.Key.ID, 0)
	if _, err := e.rotate(r1.Key.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.rotate(old2.Key.ID, 0); !errors.Is(err, ErrRotatedAway) {
		t.Fatalf("superseded successor: %v", err)
	}
}

func TestConcurrentRotationsYieldOneSuccessor(t *testing.T) {
	e := newEnv(t)
	old := e.create("agent", ScopeMCP, 0)

	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.rotate(old.Key.ID, 0)
			ids[i], errs[i] = r.Key.ID, err
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("rotation %d: %q %v (first %q)", i, ids[i], errs[i], ids[0])
		}
	}
	if got := e.countKeys(); got != 2 {
		t.Fatalf("%d keys after %d concurrent rotations, want 2", got, n)
	}
}

func TestRotationIsAuditedWithoutSecrets(t *testing.T) {
	e := newEnv(t)
	old := e.create("agent", ScopeMCP, 0)
	rot, err := e.rotate(old.Key.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.rotate(old.Key.ID, 0); err != nil { // the replay is recorded as such
		t.Fatal(err)
	}
	rows := e.auditRows("service_key.rotate")
	if len(rows) != 2 {
		t.Fatalf("audit rows: %v", rows)
	}
	if !strings.Contains(rows[0], old.Key.ID) || !strings.Contains(rows[0], rot.Key.ID) || !strings.Contains(rows[0], `"via_key_id":"`+old.Key.ID+`"`) {
		t.Fatalf("first row: %s", rows[0])
	}
	if !strings.Contains(rows[1], `"replayed":true`) {
		t.Fatalf("second row: %s", rows[1])
	}
	for _, r := range append(rows, e.auditRows("service_key.create")...) {
		for _, secret := range []string{old.Credential, rot.Credential} {
			if strings.Contains(r, secret) || strings.Contains(r, strings.Split(secret, ".")[2]) {
				t.Fatalf("audit row leaks a credential: %s", r)
			}
		}
	}
}

func (e *env) countKeys() int {
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM service_account_keys`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
