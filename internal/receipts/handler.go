package receipts

import (
	"errors"
	"io"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// Handler expone el endpoint de carga de recibos.
type Handler struct{ service *Service }

// NewHandler construye el handler sobre el service de receipts.
func NewHandler(s *Service) *Handler { return &Handler{service: s} }

// Create recibe la imagen del recibo en el cuerpo crudo y la procesa.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	image, err := io.ReadAll(r.Body)
	if err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			api.WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", "Request body exceeds 1 MiB.", nil)
			return
		}
		api.WriteError(w, http.StatusBadRequest, "BadRequest", "imagen requerida", nil)
		return
	}
	if len(image) == 0 {
		api.WriteError(w, http.StatusBadRequest, "BadRequest", "imagen requerida", nil)
		return
	}
	result, err := h.service.Process(r.Context(), image)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, result, nil)
}
