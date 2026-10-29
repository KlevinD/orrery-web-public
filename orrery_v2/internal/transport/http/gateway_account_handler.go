package httptransport

import (
	"net/http"

	"orrery_v2/internal/gateway/channel"
)

type gatewayAccountsDTO struct {
	Accounts []channel.AccountStatus `json:"accounts"`
}

func (r *Router) handleGetGatewayAccounts(w http.ResponseWriter, req *http.Request) {
	if r.gatewayAccounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_accounts_unavailable", Message: "Gateway account status is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	accounts := r.gatewayAccounts.GatewayAccountStatuses()
	if accounts == nil {
		accounts = []channel.AccountStatus{}
	}
	writeJSON(w, http.StatusOK, gatewayAccountsDTO{Accounts: accounts})
}
