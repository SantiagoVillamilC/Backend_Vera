package copilot

import("encoding/json";"net/http")
type Handler struct{service *Service}
func NewHandler(s *Service)*Handler{return &Handler{service:s}}
func(h *Handler) Chat(w http.ResponseWriter,r *http.Request){var req Request;if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{http.Error(w,"cuerpo inválido",400);return};result,err:=h.service.Respond(r.Context(),req);if err!=nil{http.Error(w,err.Error(),422);return};json.NewEncoder(w).Encode(result)}
