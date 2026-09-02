package auth

import ("encoding/json"; "net/http")
type Handler struct { service *Service }
func NewHandler(s *Service) *Handler { return &Handler{service:s} }
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) { h.respond(w, r, func(req LoginRequest) (any,error) { return h.service.Login(r.Context(), req) }) }
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) { h.respond(w, r, func(req LoginRequest) (any,error) { return h.service.Register(r.Context(), req) }) }
func (h *Handler) VerifyMFA(w http.ResponseWriter, r *http.Request) { http.Error(w, "TODO: verify MFA", http.StatusNotImplemented) }
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, fn func(LoginRequest)(any,error)) { var req LoginRequest; if err:=json.NewDecoder(r.Body).Decode(&req); err!=nil { http.Error(w,"cuerpo inválido",400); return }; result,err:=fn(req); if err!=nil { http.Error(w,err.Error(),422); return }; json.NewEncoder(w).Encode(result) }
