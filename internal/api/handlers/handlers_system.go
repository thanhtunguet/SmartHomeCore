package handlers

import (
	"net/http"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// GetControllerKey returns the active Controller public key (never the private key) for an
// operator to paste into an Agent's trusted_controllers.
func (h *Handlers) GetControllerKey(w http.ResponseWriter, r *http.Request) {
	if h.ControllerKey == nil {
		h.Unimplemented.GetControllerKey(w, r)
		return
	}
	k := h.ControllerKey.Public()
	httpx.WriteJSON(w, http.StatusOK, gen.ControllerKey{
		KeyId: k.KeyID, Algorithm: gen.ControllerKeyAlgorithm(k.Algorithm), PublicKey: k.PublicKey,
	})
}
