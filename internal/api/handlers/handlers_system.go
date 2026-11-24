package handlers

import (
	"math"
	"net/http"
	"slices"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/platform"
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

// GetSystemMetrics reports the host's CPU, memory, disk, load, uptime, addresses and the
// Controller process. A metric the platform cannot provide is left out and named in `restricted`.
func (h *Handlers) GetSystemMetrics(w http.ResponseWriter, r *http.Request) {
	if h.Metrics == nil {
		h.Unimplemented.GetSystemMetrics(w, r)
		return
	}
	m, err := h.Metrics.Collect(r.Context())
	if err != nil {
		h.apiError(w, r, "system metrics", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIMetrics(m))
}

func toAPIMetrics(m platform.Metrics) gen.SystemMetrics {
	out := gen.SystemMetrics{
		CollectedAt: m.CollectedAt.UTC(), Platform: m.Platform, Hostname: nonEmpty(m.Hostname),
		Kernel: nonEmpty(m.Kernel), CpuPercent: m.CPUPercent, Restricted: ptr(append([]string{}, m.Restricted...)),
	}
	if m.UptimeSeconds > 0 {
		out.UptimeSeconds = ptr(m.UptimeSeconds)
	}
	if m.CPUCores > 0 {
		out.CpuCores = ptr(m.CPUCores)
	}
	if len(m.LoadAverage) == 3 {
		out.LoadAverage = ptr(append([]float64{}, m.LoadAverage...))
	}
	if len(m.LocalAddresses) > 0 {
		addresses := make([]struct {
			InterfaceName string `json:"interface_name"`
			IpAddress     string `json:"ip_address"`
		}, 0, len(m.LocalAddresses))
		for _, address := range m.LocalAddresses {
			addresses = append(addresses, struct {
				InterfaceName string `json:"interface_name"`
				IpAddress     string `json:"ip_address"`
			}{InterfaceName: address.InterfaceName, IpAddress: address.IPAddress})
		}
		out.LocalAddresses = &addresses
	}
	if len(m.LocalIPs) > 0 {
		out.LocalIps = ptr(append([]string{}, m.LocalIPs...))
	}
	if m.MemTotal > 0 {
		out.Memory = &struct {
			AvailableBytes *int64 `json:"available_bytes,omitempty"`
			TotalBytes     *int64 `json:"total_bytes,omitempty"`
			UsedBytes      *int64 `json:"used_bytes,omitempty"`
		}{AvailableBytes: bytesPtr(m.MemAvailable), TotalBytes: bytesPtr(m.MemTotal), UsedBytes: bytesPtr(m.MemUsed)}
	}
	if m.DiskTotal > 0 {
		out.Disk = &struct {
			FreeBytes  *int64  `json:"free_bytes,omitempty"`
			Path       *string `json:"path,omitempty"`
			TotalBytes *int64  `json:"total_bytes,omitempty"`
			UsedBytes  *int64  `json:"used_bytes,omitempty"`
		}{FreeBytes: bytesPtr(m.DiskFree), Path: nonEmpty(m.DiskPath), TotalBytes: bytesPtr(m.DiskTotal), UsedBytes: bytesPtr(m.DiskUsed)}
	}
	proc := &struct {
		Goroutines     *int   `json:"goroutines,omitempty"`
		HeapAllocBytes *int64 `json:"heap_alloc_bytes,omitempty"`
		Pid            *int   `json:"pid,omitempty"`
		RssBytes       *int64 `json:"rss_bytes,omitempty"`
		UptimeSeconds  *int64 `json:"uptime_seconds,omitempty"`
	}{}
	if m.PID > 0 {
		proc.Pid = ptr(m.PID)
	}
	if m.Goroutines > 0 {
		proc.Goroutines = ptr(m.Goroutines)
	}
	if m.HeapAllocBytes > 0 {
		proc.HeapAllocBytes = bytesPtr(m.HeapAllocBytes)
	}
	if m.ProcessRSS > 0 {
		proc.RssBytes = bytesPtr(m.ProcessRSS)
	}
	if !slices.Contains(m.Restricted, platform.MetricProcessUptime) {
		proc.UptimeSeconds = ptr(m.ProcessUptimeSeconds)
	}
	out.Process = proc
	return out
}

func bytesPtr(v uint64) *int64 {
	if v > math.MaxInt64 {
		v = math.MaxInt64
	}
	return ptr(int64(v))
}

func (h *Handlers) GetOpenAPISettings(w http.ResponseWriter, r *http.Request) {
	if h.SystemSettings == nil {
		httpx.WriteError(w, http.StatusNotImplemented, "not_implemented", "system settings are not configured")
		return
	}
	enabled, err := h.SystemSettings.OpenAPIEnabled(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "could not read OpenAPI setting")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, gen.OpenAPISettings{Enabled: enabled})
}

func (h *Handlers) UpdateOpenAPISettings(w http.ResponseWriter, r *http.Request) {
	if h.SystemSettings == nil {
		httpx.WriteError(w, http.StatusNotImplemented, "not_implemented", "system settings are not configured")
		return
	}
	var req gen.OpenAPISettings
	if !decodeStrict(w, r, &req) {
		return
	}
	if err := h.SystemSettings.SetOpenAPIEnabled(r.Context(), req.Enabled, h.Now()); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "could not save OpenAPI setting")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, req)
}
