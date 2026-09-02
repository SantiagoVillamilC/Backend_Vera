package wallets

import ("encoding/json"; "net/http")
type Handler struct { service *Service }
func NewHandler(s *Service) *Handler { return &Handler{service:s} }
func (h *Handler) List(w http.ResponseWriter,r *http.Request) { result,err:=h.service.List(r.Context(),r.Header.Get("X-User-ID")); if err!=nil {http.Error(w,err.Error(),422);return}; json.NewEncoder(w).Encode(result) }
func (h *Handler) Create(w http.ResponseWriter,r *http.Request) { var req CreateWalletRequest;if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{http.Error(w,"cuerpo inválido",400);return}; result,err:=h.service.Create(r.Context(),r.Header.Get("X-User-ID"),req);if err!=nil{http.Error(w,err.Error(),422);return};json.NewEncoder(w).Encode(result) }
