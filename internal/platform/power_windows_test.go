//go:build windows

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func testPower(enable func() error, initiate func(bool) error) windowsPower {
	return windowsPower{log: slog.New(slog.NewTextHandler(io.Discard, nil)), enablePrivilege: enable, initiate: initiate}
}

func TestWindowsPowerEnablesPrivilegeThenInitiates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		call   func(windowsPower) error
		reboot bool
	}{
		{"poweroff", func(p windowsPower) error { return p.PowerOff(context.Background()) }, false},
		{"reboot", func(p windowsPower) error { return p.Reboot(context.Background()) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			var gotReboot bool
			p := testPower(
				func() error { order = append(order, "enable"); return nil },
				func(r bool) error { order = append(order, "initiate"); gotReboot = r; return nil },
			)
			if err := tc.call(p); err != nil {
				t.Fatal(err)
			}
			if strings.Join(order, ",") != "enable,initiate" || gotReboot != tc.reboot {
				t.Fatalf("order %v reboot %v", order, gotReboot)
			}
		})
	}
}

func TestWindowsPowerDoesNotShutDownWithoutThePrivilege(t *testing.T) {
	called := false
	p := testPower(func() error { return errors.New("boom") }, func(bool) error { called = true; return nil })
	if err := p.PowerOff(context.Background()); err == nil || called {
		t.Fatalf("err %v, initiate called %v", err, called)
	}
}

func TestWindowsPowerAccessDeniedExplainsTheFix(t *testing.T) {
	p := testPower(func() error { return nil }, func(bool) error { return windows.ERROR_ACCESS_DENIED })
	err := p.PowerOff(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Administrator") {
		t.Fatalf("got %v", err)
	}
}

func TestWindowsPowerHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	p := testPower(func() error { called = true; return nil }, func(bool) error { called = true; return nil })
	if err := p.PowerOff(ctx); err == nil || called {
		t.Fatalf("err %v, called %v", err, called)
	}
}
