package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// LoadOrCreateID returns the persistent agent_id stored in <dir>/agent_id, creating it on first
// start (ADR-002).
func LoadOrCreateID(dir string) (string, error) {
	path := filepath.Join(dir, "agent_id")
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if _, err := uuid.Parse(id); err != nil {
			return "", fmt.Errorf("%s: corrupt agent id", path)
		}
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := uuid.Must(uuid.NewV7()).String()
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write agent id: %w", err)
	}
	return id, nil
}
