package transactions

import ("encoding/json"; "net/http")
type Handler struct { service *Service }
func NewHandler(s *Service) *Handler{return &Handler{service:s}}
// Create handles POST /transactions. Authentication middleware has already validated the JWT.
func(h *Handler) Create(w http.ResponseWriter,r *http.Request){var req CreateTransactionRequest;if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{http.Error(w,"cuerpo inválido",400);return};result,err:=h.service.Execute(r.Context(),req);if err!=nil{http.Error(w,err.Error(),422);return};json.NewEncoder(w).Encode(result)}
