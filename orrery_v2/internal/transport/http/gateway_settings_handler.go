package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"

	gwconfig "orrery_v2/internal/gateway/config"
)

type GatewaySettingsDTO struct {
	ConfigPath string          `json:"config_path"`
	RawYAML    string          `json:"raw_yaml"`
	Config     gwconfig.Config `json:"config"`
}

type gatewaySettingsUpdateRequest struct {
	Config gwconfig.Config `json:"config"`
}

type gatewaySettingsRawUpdateRequest struct {
	RawYAML string `json:"raw_yaml"`
}

const maxGatewaySettingsRequestBytes = 1 << 20

func defaultGatewaySettingsStore() *gwconfig.Store {
	return gwconfig.NewStore("config/gateway.yaml")
}

func (r *Router) gatewayStore() *gwconfig.Store {
	if r.gatewaySettings != nil {
		return r.gatewaySettings
	}
	return defaultGatewaySettingsStore()
}

func (r *Router) handleGetGatewaySettings(w http.ResponseWriter, req *http.Request) {
	record, err := r.gatewayStore().LoadOrDefault()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_settings_invalid", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, gatewaySettingsDTO(gatewaySettingsPublicRecord(record)))
}

func (r *Router) handlePutGatewaySettings(w http.ResponseWriter, req *http.Request) {
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewaySettingsRequestBytes)
	}
	var body gatewaySettingsUpdateRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	store := r.gatewayStore()
	record, err := store.UpdateAndApply(func(existing *gwconfig.Config) error {
		*existing = gwconfig.MergeProtectedWeixinBindings(gwconfig.MergeMaskedConfig(body.Config, *existing), *existing)
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewaySettings(req, cfg)
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: gatewaySettingsUpdateErrorCode(err), Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, gatewaySettingsDTO(gatewaySettingsPublicRecord(record)))
}

func (r *Router) handlePutGatewaySettingsRaw(w http.ResponseWriter, req *http.Request) {
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewaySettingsRequestBytes)
	}
	var body gatewaySettingsRawUpdateRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	store := r.gatewayStore()
	next := gwconfig.DefaultConfig()
	if err := gwconfig.DecodeConfigYAML(body.RawYAML, &next); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_settings_invalid", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	record, err := store.UpdateAndApply(func(existing *gwconfig.Config) error {
		*existing = gwconfig.MergeProtectedWeixinBindings(gwconfig.MergeMaskedConfig(next, *existing), *existing)
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewaySettings(req, cfg)
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: gatewaySettingsUpdateErrorCode(err), Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, gatewaySettingsDTO(gatewaySettingsPublicRecord(record)))
}

func gatewaySettingsUpdateErrorCode(err error) string {
	if gwconfig.IsApplyError(err) {
		return "gateway_settings_reload_failed"
	}
	return "gateway_settings_invalid"
}

func (r *Router) reloadGatewaySettings(req *http.Request, cfg gwconfig.Config) error {
	if r.gatewaySettingsChanged == nil {
		return errors.New("Gateway runtime reload service is unavailable")
	}
	return r.gatewaySettingsChanged(req.Context(), cfg)
}

func (r *Router) reloadGatewayBindings(req *http.Request, cfg gwconfig.Config) error {
	if r.gatewayBindingsChanged != nil {
		return r.gatewayBindingsChanged(req.Context(), cfg)
	}
	return r.reloadGatewaySettings(req, cfg)
}

func gatewaySettingsDTO(record gwconfig.Record) GatewaySettingsDTO {
	return GatewaySettingsDTO{
		ConfigPath: record.Path,
		RawYAML:    record.RawYAML,
		Config:     record.Config,
	}
}

func gatewaySettingsPublicRecord(record gwconfig.Record) gwconfig.Record {
	record.Config = gwconfig.RedactPrivateWeixinBindings(record.Config)
	return record.Masked()
}
