package receipts

import("io";"encoding/json";"net/http")
type Handler struct{service *Service}
func NewHandler(s *Service)*Handler{return &Handler{service:s}}
func(h *Handler) Create(w http.ResponseWriter,r *http.Request){image,err:=io.ReadAll(r.Body);if err!=nil||len(image)==0{http.Error(w,"imagen requerida",400);return};result,err:=h.service.Process(r.Context(),image);if err!=nil{http.Error(w,err.Error(),422);return};json.NewEncoder(w).Encode(result)}
