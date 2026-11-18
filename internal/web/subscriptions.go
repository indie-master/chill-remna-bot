package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type MiniSubscriptionItemDTO struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	ExpireAt string `json:"expire_at"`
}

type MiniSubscriptionsDTO struct {
	Enabled    bool                      `json:"enabled"`
	SelectedID string                    `json:"selected_id,omitempty"`
	Items      []MiniSubscriptionItemDTO `json:"items"`
	Error      string                    `json:"error,omitempty"`
}

type subscriptionProvider interface {
	MiniSubscriptions(context.Context, int64) MiniSubscriptionsDTO
	MiniSelectSubscription(context.Context, int64, string) MiniActionDTO
}

type subscriptionScopeProvider interface {
	MiniSubscriptionScope(context.Context, int64, string) (context.Context, error)
}

func (s *Server) handleMiniSubscriptions(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.miniGuard(w, r)
	if !ok {
		return
	}
	p, ok := s.mini.(subscriptionProvider)
	if !ok {
		writeJSON(w, http.StatusOK, MiniSubscriptionsDTO{Items: []MiniSubscriptionItemDTO{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, p.MiniSubscriptions(ctx, id))
}

func (s *Server) handleMiniSelectSubscription(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.miniGuard(w, r)
	if !ok {
		return
	}
	p, ok := s.mini.(subscriptionProvider)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request) != nil || len(request.ID) == 0 || len(request.ID) > 128 {
		writeJSON(w, http.StatusBadRequest, MiniActionDTO{Error: "Выберите подписку из списка"})
		return
	}
	writeJSON(w, http.StatusOK, p.MiniSelectSubscription(r.Context(), id, request.ID))
}
