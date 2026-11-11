// Command devtools contains developer utilities. Not shipped in releases.
//
//	devtools keygen -out controller.key          # Ed25519 key pair; prints public key + key id
//	devtools send -key controller.key -agent http://127.0.0.1:9750 -agent-id <uuid> [-action shutdown]
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "send":
		err = send(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devtools:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: devtools keygen|send [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "controller.key", "file for the private key (base64 seed, mode 0600)")
	_ = fs.Parse(args)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		return err
	}
	fmt.Printf("private key written to %s\nkey_id:     %s\npublic_key: %s\n", *out, protocol.KeyID(pub), protocol.EncodePublicKey(pub))
	return nil
}

func send(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	keyFile := fs.String("key", "controller.key", "private key file from keygen")
	agentURL := fs.String("agent", "http://127.0.0.1:9750", "agent base URL")
	agentID := fs.String("agent-id", "", "target agent_id (printed by the agent at startup)")
	action := fs.String("action", "shutdown", "shutdown | reboot")
	commandID := fs.String("command-id", "", "command id (default: new UUIDv7; reuse to test idempotency)")
	_ = fs.Parse(args)
	if *agentID == "" {
		return errors.New("-agent-id is required")
	}
	if *commandID == "" {
		*commandID = uuid.Must(uuid.NewV7()).String()
	}

	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("invalid key file")
	}
	priv := ed25519.NewKeyFromSeed(seed)

	cmd, err := protocol.NewCommand(*commandID, *agentID, protocol.Action(*action), time.Now())
	if err != nil {
		return err
	}
	req, err := protocol.Sign(priv, cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(*agentURL, "/")+protocol.CommandsPath, bytes.NewReader(req.Body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(protocol.HeaderKeyID, req.KeyID)
	httpReq.Header.Set(protocol.HeaderSignature, req.Signature)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	fmt.Printf("HTTP %d\n%s", resp.StatusCode, body)
	return nil
}
